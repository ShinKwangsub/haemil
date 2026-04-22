package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

// TenantContext is the small value object that collapses every "where does
// this file live?" question in the engine into two paths: a per-user
// HomeDir (for cross-project settings like USER.md, mcp.json, sessions)
// and a per-project Workspace (for project-local state like MEMORY.md and
// hooks.json). Every path-resolving helper in internal/memory,
// internal/hooks, and internal/mcp is expected to route through a
// TenantContext so that a single process can host more than one agent
// without their state colliding on disk.
//
// Phase 4 C9 introduces this as the foundation; actual concurrent-runtime
// orchestration is a later cycle (C10+). The default (zero-value)
// behaviour preserves pre-C9 semantics — ResolveTenant("", "") returns
// the same paths the old per-package Default*Path helpers produced.
type TenantContext struct {
	// ID is an optional logical identifier for the tenant. Empty == the
	// implicit "default" tenant (current single-user CLI behaviour). The
	// engine does not interpret ID beyond passing it through; downstream
	// layers (future: multi-tenant DB, audit log) will use it as a
	// partition key.
	ID string

	// Workspace is the absolute path to the project root. Project-local
	// state (.haemil/MEMORY.md, .haemil/hooks.json) lives under it.
	Workspace string

	// HomeDir is the absolute path to the per-user config root. User
	// memory (USER.md), MCP config (mcp.json), and session JSONLs live
	// under <HomeDir>/.haemil/.
	HomeDir string
}

// ResolveTenant builds a TenantContext. Empty arguments fall back to
// os.Getwd() for workspace and os.UserHomeDir() for home — this preserves
// the pre-C9 default so existing callers (cmd/haemil/main.go) behave
// identically. Non-empty arguments must be absolute paths; relative paths
// are rejected to avoid ambiguity when the engine changes working
// directory (tool execution, tests, future server mode).
func ResolveTenant(workspace, home string) (TenantContext, error) {
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return TenantContext{}, fmt.Errorf("runtime: resolve tenant workspace: %w", err)
		}
		workspace = cwd
	} else if !filepath.IsAbs(workspace) {
		return TenantContext{}, fmt.Errorf("runtime: tenant workspace must be absolute: %q", workspace)
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return TenantContext{}, fmt.Errorf("runtime: resolve tenant home: %w", err)
		}
		home = h
	} else if !filepath.IsAbs(home) {
		return TenantContext{}, fmt.Errorf("runtime: tenant home must be absolute: %q", home)
	}
	return TenantContext{Workspace: workspace, HomeDir: home}, nil
}

// SessionDir is <HomeDir>/.haemil/sessions — where JSONL session files
// live. Shared across projects for the same user.
func (t TenantContext) SessionDir() string {
	return filepath.Join(t.HomeDir, ".haemil", "sessions")
}

// UserMemoryPath is <HomeDir>/.haemil/USER.md.
func (t TenantContext) UserMemoryPath() string {
	return filepath.Join(t.HomeDir, ".haemil", "USER.md")
}

// ProjectMemoryPath is <Workspace>/.haemil/MEMORY.md.
func (t TenantContext) ProjectMemoryPath() string {
	return filepath.Join(t.Workspace, ".haemil", "MEMORY.md")
}

// HooksConfigPath is <Workspace>/.haemil/hooks.json. Project-local so
// different projects can have different hook chains without crosstalk.
func (t TenantContext) HooksConfigPath() string {
	return filepath.Join(t.Workspace, ".haemil", "hooks.json")
}

// MCPConfigPath is <HomeDir>/.haemil/mcp.json. Per-user because MCP
// server definitions (credentials, binaries) are typically identity-scoped
// rather than project-scoped.
func (t TenantContext) MCPConfigPath() string {
	return filepath.Join(t.HomeDir, ".haemil", "mcp.json")
}
