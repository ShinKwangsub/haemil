package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestBashSpecSchema validates the bash tool's advertised JSON Schema. The
// schema is a pinned string literal in bash.go, and the conversation loop
// forwards it verbatim to the provider, so any drift here would break the
// tool-use round-trip silently.
//
// Checks:
//   - InputSchema parses as JSON
//   - top-level type is "object"
//   - properties.command exists with type "string"
//   - required includes "command"
func TestBashSpecSchema(t *testing.T) {
	spec := NewBash().Spec()

	if spec.Name != "bash" {
		t.Errorf("name: got %q, want %q", spec.Name, "bash")
	}
	if spec.Description == "" {
		t.Error("description is empty")
	}

	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatalf("input_schema unmarshal: %v", err)
	}

	if schema.Type != "object" {
		t.Errorf("schema.type: got %q, want %q", schema.Type, "object")
	}

	cmdProp, ok := schema.Properties["command"]
	if !ok {
		t.Fatal("schema.properties.command missing")
	}
	if cmdProp.Type != "string" {
		t.Errorf("properties.command.type: got %q, want %q", cmdProp.Type, "string")
	}

	hasCommand := false
	for _, r := range schema.Required {
		if r == "command" {
			hasCommand = true
			break
		}
	}
	if !hasCommand {
		t.Error("schema.required does not include \"command\"")
	}
}

// TestBlockedPatternsCompile verifies every entry in BLOCKED_PATTERNS is a
// valid, non-nil compiled regex. Because regexp.MustCompile would have
// panicked at package init time if any pattern were malformed, this test
// mainly guards against someone accidentally appending a nil.
func TestBlockedPatternsCompile(t *testing.T) {
	if len(BLOCKED_PATTERNS) == 0 {
		t.Fatal("BLOCKED_PATTERNS is empty — expected at least the rm -rf / pattern")
	}
	for i, p := range BLOCKED_PATTERNS {
		if p == nil {
			t.Errorf("BLOCKED_PATTERNS[%d] is nil", i)
		}
	}

	// Spot-check a few obviously bad commands hit a pattern.
	bad := []string{
		"rm -rf /",
		"rm -rf /home/user",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		":(){ :|:& };:",
		"echo foo > /dev/sda",
	}
	for _, cmd := range bad {
		hit := false
		for _, p := range BLOCKED_PATTERNS {
			if p.MatchString(cmd) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("command %q was not matched by any BLOCKED_PATTERN", cmd)
		}
	}

	// And a few benign commands should NOT be flagged.
	ok := []string{
		"ls -la",
		"grep foo bar.txt",
		"rm oldfile.txt",   // note: not rm -rf /
		"cat /etc/hosts",
	}
	for _, cmd := range ok {
		for _, p := range BLOCKED_PATTERNS {
			if p.MatchString(cmd) {
				t.Errorf("command %q incorrectly matched BLOCKED_PATTERN %q", cmd, p)
			}
		}
	}
}

// TestBashExecuteEcho verifies a simple successful command returns its stdout.
func TestBashExecuteEcho(t *testing.T) {
	b := NewBash()
	out, err := b.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output: got %q, want contains 'hello'", out)
	}
}

// TestBashExecuteCombinesStderr verifies stderr is merged into the captured
// output (not dropped silently).
func TestBashExecuteCombinesStderr(t *testing.T) {
	b := NewBash()
	out, _ := b.Execute(context.Background(), json.RawMessage(`{"command":"echo stdout; echo stderr >&2"}`))
	if !strings.Contains(out, "stdout") || !strings.Contains(out, "stderr") {
		t.Errorf("combined output: got %q, want both stdout and stderr", out)
	}
}

// TestBashExecuteNonZeroExit verifies a non-zero exit returns an error with
// the output preserved (so the conversation loop can show both).
func TestBashExecuteNonZeroExit(t *testing.T) {
	b := NewBash()
	out, err := b.Execute(context.Background(), json.RawMessage(`{"command":"echo fail; exit 7"}`))
	if err == nil {
		t.Fatal("expected error for exit 7, got nil")
	}
	if !strings.Contains(out, "fail") {
		t.Errorf("output on error: got %q, want 'fail'", out)
	}
}

// TestBashExecuteBlocked verifies BLOCKED_PATTERNS reject before spawning.
func TestBashExecuteBlocked(t *testing.T) {
	b := NewBash()
	out, err := b.Execute(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`))
	if err == nil {
		t.Fatal("expected block for rm -rf /, got nil error")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error: got %q, want 'blocked' mentioned", err.Error())
	}
	if out != "" {
		t.Errorf("expected empty output (command never ran), got %q", out)
	}
}

// TestBashExecuteTimeout verifies timeout_seconds kills a hung command.
func TestBashExecuteTimeout(t *testing.T) {
	b := NewBash()
	t0 := time.Now()
	_, err := b.Execute(context.Background(), json.RawMessage(`{"command":"sleep 10","timeout_seconds":1}`))
	elapsed := time.Since(t0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error: got %q, want 'timed out' mentioned", err.Error())
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed %v — timeout did not fire promptly", elapsed)
	}
}

// TestBashExecuteCtxCancel verifies external ctx cancel stops the command.
func TestBashExecuteCtxCancel(t *testing.T) {
	b := NewBash()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.Execute(ctx, json.RawMessage(`{"command":"sleep 10"}`))
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after ctx cancel, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return after ctx cancel")
	}
}

// TestBashExecuteEmptyInput verifies invalid inputs are rejected cleanly.
func TestBashExecuteEmptyInput(t *testing.T) {
	b := NewBash()
	cases := []string{
		`{}`,
		`{"command":""}`,
	}
	for _, c := range cases {
		_, err := b.Execute(context.Background(), json.RawMessage(c))
		if err == nil {
			t.Errorf("expected error for input %q, got nil", c)
		}
	}
}
