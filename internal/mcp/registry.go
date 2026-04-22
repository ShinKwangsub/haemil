package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// Config is the on-disk shape of ~/.haemil/mcp.json.
//
//	{
//	  "servers": {
//	    "filesystem": {
//	      "command": "npx",
//	      "args": ["@modelcontextprotocol/server-filesystem", "/tmp"],
//	      "env":  {"DEBUG": "1"}
//	    }
//	  }
//	}
//
// Future keys (transport, oauth, timeouts, etc.) would extend ServerConfig
// without breaking the file format.
type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// ServerConfig is one stdio-transport MCP server definition.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// LoadConfig reads the MCP config from path. Returns an empty Config + nil
// error if the file does not exist — missing config is normal, not a fault.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("mcp: read config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("mcp: parse config %q: %w", path, err)
	}
	return &cfg, nil
}

// DefaultConfigPath returns <home>/.haemil/mcp.json (or the override when
// set). Routed through runtime.TenantContext so multi-tenant callers
// (C9+) can override home.
func DefaultConfigPath() string {
	t, err := runtime.ResolveTenant("", "")
	if err != nil {
		return "mcp.json"
	}
	return t.MCPConfigPath()
}

// Registry owns the live MCP servers and exposes a tool list suitable for
// appending to runtime.Default()'s result.
type Registry struct {
	Servers []*Server
	Tools   []runtime.Tool
}

// BootstrapFromConfig connects every server in cfg, initializes each,
// fetches its tool list, and returns a populated Registry. Failures on
// individual servers are logged to stderr and skipped — one flaky server
// does not knock out the rest (claw-code's degraded-mode pattern).
//
// The returned Registry owns the Server instances; callers must Close it
// when shutting down.
func BootstrapFromConfig(ctx context.Context, cfg *Config) *Registry {
	reg := &Registry{}
	if cfg == nil || len(cfg.Servers) == 0 {
		return reg
	}
	// Deterministic iteration so tool ordering is stable across runs.
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sc := cfg.Servers[name]
		srv, err := NewServer(ctx, StdioConfig{
			Name:    name,
			Command: sc.Command,
			Args:    sc.Args,
			Env:     envSliceFromMap(sc.Env),
		})
		if err != nil {
			fmt.Fprintf(stderrOf(), "mcp: skip server %q (spawn): %v\n", name, err)
			continue
		}
		if err := srv.Initialize(ctx); err != nil {
			fmt.Fprintf(stderrOf(), "mcp: skip server %q (initialize): %v\n", name, err)
			_ = srv.Close()
			continue
		}
		defs, err := srv.ListTools(ctx)
		if err != nil {
			fmt.Fprintf(stderrOf(), "mcp: skip server %q (tools/list): %v\n", name, err)
			_ = srv.Close()
			continue
		}
		for _, d := range defs {
			reg.Tools = append(reg.Tools, NewTool(srv, d))
		}
		reg.Servers = append(reg.Servers, srv)
	}
	return reg
}

// Close shuts down every live server. Safe to call on a zero-value or
// partially-populated registry.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	for _, s := range r.Servers {
		_ = s.Close()
	}
	r.Servers = nil
	r.Tools = nil
}

// envSliceFromMap converts `{"KEY":"value"}` to `["KEY=value"]`, plus the
// parent process's env so the subprocess inherits PATH/HOME/etc.
func envSliceFromMap(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+len(m))
	out = append(out, base...)
	// Deterministic order so tests are stable.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
