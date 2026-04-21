package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// Tool adapts an MCP-discovered tool to the runtime.Tool interface.
//
// Naming: to avoid collisions with native tools and across servers, the
// advertised tool name is `mcp__<server>__<tool>` (double-underscore
// delimiter, matching claw-code's mcp::mcp_tool_name convention).
//
// Capability: unknown by default. MCP servers don't advertise a capability
// class, so we default to CapExec (strictest). Callers that know a server
// is read-only can configure Policy.Fallback to override per-tool.
type Tool struct {
	server *Server
	raw    ToolDef
	spec   runtime.ToolSpec
}

// NewTool wraps a discovered MCP ToolDef. The server pointer is retained
// for subsequent CallTool invocations.
func NewTool(server *Server, raw ToolDef) *Tool {
	schema := raw.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	prefixed := formatToolName(server.Name, raw.Name)
	return &Tool{
		server: server,
		raw:    raw,
		spec: runtime.ToolSpec{
			Name:        prefixed,
			Description: describeToolForProvider(server, raw),
			InputSchema: schema,
		},
	}
}

// Spec returns the cached ToolSpec.
func (t *Tool) Spec() runtime.ToolSpec { return t.spec }

// Execute proxies to the underlying MCP server's tools/call.
func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	return t.server.CallTool(ctx, t.raw.Name, input)
}

// Capability classifies MCP tools as CapExec by default. The registry may
// override this via runtime.Policy.Fallback for known read-only servers.
func (t *Tool) Capability() runtime.Capability { return runtime.CapExec }

// formatToolName builds the "mcp__<server>__<tool>" namespaced identifier.
// Sanitises the server name so it's safe for provider tool-name regexes
// (Anthropic requires ^[a-zA-Z0-9_-]{1,64}$).
func formatToolName(server, tool string) string {
	return "mcp__" + sanitizeName(server) + "__" + sanitizeName(tool)
}

// sanitizeName lowercases and replaces any char that isn't alnum/underscore/
// hyphen with an underscore.
func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// describeToolForProvider annotates the description with provenance so
// the model knows where this tool came from.
func describeToolForProvider(s *Server, raw ToolDef) string {
	desc := raw.Description
	if desc == "" {
		desc = "(no description provided)"
	}
	return "[mcp:" + s.Name + "] " + desc
}
