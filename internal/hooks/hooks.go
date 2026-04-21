// Package hooks implements subprocess-based Pre/PostToolUse hooks.
//
// Design (minimal port of claw-code's hooks.rs):
//   - A hook is a subprocess spec (command + args). When the conversation
//     loop is about to execute a tool whose name matches the hook's
//     matcher, the hook is spawned.
//   - The event payload is written as JSON to the subprocess's stdin.
//   - The subprocess replies with JSON on stdout.
//   - PreToolUse reply may: allow (optionally with modifiedInput), deny
//     (with a reason that becomes the tool_result content).
//   - PostToolUse reply may add additionalContext appended to the output
//     or flip the error flag.
//
// Non-goals for C6 MVP: regex matchers (substring only), multiple
// concurrent hooks per event (list is allowed, first non-allow wins),
// permission-overriding hooks (deferred — would require more design).
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Event names match the claw-code naming so config files are portable.
const (
	EventPreToolUse  = "PreToolUse"
	EventPostToolUse = "PostToolUse"
)

// defaultHookTimeout caps any single hook invocation. Keeps a misbehaving
// hook from deadlocking the whole conversation loop.
const defaultHookTimeout = 10 * time.Second

// Config is the on-disk shape of ~/.haemil/hooks.json (or $cwd equivalent).
//
//	{
//	  "preToolUse":  [{"matcher": "bash", "command": "/path/to/gate.sh"}],
//	  "postToolUse": [{"matcher": ".*",   "command": "logger", "args": ["-t", "haemil"]}]
//	}
type Config struct {
	PreToolUse  []HookSpec `json:"preToolUse,omitempty"`
	PostToolUse []HookSpec `json:"postToolUse,omitempty"`
}

