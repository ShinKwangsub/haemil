package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- HookSpec.Matches --------------------------------------------------

func TestHookMatches(t *testing.T) {
	cases := []struct {
		matcher string
		tool    string
		want    bool
	}{
		{"", "bash", true},
		{"*", "bash", true},
		{"bash", "bash", true},
		{"BASH", "bash", true},
		{"bash", "mcp__fs__read", false},
		{"mcp", "mcp__fs__read", true},
		{"read", "mcp__fs__read_file", true},
	}
	for _, c := range cases {
		h := HookSpec{Matcher: c.matcher}
		if got := h.Matches(c.tool); got != c.want {
			t.Errorf("Matches(%q,%q): got %v, want %v", c.matcher, c.tool, got, c.want)
		}
	}
}

// --- LoadConfig --------------------------------------------------------

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing should be OK: %v", err)
	}
	if cfg == nil || len(cfg.PreToolUse)+len(cfg.PostToolUse) != 0 {
		t.Errorf("empty config expected, got %+v", cfg)
	}
}

func TestLoadConfigRoundtrip(t *testing.T) {
	body := `{"preToolUse":[{"matcher":"bash","command":"echo","args":["hi"]}],"postToolUse":[{"matcher":"*","command":"cat"}]}`
	path := filepath.Join(t.TempDir(), "hooks.json")
	_ = os.WriteFile(path, []byte(body), 0o600)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.PreToolUse) != 1 || cfg.PreToolUse[0].Command != "echo" {
		t.Errorf("pre: %+v", cfg.PreToolUse)
	}
	if len(cfg.PostToolUse) != 1 || cfg.PostToolUse[0].Command != "cat" {
		t.Errorf("post: %+v", cfg.PostToolUse)
	}
}

// --- RunPre / RunPost with real subprocesses --------------------------

// writeHookScript drops a bash script that reads stdin JSON and prints
// the requested reply JSON. Returns the script path.
func writeHookScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	script := "#!/bin/bash\nset -e\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPreAllowPassthrough(t *testing.T) {
	// Silent hook (prints nothing) → default allow, no change.
	script := writeHookScript(t, `# silent — read stdin and exit cleanly
cat > /dev/null
`)
	cfg := &Config{
		PreToolUse: []HookSpec{{Matcher: "bash", Command: script}},
	}
	r := NewRunner(cfg)
	orig := json.RawMessage(`{"command":"ls"}`)
	finalInput, allowed, denyCtx, err := r.RunPre(context.Background(), "bash", orig)
	if err != nil {
		t.Fatalf("RunPre: %v", err)
	}
	if !allowed || denyCtx != "" {
		t.Errorf("silent hook should allow, got allowed=%v denyCtx=%q", allowed, denyCtx)
	}
	if string(finalInput) != string(orig) {
		t.Errorf("input mutated: got %s, want %s", finalInput, orig)
	}
}

func TestRunPreDeny(t *testing.T) {
	script := writeHookScript(t, `cat > /dev/null
echo '{"decision":"deny","additionalContext":"no network access allowed here"}'
`)
	cfg := &Config{
		PreToolUse: []HookSpec{{Matcher: "bash", Command: script}},
	}
	r := NewRunner(cfg)
	_, allowed, denyCtx, err := r.RunPre(context.Background(), "bash", json.RawMessage(`{"command":"curl bad.com"}`))
	if err != nil {
		t.Fatalf("RunPre: %v", err)
	}
	if allowed {
		t.Error("expected deny")
	}
	if !strings.Contains(denyCtx, "no network access") {
		t.Errorf("denyCtx: %q", denyCtx)
	}
}

