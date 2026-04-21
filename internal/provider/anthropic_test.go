package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// TestSSEScannerParsesFrames covers the core SSE parsing contract:
//   - Blank lines separate frames
//   - `event:` and `data:` fields captured
//   - `:` comments ignored
//   - Multi-line `data:` concatenated with newlines
//   - CRLF tolerated
//   - Stream ends cleanly on EOF
func TestSSEScannerParsesFrames(t *testing.T) {
	raw := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		": this is a comment\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0}\n\n" +
		"event: ping\ndata: ping payload\n\n" +
		// CRLF frame
		"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"

	s := newSSEScanner(strings.NewReader(raw))

	var got []sseEvent
	for {
		ev, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("scan: %v", err)
		}
		if ev == nil {
			break
		}
		got = append(got, *ev)
	}

	wantEvents := []string{"message_start", "content_block_delta", "ping", "message_stop"}
	if len(got) != len(wantEvents) {
		t.Fatalf("event count: got %d, want %d", len(got), len(wantEvents))
	}
	for i, want := range wantEvents {
		if got[i].Event != want {
			t.Errorf("event[%d]: got %q, want %q", i, got[i].Event, want)
		}
	}
	// Spot-check data payloads parse as JSON
	var tmp map[string]any
	if err := json.Unmarshal(got[0].Data, &tmp); err != nil {
		t.Errorf("data[0] not valid JSON: %v (%s)", err, got[0].Data)
	}
}

// TestSSEScannerMultilineData verifies multi-line data: fields are joined
// with `\n` per the SSE spec.
func TestSSEScannerMultilineData(t *testing.T) {
	raw := "event: msg\ndata: line1\ndata: line2\ndata: line3\n\n"
	s := newSSEScanner(strings.NewReader(raw))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := string(ev.Data)
	want := "line1\nline2\nline3"
	if got != want {
		t.Errorf("multi-line data: got %q, want %q", got, want)
	}
}

