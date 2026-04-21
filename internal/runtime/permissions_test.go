package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// readTool implements CapabilityProvider → CapRead.
type readTool struct{ name string }

func (t *readTool) Spec() ToolSpec {
	return ToolSpec{Name: t.name, Description: "r", InputSchema: json.RawMessage(`{}`)}
}
func (t *readTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }
func (t *readTool) Capability() Capability                                       { return CapRead }

// writeTool implements CapabilityProvider → CapWrite.
type writeTool struct{ name string }

func (t *writeTool) Spec() ToolSpec {
	return ToolSpec{Name: t.name, Description: "w", InputSchema: json.RawMessage(`{}`)}
}
func (t *writeTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }
func (t *writeTool) Capability() Capability                                       { return CapWrite }

// execTool implements CapabilityProvider → CapExec.
type execTool struct{ name string }

func (t *execTool) Spec() ToolSpec {
	return ToolSpec{Name: t.name, Description: "x", InputSchema: json.RawMessage(`{}`)}
}
func (t *execTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }
func (t *execTool) Capability() Capability                                       { return CapExec }

// silentTool does NOT implement CapabilityProvider — exercises the fallback
// path (Policy.Fallback map, else CapExec default).
type silentTool struct{ name string }

func (t *silentTool) Spec() ToolSpec {
	return ToolSpec{Name: t.name, Description: "silent", InputSchema: json.RawMessage(`{}`)}
}
func (t *silentTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "", nil
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    PermissionMode
		wantErr bool
	}{
		{"readonly", ModeReadOnly, false},
		{"READ-ONLY", ModeReadOnly, false},
		{"read", ModeReadOnly, false},
		{"workspace-write", ModeWorkspaceWrite, false},
		{"workspace_write", ModeWorkspaceWrite, false},
		{"write", ModeWorkspaceWrite, false},
		{"danger-full", ModeDangerFullAccess, false},
		{"danger", ModeDangerFullAccess, false},
		{"full", ModeDangerFullAccess, false},
		{"  Dangerous  ", ModeDangerFullAccess, false},
		{"", 0, true},
		{"nonsense", 0, true},
	}
	for _, c := range cases {
		got, err := ParseMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q): expected error, got mode %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMode(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestModeAndCapabilityStrings(t *testing.T) {
	if ModeReadOnly.String() != "readonly" ||
		ModeWorkspaceWrite.String() != "workspace-write" ||
		ModeDangerFullAccess.String() != "danger-full" {
		t.Errorf("mode strings drifted")
	}
	if CapRead.String() != "read" || CapWrite.String() != "write" || CapExec.String() != "exec" {
		t.Errorf("capability strings drifted")
	}
	if DecisionAllow.String() != "allow" || DecisionDeny.String() != "deny" || DecisionAsk.String() != "ask" {
		t.Errorf("decision strings drifted")
	}
}

// TestAuthorizeMatrix walks the 3×3 (mode × capability) cross-product.
func TestAuthorizeMatrix(t *testing.T) {
	tools := map[Capability]Tool{
		CapRead:  &readTool{name: "r"},
		CapWrite: &writeTool{name: "w"},
		CapExec:  &execTool{name: "x"},
	}
	cases := []struct {
		mode    PermissionMode
		cap     Capability
		wantDec Decision
	}{
		{ModeReadOnly, CapRead, DecisionAllow},
		{ModeReadOnly, CapWrite, DecisionDeny},
		{ModeReadOnly, CapExec, DecisionDeny},
		{ModeWorkspaceWrite, CapRead, DecisionAllow},
		{ModeWorkspaceWrite, CapWrite, DecisionAllow},
		{ModeWorkspaceWrite, CapExec, DecisionDeny},
		{ModeDangerFullAccess, CapRead, DecisionAllow},
		{ModeDangerFullAccess, CapWrite, DecisionAllow},
		{ModeDangerFullAccess, CapExec, DecisionAllow},
	}
	for _, c := range cases {
		p := NewPolicy(c.mode, nil)
		dec, reason := p.Authorize(tools[c.cap], nil)
		if dec != c.wantDec {
			t.Errorf("mode=%v cap=%v: got %v (%q), want %v", c.mode, c.cap, dec, reason, c.wantDec)
		}
		if dec == DecisionDeny && !strings.Contains(reason, "permission_denied") {
			t.Errorf("deny reason should mention permission_denied, got %q", reason)
		}
	}
}

// TestAuthorizeNilPolicy: nil Policy == allow-everything (backwards compat).
func TestAuthorizeNilPolicy(t *testing.T) {
	var p *Policy
	dec, _ := p.Authorize(&execTool{name: "x"}, nil)
	if dec != DecisionAllow {
		t.Errorf("nil policy should allow, got %v", dec)
	}
}

// TestCapabilityFallback: silentTool has no CapabilityProvider; policy uses
// its Fallback map, or defaults to CapExec.
func TestCapabilityFallback(t *testing.T) {
	silent := &silentTool{name: "mcp.query"}

	// No fallback → default CapExec.
	p := NewPolicy(ModeReadOnly, nil)
	if got := p.CapabilityOf(silent); got != CapExec {
		t.Errorf("silent tool without fallback: got %v, want CapExec", got)
	}
	if dec, _ := p.Authorize(silent, nil); dec != DecisionDeny {
		t.Errorf("silent tool in readonly: want Deny, got %v", dec)
	}

	// Fallback explicitly set to CapRead → allowed in ReadOnly.
	p2 := NewPolicy(ModeReadOnly, map[string]Capability{"mcp.query": CapRead})
	if got := p2.CapabilityOf(silent); got != CapRead {
		t.Errorf("silent tool with fallback: got %v, want CapRead", got)
	}
	if dec, _ := p2.Authorize(silent, nil); dec != DecisionAllow {
		t.Errorf("silent tool in readonly with fallback: want Allow, got %v", dec)
	}
}

// TestCapabilityOfNilTool: defensive — nil tool → CapExec (fail-closed).
func TestCapabilityOfNilTool(t *testing.T) {
	p := NewPolicy(ModeReadOnly, nil)
	if got := p.CapabilityOf(nil); got != CapExec {
		t.Errorf("nil tool: got %v, want CapExec", got)
	}
}
