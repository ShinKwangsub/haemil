package tools

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// bashSpecSchema is the JSON Schema advertised to the provider. Pinned as a
// string literal (not built from a struct) so TestBashSpecSchema can validate
// the exact wire format without going through a marshalling roundtrip.
const bashSpecSchema = `{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "The bash command to execute."
    },
    "timeout_seconds": {
      "type": "integer",
      "description": "Max execution time in seconds. Default 30.",
      "default": 30
    }
  },
  "required": ["command"]
}`

// bashSpecDescription is the short prose shown to the model.
const bashSpecDescription = "Run a bash command on the local machine. Output is captured (stdout+stderr combined) and returned as text. This is a minimal Phase 2 tool with no permission layer — do not use it for anything that matters. Only obviously catastrophic commands (rm -rf /, fork bombs, etc.) are blocked. Full security policy arrives in Phase 3."

// BLOCKED_PATTERNS matches commands that are almost certainly destructive
// enough that we never want to run them, even in a development skeleton.
// These are pre-compiled at package init time so runtime checks are cheap.
//
// THIS IS NOT A SECURITY BOUNDARY. Bypassing these patterns is trivial
// (sudo, base64-decoded payloads, curl | sh, etc.). The sole purpose is to
// prevent a distracted developer (or the model) from nuking the dev machine
// by accident during Phase 2 smoke tests.
//
// The real permission model arrives in Phase 3 when we lift GoClaw's
// 5-layer security architecture (see analysis/platforms/goclaw.md §2.4).
var BLOCKED_PATTERNS = []*regexp.Regexp{
	// rm -rf / (or /<anything>), including spaces and extra flags
	regexp.MustCompile(`\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r|-r\s+-f|-f\s+-r)\s+/`),
	// mkfs.* — format a filesystem
	regexp.MustCompile(`\bmkfs\.[a-zA-Z0-9]+\b`),
	// dd writing to a raw block device
	regexp.MustCompile(`\bdd\s+.*\bof=/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
	// Shell fork bomb :(){ :|:& };:
	regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
	// Overwriting a raw disk device via redirection
	regexp.MustCompile(`>\s*/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
}

// BashTool implements runtime.Tool for local bash execution.
//
// Phase 2 stub: NewBash(), Spec() are real so the conversation loop can
// advertise the tool and the test suite can validate the schema. Execute()
// panics until Phase 2b fills in the actual subprocess logic.
type BashTool struct {
	spec runtime.ToolSpec
}

// NewBash builds a BashTool. Real, cheap, safe to call during wiring.
func NewBash() *BashTool {
	return &BashTool{
		spec: runtime.ToolSpec{
			Name:        "bash",
			Description: bashSpecDescription,
			InputSchema: json.RawMessage(bashSpecSchema),
		},
	}
}

// Spec returns the cached ToolSpec. Real.
func (b *BashTool) Spec() runtime.ToolSpec { return b.spec }

// Execute runs the command. Stub until Phase 2b.
//
// Phase 2b plan:
//  1. Unmarshal input → {Command string, TimeoutSeconds int}
//  2. Run every BLOCKED_PATTERNS against Command; reject with is_error=true
//  3. exec.CommandContext(ctx, "bash", "-c", cmd) with timeout
//  4. Capture stdout+stderr combined, cap output at 10 MB
//  5. On ctx.Done() kill the process group (not just the pid)
func (b *BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	panic("TODO: bash.Execute not implemented (Phase 2b)")
}
