package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProvider scripts a sequence of ChatResponse values to return on
// successive Chat calls, plus optional hooks for error / cancel testing.
type fakeProvider struct {
	name      string
	responses []*ChatResponse
	calls     int32
	err       error
	blockFor  time.Duration
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.blockFor > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.blockFor):
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	n := int(atomic.AddInt32(&p.calls, 1))
	if n > len(p.responses) {
		return nil, errors.New("fakeProvider: scripted responses exhausted")
	}
	return p.responses[n-1], nil
}

// fakeTool executes a Go function on the tool input.
type fakeTool struct {
	name string
	run  func(ctx context.Context, input json.RawMessage) (string, error)
}

func (t *fakeTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        t.name,
		Description: "fake tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}
func (t *fakeTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	return t.run(ctx, input)
}

// TestRunTurnSingleRound: provider returns text with no tool_use → turn
// ends after one provider call.
func TestRunTurnSingleRound(t *testing.T) {
	provider := &fakeProvider{
		name: "fake",
		responses: []*ChatResponse{{
			ID:    "msg_01",
			Model: "test",
			Content: []ContentBlock{
				{Type: BlockTypeText, Text: "Hi there!"},
			},
			StopReason: "end_turn",
			Usage:      Usage{InputTokens: 10, OutputTokens: 5},
		}},
	}
	rt := New(provider, nil, nil, Options{MaxIterations: 10, MaxTokens: 1024})

	summary, err := rt.RunTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if summary.Iterations != 1 {
		t.Errorf("iterations: got %d, want 1", summary.Iterations)
	}
	if summary.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q, want end_turn", summary.StopReason)
	}
	if len(summary.AssistantMessages) != 1 {
		t.Errorf("assistant msgs: got %d, want 1", len(summary.AssistantMessages))
	}
	if summary.Usage.InputTokens != 10 || summary.Usage.OutputTokens != 5 {
		t.Errorf("usage: %+v", summary.Usage)
	}
	if len(summary.ToolCalls) != 0 {
		t.Errorf("tool calls: got %d, want 0", len(summary.ToolCalls))
	}
}

// TestRunTurnToolRound: provider returns tool_use, tool executes, provider
// returns final text. Verifies two rounds + tool call recorded.
func TestRunTurnToolRound(t *testing.T) {
	provider := &fakeProvider{
		name: "fake",
		responses: []*ChatResponse{
			{
				ID:    "msg_01",
				Model: "test",
				Content: []ContentBlock{
					{Type: BlockTypeText, Text: "let me check"},
					{Type: BlockTypeToolUse, ID: "toolu_1", Name: "echo_tool", Input: json.RawMessage(`{"msg":"hi"}`)},
				},
				StopReason: "tool_use",
				Usage:      Usage{InputTokens: 10, OutputTokens: 3},
			},
			{
				ID:    "msg_02",
				Model: "test",
				Content: []ContentBlock{
					{Type: BlockTypeText, Text: "tool said: hi"},
				},
				StopReason: "end_turn",
				Usage:      Usage{InputTokens: 20, OutputTokens: 4},
			},
		},
	}
	tool := &fakeTool{
		name: "echo_tool",
		run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var m map[string]string
			_ = json.Unmarshal(input, &m)
			return m["msg"], nil
		},
	}
	rt := New(provider, []Tool{tool}, nil, Options{MaxIterations: 10, MaxTokens: 1024})

	summary, err := rt.RunTurn(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if summary.Iterations != 2 {
		t.Errorf("iterations: got %d, want 2", summary.Iterations)
	}
	if summary.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q, want end_turn", summary.StopReason)
	}
	if len(summary.AssistantMessages) != 2 {
		t.Errorf("assistant msgs: got %d, want 2", len(summary.AssistantMessages))
	}
	if len(summary.ToolCalls) != 1 {
		t.Fatalf("tool calls: got %d, want 1", len(summary.ToolCalls))
	}
	tc := summary.ToolCalls[0]
	if tc.ToolName != "echo_tool" {
		t.Errorf("tool name: got %q", tc.ToolName)
	}
	if tc.Output != "hi" {
		t.Errorf("tool output: got %q, want hi", tc.Output)
	}
	if tc.IsError {
		t.Errorf("tool unexpectedly marked error")
	}
	// Aggregated usage
	if summary.Usage.InputTokens != 30 || summary.Usage.OutputTokens != 7 {
		t.Errorf("aggregated usage: %+v", summary.Usage)
	}
}