func TestRunPreModifiesInput(t *testing.T) {
	script := writeHookScript(t, `cat > /dev/null
echo '{"decision":"allow","modifiedInput":{"command":"ls -la","timeout_seconds":5}}'
`)
	cfg := &Config{
		PreToolUse: []HookSpec{{Matcher: "bash", Command: script}},
	}
	r := NewRunner(cfg)
	finalInput, allowed, _, err := r.RunPre(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if err != nil || !allowed {
		t.Fatalf("RunPre: allowed=%v err=%v", allowed, err)
	}
	if !strings.Contains(string(finalInput), `"ls -la"`) {
		t.Errorf("input not modified: %s", finalInput)
	}
	if !strings.Contains(string(finalInput), `"timeout_seconds":5`) {
		t.Errorf("timeout missing: %s", finalInput)
	}
}

func TestRunPreMatcherSkipsNonMatching(t *testing.T) {
	// Hook targets only bash — read_file should skip it entirely.
	script := writeHookScript(t, `echo '{"decision":"deny"}'`)
	cfg := &Config{
		PreToolUse: []HookSpec{{Matcher: "bash", Command: script}},
	}
	r := NewRunner(cfg)
	_, allowed, _, err := r.RunPre(context.Background(), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("RunPre: %v", err)
	}
	if !allowed {
		t.Error("non-matching hook should not deny")
	}
}

func TestRunPostAppendsContext(t *testing.T) {
	script := writeHookScript(t, `cat > /dev/null
echo '{"additionalContext":"[audited]"}'
`)
	cfg := &Config{
		PostToolUse: []HookSpec{{Matcher: "bash", Command: script}},
	}
	r := NewRunner(cfg)
	out, isErr, err := r.RunPost(context.Background(), "bash", nil, "hello world", false)
	if err != nil {
		t.Fatalf("RunPost: %v", err)
	}
	if !strings.Contains(out, "[audited]") {
		t.Errorf("context not appended: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("original output lost: %q", out)
	}
	if isErr {
		t.Error("isErr should remain false")
	}
}

func TestRunPostFlipError(t *testing.T) {
	script := writeHookScript(t, `cat > /dev/null
echo '{"flipError":true,"additionalContext":"suppressed"}'
`)
	cfg := &Config{
		PostToolUse: []HookSpec{{Matcher: "*", Command: script}},
	}
	r := NewRunner(cfg)
	_, isErr, err := r.RunPost(context.Background(), "bash", nil, "err msg", true)
	if err != nil {
		t.Fatalf("RunPost: %v", err)
	}
	if isErr {
		t.Error("isErr should have been flipped to false")
	}
}

func TestRunPostReplacesOutput(t *testing.T) {
	script := writeHookScript(t, `cat > /dev/null
echo '{"modifiedOutput":"REDACTED"}'
`)
	cfg := &Config{
		PostToolUse: []HookSpec{{Matcher: "*", Command: script}},
	}
	r := NewRunner(cfg)
	out, _, err := r.RunPost(context.Background(), "bash", nil, "original sensitive data", false)
	if err != nil {
		t.Fatalf("RunPost: %v", err)
	}
	if !strings.Contains(out, "REDACTED") || strings.Contains(out, "sensitive data") {
		t.Errorf("output not replaced correctly: %q", out)
	}
}

func TestRunPreFailingHookBubbles(t *testing.T) {
	// Command that exits non-zero → Runner surfaces an error.
	script := writeHookScript(t, `exit 7`)
	cfg := &Config{
		PreToolUse: []HookSpec{{Matcher: "*", Command: script}},
	}
	r := NewRunner(cfg)
	_, _, _, err := r.RunPre(context.Background(), "bash", nil)
	if err == nil {
		t.Error("expected error from failing hook")
	}
}

func TestRunnerNilSafe(t *testing.T) {
	var r *Runner
	in := json.RawMessage(`{"x":1}`)
	got, allowed, ctx, err := r.RunPre(context.Background(), "any", in)
	if err != nil || !allowed || ctx != "" || string(got) != string(in) {
		t.Errorf("nil runner should pass-through: %s allowed=%v ctx=%q err=%v", got, allowed, ctx, err)
	}
	out, isErr, err := r.RunPost(context.Background(), "any", in, "o", true)
	if err != nil || out != "o" || !isErr {
		t.Errorf("nil runner post: %q %v %v", out, isErr, err)
	}
}

func TestEnabled(t *testing.T) {
	if (*Runner)(nil).Enabled() {
		t.Error("nil runner should not be enabled")
	}
	r := NewRunner(&Config{})
	if r.Enabled() {
		t.Error("empty config should not be enabled")
	}
	r = NewRunner(&Config{PreToolUse: []HookSpec{{Command: "x"}}})
	if !r.Enabled() {
		t.Error("config with a pre hook should be enabled")
	}
}
