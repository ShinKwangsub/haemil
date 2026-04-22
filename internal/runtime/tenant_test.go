package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveTenantDefaults: empty args → os.Getwd()/UserHomeDir() fallback.
// Verifies the "don't break existing CLI" contract of C9.
func TestResolveTenantDefaults(t *testing.T) {
	tenant, err := ResolveTenant("", "")
	if err != nil {
		t.Fatalf("ResolveTenant: %v", err)
	}
	cwd, _ := os.Getwd()
	if tenant.Workspace != cwd {
		t.Errorf("workspace: got %q, want %q", tenant.Workspace, cwd)
	}
	home, _ := os.UserHomeDir()
	if tenant.HomeDir != home {
		t.Errorf("home: got %q, want %q", tenant.HomeDir, home)
	}
}

// TestResolveTenantExplicit: explicit absolute paths → preserved verbatim.
func TestResolveTenantExplicit(t *testing.T) {
	ws := t.TempDir()
	hm := t.TempDir()
	tenant, err := ResolveTenant(ws, hm)
	if err != nil {
		t.Fatalf("ResolveTenant: %v", err)
	}
	if tenant.Workspace != ws {
		t.Errorf("workspace: got %q, want %q", tenant.Workspace, ws)
	}
	if tenant.HomeDir != hm {
		t.Errorf("home: got %q, want %q", tenant.HomeDir, hm)
	}
}

// TestResolveTenantRejectsRelative: relative paths are ambiguous once the
// engine changes working directory (tool exec, tests). Fail fast.
func TestResolveTenantRejectsRelative(t *testing.T) {
	cases := []struct {
		name, ws, hm string
		wantSubstr   string
	}{
		{"relative workspace", "some/relative", "/abs/home", "workspace must be absolute"},
		{"relative home", "/abs/ws", "rel/home", "home must be absolute"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveTenant(c.ws, c.hm)
			if err == nil {
				t.Fatal("expected error on relative path, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error: got %q, want substring %q", err.Error(), c.wantSubstr)
			}
		})
	}
}

// TestTenantPathsDerived: the 5 standard helpers compose the expected
// <root>/.haemil/<leaf> layout. If this test ever changes, every caller
// in memory/hooks/mcp must be audited.
func TestTenantPathsDerived(t *testing.T) {
	ws := t.TempDir()
	hm := t.TempDir()
	tenant, err := ResolveTenant(ws, hm)
	if err != nil {
		t.Fatalf("ResolveTenant: %v", err)
	}
	cases := []struct {
		label string
		got   string
		want  string
	}{
		{"SessionDir", tenant.SessionDir(), filepath.Join(hm, ".haemil", "sessions")},
		{"UserMemoryPath", tenant.UserMemoryPath(), filepath.Join(hm, ".haemil", "USER.md")},
		{"ProjectMemoryPath", tenant.ProjectMemoryPath(), filepath.Join(ws, ".haemil", "MEMORY.md")},
		{"HooksConfigPath", tenant.HooksConfigPath(), filepath.Join(ws, ".haemil", "hooks.json")},
		{"MCPConfigPath", tenant.MCPConfigPath(), filepath.Join(hm, ".haemil", "mcp.json")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.label, c.got, c.want)
		}
	}
}
