package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// fakeServerProvider echoes the last user turn back so we can assert
// round-trip. goroutine-safe.
type fakeServerProvider struct{ callN atomic.Int32 }

func (p *fakeServerProvider) Name() string { return "fake" }
func (p *fakeServerProvider) Chat(ctx context.Context, req runtime.ChatRequest) (*runtime.ChatResponse, error) {
	p.callN.Add(1)
	var echo string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == runtime.RoleUser {
			for _, b := range req.Messages[i].Content {
				if b.Type == runtime.BlockTypeText {
					echo = b.Text
					break
				}
			}
			break
		}
	}
	return &runtime.ChatResponse{
		Content:    []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "echo:" + echo}},
		StopReason: "end_turn",
	}, nil
}

// buildServerFixture stands up a Supervisor with one registered tenant
// and returns it along with the shared EventBus. Session + wiring
// cleanup runs on t.Cleanup.
func buildServerFixture(t *testing.T) (*runtime.Supervisor, *runtime.EventBus) {
	t.Helper()
	sess, err := runtime.NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	rt := runtime.New(&fakeServerProvider{}, nil, sess, runtime.Options{
		MaxIterations: 3,
		MaxTokens:     256,
	})
	tenant := runtime.TenantContext{
		ID:        "default",
		Workspace: t.TempDir(),
		HomeDir:   t.TempDir(),
	}
	bus := runtime.NewEventBus()
	sup := runtime.NewSupervisor()
	if err := sup.Register(tenant, rt, runtime.RegisterOpts{EventBus: bus, QueueSize: 4}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		_ = sup.Close(context.Background())
		_ = bus.Close()
	})
	return sup, bus
}

func TestHealthz(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "ok") {
		t.Errorf("body: %q", b)
	}
}

func TestPostTurnRoundtrip(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	body, _ := json.Marshal(TurnRequest{Input: "hello"})
	resp, err := http.Post(srv.URL+"/v1/turn", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body: %s", resp.StatusCode, b)
	}

	var out TurnResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.TenantID != "default" {
		t.Errorf("tenant_id: %q", out.TenantID)
	}
	if out.Summary == nil || len(out.Summary.AssistantMessages) == 0 {
		t.Fatalf("summary: %+v", out.Summary)
	}
	got := out.Summary.AssistantMessages[0].Content[0].Text
	if got != "echo:hello" {
		t.Errorf("assistant text: %q, want echo:hello", got)
	}
	if out.Summary.StopReason != "end_turn" {
		t.Errorf("stop_reason: %q", out.Summary.StopReason)
	}
}

func TestPostTurnEmptyInput(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	body, _ := json.Marshal(TurnRequest{Input: "   "})
	resp, err := http.Post(srv.URL+"/v1/turn", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: %d, want 400", resp.StatusCode)
	}
}

func TestUnknownRoute(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: %d, want 404", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/turn")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d, want 405", resp.StatusCode)
	}
}

func TestPostTurnUnknownFields(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	body := []byte(`{"input":"hi","rogue":true}`)
	resp, err := http.Post(srv.URL+"/v1/turn", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: %d, want 400", resp.StatusCode)
	}
}

// TestGetEventsSSE verifies an SSE subscriber sees a turn.completed
// event after POST /v1/turn.
func TestGetEventsSSE(t *testing.T) {
	sup, bus := buildServerFixture(t)
	srv := httptest.NewServer(newMux(sup, bus, "default"))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type: %q", ct)
	}

	// Frame reader goroutine.
	frames := make(chan string, 16)
	go func() {
		defer close(frames)
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			frames <- strings.TrimRight(line, "\r\n")
		}
	}()

	// Let the Subscribe register before we fire.
	time.Sleep(50 * time.Millisecond)

	body, _ := json.Marshal(TurnRequest{Input: "sse-hi"})
	turnResp, err := http.Post(srv.URL+"/v1/turn", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, turnResp.Body)
	turnResp.Body.Close()

	foundEvent := false
	var dataLine string
	deadline := time.After(2 * time.Second)
collecting:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				break collecting
			}
			if strings.HasPrefix(f, "event: ") &&
				strings.TrimPrefix(f, "event: ") == runtime.TurnCompletedEventType {
				foundEvent = true
				continue
			}
			if foundEvent && strings.HasPrefix(f, "data: ") {
				dataLine = strings.TrimPrefix(f, "data: ")
				break collecting
			}
		case <-deadline:
			t.Fatalf("SSE timeout (event=%v, data=%q)", foundEvent, dataLine)
		}
	}

	var ev runtime.Event
	if err := json.Unmarshal([]byte(dataLine), &ev); err != nil {
		t.Fatalf("decode event: %v; raw=%q", err, dataLine)
	}
	if ev.TenantID != "default" {
		t.Errorf("event TenantID: %q", ev.TenantID)
	}
	var payload runtime.TurnCompletedPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Input != "sse-hi" {
		t.Errorf("payload.Input: %q", payload.Input)
	}
}

// TestGracefulShutdown verifies the http.Server + Supervisor shutdown
// handshake: Shutdown returns promptly even with no in-flight requests,
// and the Serve goroutine exits with http.ErrServerClosed.
func TestGracefulShutdown(t *testing.T) {
	sup, bus := buildServerFixture(t)

	httpSrv := &http.Server{
		Handler:           newMux(sup, bus, "default"),
		ReadHeaderTimeout: time.Second,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpSrv.Serve(ln)
	}()

	// Sanity: server responds.
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("serve returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve goroutine did not exit")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %v", d)
	}
}
