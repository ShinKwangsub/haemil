package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

// TestREPLCompactBelowThreshold: /compact on a short session prints the
// "skipped" message and does not mutate the in-memory list.
func TestREPLCompactBelowThreshold(t *testing.T) {
	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	dir := t.TempDir()
	sess, _ := runtime.NewSession(dir)
	defer sess.Close()
	// Two-message session, well below any threshold.
	_ = sess.AppendUser(runtime.Message{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "hi"}}})
	_ = sess.AppendAssistant(runtime.Message{Role: runtime.RoleAssistant, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "hello"}}})
	before := len(sess.Messages())

	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("/compact\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	_ = runREPL(context.Background(), cfg, rt)
	out := stdout.String()
	if !strings.Contains(out, "below threshold") {
		t.Errorf("expected 'below threshold' skip note, got %q", out)
	}
	after := len(sess.Messages())
	if after != before {
		t.Errorf("message count changed: got %d, want %d (no compaction should occur)", after, before)
	}
}

// TestREPLCompactAboveThreshold: a verbose session crosses the claw-code
// default (~10k tokens). /compact should report a reduction.
func TestREPLCompactAboveThreshold(t *testing.T) {
	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	dir := t.TempDir()
	sess, _ := runtime.NewSession(dir)
	defer sess.Close()
	// ~5k chars per message × 8 messages = ~10k tokens.
	blob := strings.Repeat("x", 5000)
	for i := 0; i < 4; i++ {
		_ = sess.AppendUser(runtime.Message{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "u " + blob}}})
		_ = sess.AppendAssistant(runtime.Message{Role: runtime.RoleAssistant, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "a " + blob}}})
	}
	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("/compact\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	_ = runREPL(context.Background(), cfg, rt)
	out := stdout.String()
	if !strings.Contains(out, "removed") {
		t.Errorf("expected compact summary mentioning 'removed', got %q", out)
	}
	// After compaction the in-memory list should be shorter than 8.
	if got := len(sess.Messages()); got >= 8 {
		t.Errorf("post-compact len: got %d, expected < 8", got)
	}
}

