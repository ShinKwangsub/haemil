package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// fakeProvider scripts a sequence of ChatResponses for REPL tests.
type fakeProvider struct {
	responses []*runtime.ChatResponse
	i         int
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Chat(_ context.Context, _ runtime.ChatRequest) (*runtime.ChatResponse, error) {
	if p.i >= len(p.responses) {
		return nil, nil
	}
	r := p.responses[p.i]
	p.i++
	return r, nil
}

// TestRenderSummaryText: basic text-only assistant message.
func TestRenderSummaryText(t *testing.T) {
	var buf bytes.Buffer
	renderSummary(&buf, &runtime.TurnSummary{
		AssistantMessages: []runtime.Message{{
			Role: runtime.RoleAssistant,
			Content: []runtime.ContentBlock{
				{Type: runtime.BlockTypeText, Text: "hello there"},
			},
		}},
		StopReason: "end_turn",
	})
	if !strings.Contains(buf.String(), "haemil > hello there") {
		t.Errorf("output: %q", buf.String())
	}
}

// TestRenderSummaryWithTool: tool use + result render.
func TestRenderSummaryWithTool(t *testing.T) {
	var buf bytes.Buffer
	renderSummary(&buf, &runtime.TurnSummary{
		AssistantMessages: []runtime.Message{{
			Role: runtime.RoleAssistant,
			Content: []runtime.ContentBlock{
				{Type: runtime.BlockTypeText, Text: "running ls"},
				{Type: runtime.BlockTypeToolUse, Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
			},
		}},
		ToolCalls: []runtime.ToolCallRecord{
			{ToolName: "bash", Input: `{"command":"ls"}`, Output: "file1\nfile2", IsError: false},
		},
		StopReason: "end_turn",
	})
	out := buf.String()
	if !strings.Contains(out, "[tool] bash") {
		t.Errorf("tool line missing: %q", out)
	}
	if !strings.Contains(out, "[result]") {
		t.Errorf("result line missing: %q", out)
	}
	if !strings.Contains(out, "file1") {
		t.Errorf("tool output missing: %q", out)
	}
}

// TestRenderSummaryMaxIterations: stop_reason != end_turn shows meta line.
func TestRenderSummaryMaxIterations(t *testing.T) {
	var buf bytes.Buffer
	renderSummary(&buf, &runtime.TurnSummary{
		AssistantMessages: []runtime.Message{{Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "..."}}}},
		StopReason:        "max_iterations",
		Iterations:        10,
	})
	if !strings.Contains(buf.String(), "max_iterations") {
		t.Errorf("stop meta missing: %q", buf.String())
	}
}

// TestSingleLineTruncation verifies the compact-display helper.
func TestSingleLineTruncation(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"", 10, ""},
		{"short", 100, "short"},
		{"line1\nline2", 100, "line1 ⏎ line2"},
		{"a very long text that needs cutting", 10, "a very lon …"},
	}
	for _, c := range cases {
		got := singleLine(c.in, c.max)
		if got != c.want {
			t.Errorf("singleLine(%q, %d): got %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// TestREPLSmoke: full pipe test. Feed "hello\n/exit\n" via stdin, verify
// we get the assistant response and a clean bye.
func TestREPLSmoke(t *testing.T) {
	// Build a runtime with a scripted provider that returns a text reply.
	provider := &fakeProvider{responses: []*runtime.ChatResponse{
		{
			Content:    []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "hi from haemil"}},
			StopReason: "end_turn",
		},
	}}
	dir := t.TempDir()
	sess, err := runtime.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("hello\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = runREPL(ctx, cfg, rt)
	if err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "hi from haemil") {
		t.Errorf("expected assistant text in output, got %q", out)
	}
	if !strings.Contains(out, "bye.") {
		t.Errorf("expected bye on /exit, got %q", out)
	}
}

// TestREPLEOF verifies that EOF on stdin exits cleanly (no error).
func TestREPLEOF(t *testing.T) {
	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	dir := t.TempDir()
	sess, _ := runtime.NewSession(dir)
	defer sess.Close()
	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("") // immediate EOF
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	if err := runREPL(context.Background(), cfg, rt); err != nil {
		t.Fatalf("runREPL on EOF: %v", err)
	}
}

// TestIsSlashCommand verifies only bare /<word> is treated as a command;
// paths like /tmp/foo or /Users/x fall through to the runtime as messages.
func TestIsSlashCommand(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/exit", true},
		{"/quit", true},
		{"/help", true},
		{"/foo", true},
		{"/foo bar baz", true},
		{"/foo_bar", true},
		{"/my-cmd", true},
		{"hello", false},
		{"", false},
		{"/", false},
		{"/tmp/foo", false},
		{"/Users/ayajin/haemil", false},
		{"/path/to/file.txt 를 읽어줘", false},
		{"/etc/hosts", false},
	}
	for _, c := range cases {
		got := isSlashCommand(c.in)
		if got != c.want {
			t.Errorf("isSlashCommand(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

// TestREPLUnknownSlash verifies /foo prints unknown-command hint.
func TestREPLUnknownSlash(t *testing.T) {
	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	dir := t.TempDir()
	sess, _ := runtime.NewSession(dir)
	defer sess.Close()
	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("/notreal\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	_ = runREPL(context.Background(), cfg, rt)
	out := stdout.String()
	if !strings.Contains(out, "unknown command") {
		t.Errorf("expected 'unknown command' hint, got %q", out)
	}
}