// TestRunTurnUnknownTool: provider calls a tool not in the registry.
// Loop should NOT crash — it records an error tool_result and the next
// provider response recovers.
func TestRunTurnUnknownTool(t *testing.T) {
	provider := &fakeProvider{
		name: "fake",
		responses: []*ChatResponse{
			{
				Content: []ContentBlock{
					{Type: BlockTypeToolUse, ID: "toolu_1", Name: "missing", Input: json.RawMessage(`{}`)},
				},
				StopReason: "tool_use",
			},
			{
				Content: []ContentBlock{
					{Type: BlockTypeText, Text: "ok, recovering"},
				},
				StopReason: "end_turn",
			},
		},
	}
	rt := New(provider, nil, nil, Options{MaxIterations: 10, MaxTokens: 1024})

	summary, err := rt.RunTurn(context.Background(), "x")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(summary.ToolCalls) != 1 {
		t.Fatalf("tool calls: got %d, want 1", len(summary.ToolCalls))
	}
	if !summary.ToolCalls[0].IsError {
		t.Error("unknown tool should be marked IsError")
	}
	if !strings.Contains(summary.ToolCalls[0].Output, "unknown tool") {
		t.Errorf("output: got %q, want 'unknown tool' mention", summary.ToolCalls[0].Output)
	}
}

// TestRunTurnMaxIterations: tool keeps returning more tool_use blocks → hit
// MaxIterations cap.
func TestRunTurnMaxIterations(t *testing.T) {
	// Every response is tool_use. Script enough for any MaxIterations.
	responses := make([]*ChatResponse, 20)
	for i := range responses {
		responses[i] = &ChatResponse{
			Content: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "toolu_x", Name: "loop_tool", Input: json.RawMessage(`{}`)},
			},
			StopReason: "tool_use",
		}
	}
	provider := &fakeProvider{name: "fake", responses: responses}
	tool := &fakeTool{
		name: "loop_tool",
		run: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "loop", nil
		},
	}
	rt := New(provider, []Tool{tool}, nil, Options{MaxIterations: 3, MaxTokens: 1024})

	summary, err := rt.RunTurn(context.Background(), "x")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if summary.Iterations != 3 {
		t.Errorf("iterations: got %d, want 3 (MaxIterations cap)", summary.Iterations)
	}
	if summary.StopReason != "max_iterations" {
		t.Errorf("stop_reason: got %q, want max_iterations", summary.StopReason)
	}
	if len(summary.ToolCalls) != 3 {
		t.Errorf("tool calls: got %d, want 3", len(summary.ToolCalls))
	}
}

// TestRunTurnCtxCancel: provider is slow; we cancel mid-turn → partial
// summary + ctx.Err().
func TestRunTurnCtxCancel(t *testing.T) {
	provider := &fakeProvider{
		name:     "fake",
		blockFor: 2 * time.Second,
	}
	rt := New(provider, nil, nil, Options{MaxIterations: 10, MaxTokens: 1024})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	summary, err := rt.RunTurn(ctx, "x")
	if err == nil {
		t.Fatal("expected error after cancel, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancel") {
		t.Errorf("expected context/cancel error, got %v", err)
	}
	if summary == nil {
		t.Fatal("partial summary should be non-nil even on cancel")
	}
}

// TestRunTurnProviderError: provider returns an error → summary contains
// the partial state before the failure.
func TestRunTurnProviderError(t *testing.T) {
	provider := &fakeProvider{
		name: "fake",
		err:  errors.New("boom"),
	}
	rt := New(provider, nil, nil, Options{MaxIterations: 10, MaxTokens: 1024})

	summary, err := rt.RunTurn(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error: got %q", err.Error())
	}
	if summary == nil {
		t.Fatal("summary should be non-nil")
	}
	if summary.Iterations != 1 {
		t.Errorf("iterations: got %d, want 1 (one failed attempt)", summary.Iterations)
	}
}

// TestRunTurnWithSession: session is wired — user + assistant messages
// are actually appended and match Messages().
func TestRunTurnWithSession(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	provider := &fakeProvider{
		name: "fake",
		responses: []*ChatResponse{{
			Content:    []ContentBlock{{Type: BlockTypeText, Text: "hi!"}},
			StopReason: "end_turn",
		}},
	}
	rt := New(provider, nil, sess, Options{MaxIterations: 10, MaxTokens: 1024})

	if _, err := rt.RunTurn(context.Background(), "greet"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	msgs := sess.Messages()
	if len(msgs) != 2 {
		t.Fatalf("session msgs: got %d, want 2 (user + assistant)", len(msgs))
	}
	if msgs[0].Role != RoleUser || msgs[0].Content[0].Text != "greet" {
		t.Errorf("msg[0] user: %+v", msgs[0])
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Content[0].Text != "hi!" {
		t.Errorf("msg[1] assistant: %+v", msgs[1])
	}
}