// HookSpec is one hook entry.
//
// Matcher is a case-insensitive substring test against the tool name
// (empty matcher or "*" matches everything). Regex matchers are a
// future extension.
type HookSpec struct {
	Matcher     string            `json:"matcher,omitempty"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	TimeoutMs   int               `json:"timeoutMs,omitempty"`
	Description string            `json:"description,omitempty"`
}

// Matches reports whether the spec applies to the given tool name.
func (h HookSpec) Matches(toolName string) bool {
	m := strings.TrimSpace(h.Matcher)
	if m == "" || m == "*" {
		return true
	}
	return strings.Contains(
		strings.ToLower(toolName),
		strings.ToLower(m),
	)
}

// Event is the JSON payload sent to the hook's stdin.
type Event struct {
	Event    string          `json:"event"`
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   string          `json:"output,omitempty"`
	IsError  bool            `json:"isError,omitempty"`
}

// Reply is the JSON shape expected from the hook's stdout.
//
// All fields are optional:
//   - Decision "" is treated as "allow" (zero-value == pass-through).
//   - ModifiedInput (Pre only) replaces the tool's input if non-empty.
//   - AdditionalContext appends to the tool_result (Post) or stands in
//     as the result when a Pre hook denies (a denial with no context
//     becomes "hook <name> denied").
//   - ModifiedOutput (Post only) replaces the captured output verbatim.
//   - FlipError (Post only) toggles the isError flag — useful for hooks
//     that classify non-zero exits as warnings or vice versa.
type Reply struct {
	Decision          string          `json:"decision,omitempty"`
	ModifiedInput     json.RawMessage `json:"modifiedInput,omitempty"`
	AdditionalContext string          `json:"additionalContext,omitempty"`
	ModifiedOutput    string          `json:"modifiedOutput,omitempty"`
	FlipError         bool            `json:"flipError,omitempty"`
}

// Decision tags for Reply.Decision.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

// LoadConfig reads hooks.json. Missing file is not an error (returns
// empty Config). Invalid JSON is an error.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("hooks: read %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("hooks: parse %q: %w", path, err)
	}
	return &cfg, nil
}

// DefaultConfigPath returns <cwd>/.haemil/hooks.json. Project-local by
// default so different projects can have different hooks without crosstalk.
func DefaultConfigPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "hooks.json"
	}
	return filepath.Join(cwd, ".haemil", "hooks.json")
}

// Runner executes the configured hooks for each event. Zero-value Runner
// is safe — Run* methods are no-ops.
type Runner struct {
	cfg *Config
}

// NewRunner builds a Runner from a Config. Accepts nil.
func NewRunner(cfg *Config) *Runner { return &Runner{cfg: cfg} }

// Enabled reports whether any hooks are configured at all.
func (r *Runner) Enabled() bool {
	if r == nil || r.cfg == nil {
		return false
	}
	return len(r.cfg.PreToolUse)+len(r.cfg.PostToolUse) > 0
}

// RunPre invokes every matching PreToolUse hook in order. The first hook
// that denies short-circuits; otherwise each hook's ModifiedInput stacks
// onto the input for the next hook.
//
// Returns (finalInput, allowed, denyContext, err).
//
//	allowed=true,  denyContext=""    → caller proceeds with finalInput
//	allowed=false, denyContext=...    → caller SKIPS execute, uses context as tool_result
func (r *Runner) RunPre(ctx context.Context, toolName string, input json.RawMessage) (json.RawMessage, bool, string, error) {
	if r == nil || r.cfg == nil {
		return input, true, "", nil
	}
	current := input
	for _, h := range r.cfg.PreToolUse {
		if !h.Matches(toolName) {
			continue
		}
		reply, err := runHook(ctx, h, Event{
			Event:    EventPreToolUse,
			ToolName: toolName,
			Input:    current,
		})
		if err != nil {
			return current, true, "", err
		}
		switch reply.Decision {
		case DecisionDeny:
			reason := reply.AdditionalContext
			if reason == "" {
				reason = fmt.Sprintf("hook %q denied %s", h.Command, toolName)
			}
			return current, false, reason, nil
		default:
			if len(reply.ModifiedInput) > 0 {
				current = reply.ModifiedInput
			}
		}
	}
	return current, true, "", nil
}

// RunPost invokes every matching PostToolUse hook. Hooks can append to
// output (AdditionalContext) or replace it (ModifiedOutput), and can
// flip the error flag.
//
// Returns (finalOutput, finalIsError, err).
func (r *Runner) RunPost(ctx context.Context, toolName string, input json.RawMessage, output string, isError bool) (string, bool, error) {
	if r == nil || r.cfg == nil {
		return output, isError, nil
	}
	currentOut := output
	currentErr := isError
	for _, h := range r.cfg.PostToolUse {
		if !h.Matches(toolName) {
			continue
		}
		reply, err := runHook(ctx, h, Event{
			Event:    EventPostToolUse,
			ToolName: toolName,
			Input:    input,
			Output:   currentOut,
			IsError:  currentErr,
		})
		if err != nil {
			return currentOut, currentErr, err
		}
		if reply.ModifiedOutput != "" {
			currentOut = reply.ModifiedOutput
		}
		if reply.AdditionalContext != "" {
			currentOut = currentOut + "\n" + reply.AdditionalContext
		}
		if reply.FlipError {
			currentErr = !currentErr
		}
	}
	return currentOut, currentErr, nil
}

// runHook spawns one hook subprocess, feeds it the event JSON, and parses
// the response. Unset Reply fields decode to zero values — a silent
// subprocess (no stdout output) is treated as "allow, no change".
func runHook(ctx context.Context, h HookSpec, evt Event) (Reply, error) {
	timeout := defaultHookTimeout
	if h.TimeoutMs > 0 {
		timeout = time.Duration(h.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, h.Command, h.Args...)
	if len(h.Env) > 0 {
		cmd.Env = append(os.Environ(), envSliceFromMap(h.Env)...)
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		return Reply{}, fmt.Errorf("hooks: marshal event: %w", err)
	}
	cmd.Stdin = bytes.NewReader(payload)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return Reply{}, fmt.Errorf("hooks: %q timed out after %s", h.Command, timeout)
		}
		return Reply{}, fmt.Errorf("hooks: %q exited: %w (stderr=%q)", h.Command, err, stderr.String())
	}

	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) == 0 {
		// Silent hook → default allow / pass-through.
		return Reply{}, nil
	}
	var reply Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return Reply{}, fmt.Errorf("hooks: %q produced invalid JSON: %w (body=%q)", h.Command, err, string(raw))
	}
	return reply, nil
}

func envSliceFromMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