// TestAnthropicChatAccumulates feeds a canned SSE stream through a
// httptest server and verifies the Chat method correctly accumulates
// into a ChatResponse: text deltas, tool_use with input_json_delta
// accumulation, usage, stop_reason.
func TestAnthropicChatAccumulates(t *testing.T) {
	sse := buildSSEStream([]sseFrame{
		{"message_start", `{"type":"message_start","message":{"id":"msg_01","model":"claude-test","usage":{"input_tokens":42,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world!"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"bash","input":{}}}`},
		// input JSON arrives as partial fragments — must be concatenated.
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"and\":\"ls"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"}"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17}}`},
		{"message_stop", `{"type":"message_stop"}`},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Header validation (gotchas #4, #5, #7, #12)
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key: got %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicAPIVersion {
			t.Errorf("anthropic-version: got %q, want %q", got, anthropicAPIVersion)
		}
		if got := r.Header.Get("accept"); got != "text/event-stream" {
			t.Errorf("accept: got %q, want text/event-stream", got)
		}
		if got := r.Header.Get("authorization"); got != "" {
			t.Errorf("should NOT send authorization bearer, got %q", got)
		}
		// Body validation: stream:true, max_tokens present
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		if err := json.Unmarshal(body, &reqMap); err != nil {
			t.Fatalf("request body not JSON: %v", err)
		}
		if reqMap["stream"] != true {
			t.Errorf("stream: got %v, want true", reqMap["stream"])
		}
		if _, ok := reqMap["max_tokens"]; !ok {
			t.Error("max_tokens missing in request body")
		}
		// Respond with SSE
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(sse)
	}))
	defer srv.Close()

	p := &anthropicProvider{
		apiKey: "test-key",
		model:  "claude-test",
		http:   &http.Client{Timeout: 5 * time.Second},
	}
	p.setEndpoint(srv.URL)

	resp, err := p.Chat(context.Background(), runtime.ChatRequest{
		Model:     "claude-test",
		System:    "you are a test",
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "ls"}}}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if resp.ID != "msg_01" {
		t.Errorf("id: got %q, want msg_01", resp.ID)
	}
	if resp.Model != "claude-test" {
		t.Errorf("model: got %q, want claude-test", resp.Model)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason: got %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 42 {
		t.Errorf("input_tokens: got %d, want 42", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 17 {
		t.Errorf("output_tokens: got %d, want 17", resp.Usage.OutputTokens)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("content blocks: got %d, want 2", len(resp.Content))
	}
	// Block 0: text, concatenated
	if resp.Content[0].Type != runtime.BlockTypeText {
		t.Errorf("block[0] type: got %q", resp.Content[0].Type)
	}
	if resp.Content[0].Text != "Hello world!" {
		t.Errorf("block[0] text: got %q, want %q", resp.Content[0].Text, "Hello world!")
	}
	// Block 1: tool_use, input accumulated
	if resp.Content[1].Type != runtime.BlockTypeToolUse {
		t.Errorf("block[1] type: got %q", resp.Content[1].Type)
	}
	if resp.Content[1].ID != "toolu_01" {
		t.Errorf("block[1] id: got %q", resp.Content[1].ID)
	}
	if resp.Content[1].Name != "bash" {
		t.Errorf("block[1] name: got %q", resp.Content[1].Name)
	}
	// Input was streamed as "{\"comm" + "and\":\"ls" + "\"}" = `{"command":"ls"}`
	var inp map[string]string
	if err := json.Unmarshal(resp.Content[1].Input, &inp); err != nil {
		t.Errorf("block[1] input not valid JSON: %v (raw=%s)", err, resp.Content[1].Input)
	}
	if inp["command"] != "ls" {
		t.Errorf("block[1] input.command: got %q, want ls", inp["command"])
	}
}

// TestAnthropicChatHTTPError verifies non-200 responses return a classified error.
func TestAnthropicChatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens is required"}}`)
	}))
	defer srv.Close()

	p := &anthropicProvider{
		apiKey: "k",
		model:  "m",
		http:   &http.Client{Timeout: 5 * time.Second},
	}
	p.setEndpoint(srv.URL)

	_, err := p.Chat(context.Background(), runtime.ChatRequest{MaxTokens: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_request_error") || !strings.Contains(err.Error(), "max_tokens is required") {
		t.Errorf("error message: %q", err.Error())
	}
}

// TestAnthropicChatCtxCancel verifies ctx cancellation aborts in-flight requests.
func TestAnthropicChatCtxCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fl.Flush()
		// Block until the client disconnects.
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &anthropicProvider{
		apiKey: "k",
		model:  "m",
		http:   &http.Client{Timeout: 5 * time.Second},
	}
	p.setEndpoint(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Chat(ctx, runtime.ChatRequest{MaxTokens: 1})
		done <- err
	}()

	// Give the request a moment to reach the server
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after cancel, got nil")
		}
		// err should reference context cancellation
		if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancel") {
			t.Errorf("expected context/cancel in error, got %q", err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Chat did not return after ctx cancel")
	}
}

// TestAnthropicChatMissingKey verifies an explicit error for empty apiKey.
func TestAnthropicChatMissingKey(t *testing.T) {
	p := &anthropicProvider{
		apiKey: "",
		model:  "m",
		http:   &http.Client{Timeout: 5 * time.Second},
	}
	_, err := p.Chat(context.Background(), runtime.ChatRequest{MaxTokens: 1})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error message: %q", err.Error())
	}
}

// ---- helpers ----

type sseFrame struct {
	Event string
	Data  string
}

func buildSSEStream(frames []sseFrame) []byte {
	var b bytes.Buffer
	for _, f := range frames {
		b.WriteString("event: ")
		b.WriteString(f.Event)
		b.WriteString("\ndata: ")
		b.WriteString(f.Data)
		b.WriteString("\n\n")
	}
	return b.Bytes()
}
