package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// supFakeProvider is a goroutine-safe minimal Provider for supervisor
// tests. Unlike the fakeProvider in conversation_test.go (which uses an
// atomic counter but a fixed responses slice), this one echoes the input
// back so we can assert per-tenant isolation by looking at session
// content. 9-line deliberate duplicate — see plan §4 "fakeProvider 복제".
type supFakeProvider struct {
	name     string
	callN    int32
	blockFor time.Duration
}

func (p *supFakeProvider) Name() string { return p.name }

func (p *supFakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.blockFor > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.blockFor):
		}
	}
	atomic.AddInt32(&p.callN, 1)
	// Echo the last user text so tests can assert which tenant's input
	// landed in which session.
	var echo string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleUser {
			for _, b := range req.Messages[i].Content {
				if b.Type == BlockTypeText {
					echo = b.Text
					break
				}
			}
			break
		}
	}
	return &ChatResponse{
		Content:    []ContentBlock{{Type: BlockTypeText, Text: "echo:" + echo}},
		StopReason: "end_turn",
	}, nil
}

func mkRuntime(t *testing.T, p Provider) *Runtime {
	t.Helper()
	dir := t.TempDir()
	sess, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return New(p, nil, sess, Options{MaxIterations: 5, MaxTokens: 512})
}

