package tools

import (
	"encoding/json"
	"testing"
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
