package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Timeouts for the handshake stages. Production values; tests override via
// Server.Timeouts for speed.
const (
	defaultInitTimeout      = 10 * time.Second
	defaultListToolsTimeout = 30 * time.Second
)

// Timeouts controls per-operation deadlines for a Server.
type Timeouts struct {
	Initialize time.Duration
	ListTools  time.Duration
	CallTool   time.Duration
}

// Server is a live, initialized connection to a single MCP server.
//
// Lifecycle: NewServer → Initialize → ListTools → (CallTool)* → Close.
// Not safe to use before Initialize. Safe for concurrent CallTool/ListTools
// once initialized.
type Server struct {
	Name     string
	client   *stdioClient
	info     ServerInfo
	caps     map[string]interface{}
	Timeouts Timeouts
}

// StdioConfig is the subset of server configuration that we need to spawn
// and initialize a stdio-transport MCP server.
type StdioConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string // formatted "KEY=value" entries, like exec.Cmd.Env
}

// NewServer spawns the subprocess and returns an uninitialized Server.
// Call Initialize() next.
func NewServer(ctx context.Context, cfg StdioConfig) (*Server, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: server %q: command is empty", cfg.Name)
	}
	client, err := newStdioClient(ctx, cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return nil, err
	}
	return &Server{
		Name:   cfg.Name,
		client: client,
		Timeouts: Timeouts{
			Initialize: defaultInitTimeout,
			ListTools:  defaultListToolsTimeout,
			CallTool:   defaultCallTimeout,
		},
	}, nil
}

// Initialize performs the MCP handshake: sends "initialize", waits for the
// result, then sends the "notifications/initialized" notification as
// mandated by the spec. Subsequent calls are no-ops.
func (s *Server) Initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: clientProtocolVersion,
		Capabilities:    defaultInitializeCapabilities(),
		ClientInfo: ClientInfo{
			Name:    clientName,
			Version: clientVersion,
		},
	}
	raw, err := s.client.Call(ctx, "initialize", params, s.Timeouts.Initialize)
	if err != nil {
		return fmt.Errorf("mcp: server %q initialize: %w", s.Name, err)
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: server %q decode initialize result: %w", s.Name, err)
	}
	s.info = res.ServerInfo
	s.caps = res.Capabilities

	// Per spec: client MUST send notifications/initialized after receiving
	// the initialize result and before sending any other request.
	if err := s.client.Notify("notifications/initialized", struct{}{}); err != nil {
		return fmt.Errorf("mcp: server %q initialized notification: %w", s.Name, err)
	}
	return nil
}

// Info returns the server's self-identification (populated by Initialize).
func (s *Server) Info() ServerInfo { return s.info }

// ListTools fetches every tool the server advertises. Pagination is
// handled automatically via nextCursor.
func (s *Server) ListTools(ctx context.Context) ([]ToolDef, error) {
	var all []ToolDef
	cursor := ""
	for {
		params := map[string]interface{}{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.client.Call(ctx, "tools/list", params, s.Timeouts.ListTools)
		if err != nil {
			return nil, fmt.Errorf("mcp: server %q tools/list: %w", s.Name, err)
		}
		var page ListToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: server %q decode tools/list: %w", s.Name, err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// CallTool invokes a named tool with the given JSON arguments and returns
// a single flattened string — the text content blocks joined by newlines.
// If the server marks the result as isError, CallTool returns the joined
// content AND an error so callers can surface both to the conversation
// loop (same pattern as the native tool path in runtime.RunTurn).
func (s *Server) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: arguments,
	}
	raw, err := s.client.Call(ctx, "tools/call", params, s.Timeouts.CallTool)
	if err != nil {
		return "", fmt.Errorf("mcp: server %q call %q: %w", s.Name, name, err)
	}
	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("mcp: server %q decode call result: %w", s.Name, err)
	}
	out := flattenContent(result.Content)
	if result.IsError {
		return out, fmt.Errorf("mcp: server %q tool %q reported error", s.Name, name)
	}
	return out, nil
}

// Close tears down the subprocess.
func (s *Server) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// flattenContent turns a slice of ContentItem into a single string. Text
// blocks are concatenated verbatim (separated by "\n"); non-text blocks
// are round-tripped to their JSON form so the model can still see them.
func flattenContent(items []ContentItem) string {
	if len(items) == 0 {
		return ""
	}
	out := make([]byte, 0, 128)
	for i, it := range items {
		if i > 0 {
			out = append(out, '\n')
		}
		switch it.Type {
		case "text":
			out = append(out, it.Text...)
		default:
			// Re-marshal so the LLM sees the full structure.
			b, err := json.Marshal(it)
			if err != nil {
				out = append(out, "["+it.Type+" content]"...)
			} else {
				out = append(out, b...)
			}
		}
	}
	return string(out)
}