// TestSupervisorRegisterAndRunTurn: baseline — single tenant, 3 turns,
// responses come back in order.
func TestSupervisorRegisterAndRunTurn(t *testing.T) {
	sup := NewSupervisor()
	defer sup.Close(context.Background())

	rt := mkRuntime(t, &supFakeProvider{name: "A"})
	tenant := TenantContext{ID: "alpha", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	if err := sup.Register(tenant, rt, RegisterOpts{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := sup.Len(); got != 1 {
		t.Errorf("Len: got %d, want 1", got)
	}

	for i := 0; i < 3; i++ {
		summary, err := sup.RunTurn(context.Background(), "alpha", fmt.Sprintf("hi-%d", i))
		if err != nil {
			t.Fatalf("RunTurn %d: %v", i, err)
		}
		if summary.StopReason != "end_turn" {
			t.Errorf("turn %d stop: %q", i, summary.StopReason)
		}
		want := "echo:hi-" + fmt.Sprintf("%d", i)
		if summary.AssistantMessages[0].Content[0].Text != want {
			t.Errorf("turn %d echo: got %q, want %q",
				i, summary.AssistantMessages[0].Content[0].Text, want)
		}
	}
}

// TestSupervisorRejectsEmptyTenantID: tenant.ID must be explicit.
func TestSupervisorRejectsEmptyTenantID(t *testing.T) {
	sup := NewSupervisor()
	defer sup.Close(context.Background())
	rt := mkRuntime(t, &supFakeProvider{name: "p"})
	err := sup.Register(TenantContext{ID: "", Workspace: t.TempDir(), HomeDir: t.TempDir()}, rt, RegisterOpts{})
	if err == nil {
		t.Fatal("expected error on empty tenant.ID, got nil")
	}
	if !strings.Contains(err.Error(), "tenant.ID") {
		t.Errorf("error should mention tenant.ID, got %q", err.Error())
	}
	// Session should not have been registered; clean it up since Register
	// did not take ownership.
	rt.Session().Close()
}

// TestSupervisorRejectsDuplicateID: double-register must fail, not
// silently replace.
func TestSupervisorRejectsDuplicateID(t *testing.T) {
	sup := NewSupervisor()
	defer sup.Close(context.Background())
	tenant := TenantContext{ID: "dup", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	rt1 := mkRuntime(t, &supFakeProvider{name: "p1"})
	rt2 := mkRuntime(t, &supFakeProvider{name: "p2"})
	if err := sup.Register(tenant, rt1, RegisterOpts{}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := sup.Register(tenant, rt2, RegisterOpts{}); err == nil {
		t.Fatal("second Register should fail")
	}
	// rt2 never got ownership — close its session manually.
	rt2.Session().Close()
}

// TestSupervisorRunTurnAfterClose: post-Close dispatches are rejected.
func TestSupervisorRunTurnAfterClose(t *testing.T) {
	sup := NewSupervisor()
	rt := mkRuntime(t, &supFakeProvider{name: "p"})
	tenant := TenantContext{ID: "c", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	if err := sup.Register(tenant, rt, RegisterOpts{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := sup.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := sup.RunTurn(context.Background(), "c", "late")
	if !errors.Is(err, ErrSupervisorClosed) {
		t.Errorf("expected ErrSupervisorClosed, got %v", err)
	}
	// Second Close is a no-op.
	if err := sup.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestSupervisorContextCancelDuringEnqueue: with QueueSize=1 and a slow
// provider, the second RunTurn should block on enqueue. Cancelling its
// ctx must return ctx.Err() without wedging the first turn.
func TestSupervisorContextCancelDuringEnqueue(t *testing.T) {
	sup := NewSupervisor()
	defer sup.Close(context.Background())

	slow := &supFakeProvider{name: "slow", blockFor: 500 * time.Millisecond}
	rt := mkRuntime(t, slow)
	tenant := TenantContext{ID: "slow", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	if err := sup.Register(tenant, rt, RegisterOpts{QueueSize: 1}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Fire first turn in a goroutine — it will sit in the agent loop for ~500ms.
	firstDone := make(chan error, 1)
	go func() {
		_, err := sup.RunTurn(context.Background(), "slow", "first")
		firstDone <- err
	}()
	// Let the first turn enter the agent loop so the jobs channel drains.
	time.Sleep(50 * time.Millisecond)

	// Second turn: fill the queue (QueueSize=1), then a third attempt
	// should block on enqueue because the first is still running and the
	// second is parked in the channel.
	go func() {
		_, _ = sup.RunTurn(context.Background(), "slow", "second")
	}()
	time.Sleep(50 * time.Millisecond)

	// Third with a ctx we cancel — this one blocks on `agent.jobs <- job`.
	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan error, 1)
	go func() {
		_, err := sup.RunTurn(ctx, "slow", "third")
		cancelDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-cancelDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("third RunTurn: want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("third RunTurn did not observe cancellation")
	}

	// First turn must eventually complete (agent goroutine not wedged).
	select {
	case err := <-firstDone:
		if err != nil {
			t.Errorf("first RunTurn: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first RunTurn never completed — agent goroutine wedged")
	}
}

// TestSupervisorTwoTenantsNoCrosstalkRace (승인 테스트): two tenants, 50
// parallel RunTurns each, session JSONLs must contain only their own
// tenant's input strings. Run with -race to catch any shared-state
// mistakes.
func TestSupervisorTwoTenantsNoCrosstalkRace(t *testing.T) {
	sup := NewSupervisor()

	// Two independent providers and two independent sessions.
	provA := &supFakeProvider{name: "A"}
	provB := &supFakeProvider{name: "B"}
	rtA := mkRuntime(t, provA)
	rtB := mkRuntime(t, provB)
	tenA := TenantContext{ID: "alpha", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	tenB := TenantContext{ID: "beta", Workspace: t.TempDir(), HomeDir: t.TempDir()}

	if err := sup.Register(tenA, rtA, RegisterOpts{QueueSize: 16}); err != nil {
		t.Fatal(err)
	}
	if err := sup.Register(tenB, rtB, RegisterOpts{QueueSize: 16}); err != nil {
		t.Fatal(err)
	}

	// Capture session paths before Close closes the sessions.
	pathA := rtA.Session().Path()
	pathB := rtB.Session().Path()

	const N = 50
	var wg sync.WaitGroup
	wg.Add(2 * N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, _ = sup.RunTurn(context.Background(), "alpha", fmt.Sprintf("A-input-%03d", i))
		}()
		go func() {
			defer wg.Done()
			_, _ = sup.RunTurn(context.Background(), "beta", fmt.Sprintf("B-input-%03d", i))
		}()
	}
	wg.Wait()

	// Flush + close sessions so JSONL is readable.
	if err := sup.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Replay each session and assert isolation.
	msgsA, err := replaySession(pathA)
	if err != nil {
		t.Fatalf("replay A: %v", err)
	}
	msgsB, err := replaySession(pathB)
	if err != nil {
		t.Fatalf("replay B: %v", err)
	}

	assertOnlyPrefix := func(label, prefix string, msgs []Message) {
		t.Helper()
		seen := 0
		for _, m := range msgs {
			if m.Role != RoleUser {
				continue
			}
			for _, b := range m.Content {
				if b.Type != BlockTypeText {
					continue
				}
				if !strings.HasPrefix(b.Text, prefix) {
					t.Errorf("%s: foreign input in session: %q (want prefix %q)",
						label, b.Text, prefix)
				}
				seen++
			}
		}
		if seen != N {
			t.Errorf("%s: user inputs recorded = %d, want %d", label, seen, N)
		}
	}
	assertOnlyPrefix("tenant-A", "A-input-", msgsA)
	assertOnlyPrefix("tenant-B", "B-input-", msgsB)

	// Sanity: each provider handled its own half only.
	if got := atomic.LoadInt32(&provA.callN); got != N {
		t.Errorf("provA calls: %d, want %d", got, N)
	}
	if got := atomic.LoadInt32(&provB.callN); got != N {
		t.Errorf("provB calls: %d, want %d", got, N)
	}
}

// TestSupervisorIntraTenantSerialization: 50 goroutines fire RunTurn on
// the same tenant; the session must record inputs in the exact order
// RunTurn returned. (Proves one-goroutine-per-tenant serialization.)
func TestSupervisorIntraTenantSerialization(t *testing.T) {
	sup := NewSupervisor()
	rt := mkRuntime(t, &supFakeProvider{name: "seq"})
	tenant := TenantContext{ID: "seq", Workspace: t.TempDir(), HomeDir: t.TempDir()}
	if err := sup.Register(tenant, rt, RegisterOpts{QueueSize: 64}); err != nil {
		t.Fatal(err)
	}
	path := rt.Session().Path()

	const N = 50
	// Order of completion — appended as each RunTurn returns.
	var compMu sync.Mutex
	completionOrder := make([]string, 0, N)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			input := fmt.Sprintf("s-%03d", i)
			_, err := sup.RunTurn(context.Background(), "seq", input)
			if err != nil {
				t.Errorf("RunTurn %d: %v", i, err)
				return
			}
			compMu.Lock()
			completionOrder = append(completionOrder, input)
			compMu.Unlock()
		}()
	}
	wg.Wait()
	if err := sup.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Each RunTurn appends ONE user message synchronously under its own
	// goroutine turn; because one tenant = one agent goroutine, user
	// messages in the JSONL must appear in the same order the
	// goroutines' RunTurn calls returned.
	msgs, err := replaySession(path)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	var sessionOrder []string
	for _, m := range msgs {
		if m.Role != RoleUser {
			continue
		}
		for _, b := range m.Content {
			if b.Type == BlockTypeText {
				sessionOrder = append(sessionOrder, b.Text)
			}
		}
	}
	if len(sessionOrder) != N {
		t.Fatalf("session user msgs: got %d, want %d", len(sessionOrder), N)
	}
	if len(completionOrder) != N {
		t.Fatalf("completion order: got %d, want %d", len(completionOrder), N)
	}
	for i := 0; i < N; i++ {
		if sessionOrder[i] != completionOrder[i] {
			t.Errorf("order mismatch at %d: session=%q completion=%q",
				i, sessionOrder[i], completionOrder[i])
		}
	}
}
