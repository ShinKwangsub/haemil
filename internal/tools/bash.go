package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"syscall"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// bashSpecSchema is the JSON Schema advertised to the provider.
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

const bashSpecDescription = "Run a bash command on the local machine. Output is captured (stdout+stderr combined) and returned as text. Commands are screened against a multi-stage validation pipeline (mode check → sed guard → destructive-pattern warn → path traversal warn). Blocked commands return an error; warnings run but prefix the output with a cautionary note."

// defaultTimeoutSec is used when the caller does not specify timeout_seconds.
const defaultTimeoutSec = 30

// maxOutputBytes caps captured output at 10 MiB (skeleton.md §8 / Phase 2b plan).
const maxOutputBytes = 10 * 1024 * 1024

// BLOCKED_PATTERNS matches commands that are catastrophically destructive
// regardless of mode. These are a LAST line of defense behind C3's
// ValidateCommand pipeline (see bash_validation.go). They should only fire
// for commands that even DangerFullAccess must refuse.
//
// Kept narrow to avoid false positives: `rm -rf /tmp/foo` is NOT blocked
// here (C3 checkDestructive warns on it); only `rm -rf /` (literal root)
// is a hard block.
var BLOCKED_PATTERNS = []*regexp.Regexp{
	// rm -rf (any flag form) targeting literal root `/` only.
	regexp.MustCompile(`\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r|-r\s+-f|-f\s+-r)\s+/\s*$`),
	// mkfs.* — wipes a filesystem.
	regexp.MustCompile(`\bmkfs\.[a-zA-Z0-9]+\b`),
	// dd writing to a raw block device.
	regexp.MustCompile(`\bdd\s+.*\bof=/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
	// Fork bomb.
	regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
	// Redirecting to a raw block device.
	regexp.MustCompile(`>\s*/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
}

// BashTool implements runtime.Tool for local bash execution.
//
// The tool stores the effective PermissionMode and workspace root so that
// its validation pipeline (see bash_validation.go) can be parameterized
// without re-plumbing them through every call. Both are captured at
// construction time — callers rebuild the tool if the mode or workspace
// changes.
type BashTool struct {
	spec      runtime.ToolSpec
	mode      runtime.PermissionMode
	workspace string
}

// NewBash builds a BashTool. mode is the active PermissionMode (runtime.Mode*);
// workspace is the resolved absolute path of the workspace root (used by the
// path-traversal heuristic — pass "" if unknown). Real, cheap, safe to call
// during wiring.
func NewBash(mode runtime.PermissionMode, workspace string) *BashTool {
	return &BashTool{
		spec: runtime.ToolSpec{
			Name:        "bash",
			Description: bashSpecDescription,
			InputSchema: json.RawMessage(bashSpecSchema),
		},
		mode:      mode,
		workspace: workspace,
	}
}

// Spec returns the cached ToolSpec.
func (b *BashTool) Spec() runtime.ToolSpec { return b.spec }

// Capability classifies bash as arbitrary command execution.
func (b *BashTool) Capability() runtime.Capability { return runtime.CapExec }

// bashInput is the shape of {command, timeout_seconds} from the model.
type bashInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// Execute runs the bash command described in input.
//
// Behaviour:
//   - Parses input JSON → {Command, TimeoutSeconds}.
//   - Rejects commands matching any BLOCKED_PATTERNS (returns error).
//   - Runs via `bash -c <command>`.
//   - Sets a process group so ctx cancellation / timeout kills children too.
//   - Captures stdout+stderr combined, capped at 10 MiB.
//   - Honors both ctx cancellation AND TimeoutSeconds (whichever first).
//   - Non-zero exit codes are returned as errors with output attached.
func (b *BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", errors.New("bash: empty input")
	}
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("bash: parse input: %w", err)
	}
	if in.Command == "" {
		return "", errors.New("bash: command is required")
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = defaultTimeoutSec
	}

	// Safety check before spawning anything.
	for _, re := range BLOCKED_PATTERNS {
		if re.MatchString(in.Command) {
			return "", fmt.Errorf("bash: command blocked by safety pattern %q", re.String())
		}
	}

	// C3: full validation pipeline (mode / sed / destructive / paths).
	// Block → refuse. Warn → run, but prefix the output with the warning.
	verdict := ValidateCommand(in.Command, b.mode, b.workspace)
	var warnPrefix string
	switch verdict.Kind {
	case ValidationBlock:
		return "", fmt.Errorf("bash: validation blocked: %s", verdict.Reason)
	case ValidationWarn:
		warnPrefix = "[warning] " + verdict.Message + "\n"
	}

	// Combine ctx timeout with per-command timeout.
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", in.Command)
	// New process group so SIGKILL on timeout/cancel reaches child processes
	// (e.g. `bash -c "sleep 1000"` spawns sleep as child — killing only the
	// bash pid leaves sleep orphaned otherwise).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Kill the whole process group on timeout/cancel.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil && pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return nil
		}
		return cmd.Process.Kill()
	}

	var buf bytes.Buffer
	cmd.Stdout = &cappedWriter{buf: &buf, cap: maxOutputBytes}
	cmd.Stderr = cmd.Stdout

	err := cmd.Run()
	out := warnPrefix + buf.String()

	// Distinguish timeout/cancel from other exit errors.
	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("bash: timed out after %ds", in.TimeoutSeconds)
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil {
		// Non-zero exit: return the captured output AND the error so the
		// conversation loop can record both.
		return out, fmt.Errorf("bash: %w", err)
	}
	return out, nil
}

// cappedWriter writes to buf until cap bytes, then silently drops the rest.
type cappedWriter struct {
	buf *bytes.Buffer
	cap int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len() >= w.cap {
		// Already at cap: drop silently but report bytes as "written" so
		// the command doesn't get a short-write error.
		return len(p), nil
	}
	remaining := w.cap - w.buf.Len()
	if remaining >= len(p) {
		return w.buf.Write(p)
	}
	_, _ = w.buf.Write(p[:remaining])
	// Append a truncation marker once.
	if !bytes.HasSuffix(w.buf.Bytes(), []byte("\n[output truncated: reached 10 MiB cap]\n")) {
		w.buf.WriteString("\n[output truncated: reached 10 MiB cap]\n")
	}
	return len(p), nil
}