// TestREPLMemoryAndRemember: /remember writes, /memory shows it back.
// Redirects HOME + cwd to a temp dir so we don't pollute the user's
// real ~/.haemil/USER.md or project's .haemil/MEMORY.md.
func TestREPLMemoryAndRemember(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// chdir to a fresh project dir so project MEMORY.md lives there.
	prevCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	projDir := tmp + "/proj"
	if err := os.Mkdir(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prevCwd)

	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	sess, _ := runtime.NewSession(tmp + "/sessions")
	defer sess.Close()
	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("/memory\n/remember 광섭 uses 반말\n/remember-user likes short responses\n/memory\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	_ = runREPL(context.Background(), cfg, rt)
	out := stdout.String()

	// First /memory: empty state.
	if !strings.Contains(out, "memory: empty") {
		t.Errorf("expected initial empty state, got %q", out)
	}
	// Remembered output.
	if !strings.Contains(out, "remembered in") {
		t.Errorf("expected 'remembered in' confirmation, got %q", out)
	}
	// Second /memory: the two bullets should appear.
	if !strings.Contains(out, "광섭 uses 반말") {
		t.Errorf("project memory bullet missing, got %q", out)
	}
	if !strings.Contains(out, "likes short responses") {
		t.Errorf("user memory bullet missing, got %q", out)
	}
}

// TestREPLRememberEmptyShowsUsage verifies /remember with no args prints
// a usage hint instead of silently succeeding.
func TestREPLRememberEmptyShowsUsage(t *testing.T) {
	provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
	dir := t.TempDir()
	sess, _ := runtime.NewSession(dir)
	defer sess.Close()
	rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

	stdin := strings.NewReader("/remember\n/exit\n")
	var stdout, stderr bytes.Buffer
	cfg := Config{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}

	_ = runREPL(context.Background(), cfg, rt)
	if !strings.Contains(stdout.String(), "usage: /remember") {
		t.Errorf("expected usage hint, got %q", stdout.String())
	}
}

// TestTwoTenantsDoNotCrosstalk is the C9 acceptance test. Two REPL
// sessions running back-to-back in the same process against independent
// Workspace/HomeDir pairs must write to disjoint MEMORY.md / USER.md /
// session files — no leakage either direction.
//
// We drive /remember + /remember-user through runREPL to exercise the
// full Config → memoryContextFromConfig → runtime.ResolveTenant path.
func TestTwoTenantsDoNotCrosstalk(t *testing.T) {
	tenantA := struct{ workspace, home string }{t.TempDir(), t.TempDir()}
	tenantB := struct{ workspace, home string }{t.TempDir(), t.TempDir()}

	runOne := func(ws, home, projectBullet, userBullet string) {
		t.Helper()
		provider := &fakeProvider{responses: []*runtime.ChatResponse{}}
		sess, err := runtime.NewSession(home + "/.haemil/sessions")
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer sess.Close()
		rt := runtime.New(provider, nil, sess, runtime.Options{MaxIterations: 10, MaxTokens: 1024})

		stdin := strings.NewReader(
			"/remember " + projectBullet + "\n" +
				"/remember-user " + userBullet + "\n" +
				"/exit\n",
		)
		var stdout, stderr bytes.Buffer
		cfg := Config{
			Stdin:     stdin,
			Stdout:    &stdout,
			Stderr:    &stderr,
			Workspace: ws,
			HomeDir:   home,
		}
		if err := runREPL(context.Background(), cfg, rt); err != nil {
			t.Fatalf("runREPL: %v", err)
		}
		if !strings.Contains(stdout.String(), "remembered in") {
			t.Fatalf("expected 'remembered in' confirmation, got %q", stdout.String())
		}
	}

	runOne(tenantA.workspace, tenantA.home, "AAA-project-fact", "AAA-user-fact")
	runOne(tenantB.workspace, tenantB.home, "BBB-project-fact", "BBB-user-fact")

	readFile := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(b)
	}

	memA := readFile(tenantA.workspace + "/.haemil/MEMORY.md")
	memB := readFile(tenantB.workspace + "/.haemil/MEMORY.md")
	usrA := readFile(tenantA.home + "/.haemil/USER.md")
	usrB := readFile(tenantB.home + "/.haemil/USER.md")

	// Each file must contain its own tenant's bullet and NOT the other.
	checks := []struct {
		name string
		body string
		want string
		deny string
	}{
		{"tenantA project", memA, "AAA-project-fact", "BBB-project-fact"},
		{"tenantB project", memB, "BBB-project-fact", "AAA-project-fact"},
		{"tenantA user", usrA, "AAA-user-fact", "BBB-user-fact"},
		{"tenantB user", usrB, "BBB-user-fact", "AAA-user-fact"},
	}
	for _, c := range checks {
		if !strings.Contains(c.body, c.want) {
			t.Errorf("%s missing own bullet %q: body=%q", c.name, c.want, c.body)
		}
		if strings.Contains(c.body, c.deny) {
			t.Errorf("%s leaked other tenant's bullet %q: body=%q", c.name, c.deny, c.body)
		}
	}

	// Cross-check the opposing tenant's MEMORY.md file simply shouldn't
	// exist at tenantA's workspace path with tenantB's content — the
	// Contains checks above already prove it, but also ensure no
	// unexpected file appeared at the other tenant's home.
	if _, err := os.Stat(tenantA.home + "/.haemil/MEMORY.md"); err == nil {
		t.Errorf("tenantA home should not contain MEMORY.md (project-local lives under workspace)")
	}
	if _, err := os.Stat(tenantB.workspace + "/.haemil/USER.md"); err == nil {
		t.Errorf("tenantB workspace should not contain USER.md (user-global lives under home)")
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
