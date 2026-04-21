package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Capability classifies what a tool does to the host. Tools advertise their
// capability via the optional CapabilityProvider interface; unknown tools
// default to the strictest level (CapExec) so the policy fails closed.
type Capability int

const (
	// CapRead: reads files, process state, or env. No side-effects.
	CapRead Capability = iota
	// CapWrite: mutates files inside the workspace. Does not spawn processes.
	CapWrite
	// CapExec: runs arbitrary external commands (bash, shell, network, ...).
	CapExec
)

// String returns the lowercase canonical name.
func (c Capability) String() string {
	switch c {
	case CapRead:
		return "read"
	case CapWrite:
		return "write"
	case CapExec:
		return "exec"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// CapabilityProvider is the optional interface a Tool may implement to
// declare its capability. If a Tool does not implement it, the policy
// assumes CapExec (fail-closed) unless the Policy's fallback map overrides.
type CapabilityProvider interface {
	Capability() Capability
}

// PermissionMode picks a preset ceiling on what capabilities are allowed.
// Modelled after claw-code's 3-level PermissionMode (see
// analysis/platforms/claw-code.md §3.1).
type PermissionMode int

const (
	// ModeReadOnly: only CapRead tools may run. CapWrite and CapExec denied.
	ModeReadOnly PermissionMode = iota
	// ModeWorkspaceWrite: CapRead + CapWrite allowed. CapExec denied.
	ModeWorkspaceWrite
	// ModeDangerFullAccess: everything allowed. Matches pre-C2 behavior.
	ModeDangerFullAccess
)

// String returns the canonical flag-value name: "readonly", "workspace-write",
// "danger-full". ParseMode accepts these plus a few aliases.
func (m PermissionMode) String() string {
	switch m {
	case ModeReadOnly:
		return "readonly"
	case ModeWorkspaceWrite:
		return "workspace-write"
	case ModeDangerFullAccess:
		return "danger-full"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// ParseMode accepts canonical names and a handful of common aliases.
// Returns an error on empty input or unrecognised values.
func ParseMode(s string) (PermissionMode, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	norm = strings.ReplaceAll(norm, "_", "-")
	switch norm {
	case "readonly", "read-only", "read":
		return ModeReadOnly, nil
	case "workspace-write", "write", "workspace":
		return ModeWorkspaceWrite, nil
	case "danger-full", "danger", "full", "danger-full-access", "dangerous":
		return ModeDangerFullAccess, nil
	case "":
		return 0, fmt.Errorf("permission mode is empty")
	default:
		return 0, fmt.Errorf("unknown permission mode %q (expected readonly | workspace-write | danger-full)", s)
	}
}

// Decision is the result of Authorize. Ask is reserved for a future
// interactive prompt integration; in Phase 2 the enforcer treats Ask the
// same as Deny so the loop never blocks waiting for input.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionAsk
)

// String returns the lowercase canonical name.
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionAsk:
		return "ask"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}

// Policy enforces a PermissionMode against individual tool invocations.
// Callers construct one via NewPolicy and attach it to runtime.Options.
//
// The only authoritative signal today is the tool's Capability; per-tool
// rules (name match, arg regex) are Phase 3 territory. The Fallback map
// lets callers declare capability for tools that do NOT implement
// CapabilityProvider — useful for third-party / MCP tools wired in later.
type Policy struct {
	Mode PermissionMode

	// Fallback maps a tool name to a Capability to use when the tool does
	// not implement CapabilityProvider. If the tool is absent from this
	// map AND does not implement CapabilityProvider, the policy assumes
	// CapExec (fail-closed).
	Fallback map[string]Capability
}

// NewPolicy builds a Policy in the given mode. fallback may be nil.
func NewPolicy(mode PermissionMode, fallback map[string]Capability) *Policy {
	return &Policy{Mode: mode, Fallback: fallback}
}

// CapabilityOf reports the effective Capability for a Tool. It consults, in
// order: CapabilityProvider → Fallback map (by tool name) → CapExec.
func (p *Policy) CapabilityOf(t Tool) Capability {
	if t == nil {
		return CapExec
	}
	if cp, ok := t.(CapabilityProvider); ok {
		return cp.Capability()
	}
	if p != nil && p.Fallback != nil {
		if c, ok := p.Fallback[t.Spec().Name]; ok {
			return c
		}
	}
	return CapExec
}

// Authorize returns a Decision for a tool invocation. The input argument is
// currently unused (no per-arg rules yet) but is part of the signature so
// future rule engines can inspect arguments without a breaking change.
//
// Authorize is safe to call with a nil Policy — it returns DecisionAllow,
// which matches the pre-C2 behavior of executing every tool unconditionally.
func (p *Policy) Authorize(t Tool, _ json.RawMessage) (Decision, string) {
	if p == nil {
		return DecisionAllow, ""
	}
	cap := p.CapabilityOf(t)
	var name string
	if t != nil {
		name = t.Spec().Name
	}
	switch p.Mode {
	case ModeReadOnly:
		if cap == CapRead {
			return DecisionAllow, ""
		}
		return DecisionDeny, fmt.Sprintf("permission_denied: tool %q requires %s but mode is %s", name, cap, p.Mode)
	case ModeWorkspaceWrite:
		if cap == CapRead || cap == CapWrite {
			return DecisionAllow, ""
		}
		return DecisionDeny, fmt.Sprintf("permission_denied: tool %q requires %s but mode is %s", name, cap, p.Mode)
	case ModeDangerFullAccess:
		return DecisionAllow, ""
	default:
		return DecisionDeny, fmt.Sprintf("permission_denied: unknown mode %s", p.Mode)
	}
}
