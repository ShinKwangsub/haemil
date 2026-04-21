// Package mcp implements a minimal Model Context Protocol client.
//
// Scope (MVP, C7): stdio-transport only, JSON-RPC 2.0 over newline-delimited
// JSON on stdin/stdout, synchronous request/response + notifications. OAuth,
// SSE, HTTP, WebSocket transports are out of scope for this cycle — see
// claw-code's mcp_client.rs for the full set.
//
// Layered:
//   - protocol.go     — message shapes + helpers
//   - stdio_client.go — subprocess transport
//   - server.go       — Server type: Initialize/ListTools/CallTool
//   - tool.go         — runtime.Tool adapter for a discovered MCP tool
//   - registry.go     — config loading + multi-server bootstrap
package mcp

import (
	"encoding/json"
	"fmt"
)

// The JSON-RPC version string. Always "2.0".
const jsonRPCVersion = "2.0"

// MCP protocol version this client advertises in Initialize. 2025-03-26 is
// the current public spec as of this commit; servers that require a newer
// version will reject the handshake.
const clientProtocolVersion = "2025-03-26"

// Client name + version sent as part of the Initialize clientInfo.
const (
	clientName    = "haemil"
	clientVersion = "0.1.0-c7"
)

// Request is a JSON-RPC 2.0 request (with id). Notifications are a
// separate type (Notification) because they have no id and carry no
// reply — modelling them as the same struct led to mistakes in Rust.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no id, no reply).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response. Either Result or Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the standard JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// InitializeParams is the payload for the "initialize" request.
type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      ClientInfo             `json:"clientInfo"`
}

// ClientInfo identifies the client to the server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the server's response to "initialize".
type InitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      ServerInfo             `json:"serverInfo"`
}

// ServerInfo describes the server to the client.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ListToolsResult is the result of "tools/list".
type ListToolsResult struct {
	Tools      []ToolDef `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// ToolDef is how a tool is advertised by an MCP server.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// CallToolParams invokes a tool by name with arguments.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the outcome of "tools/call". Content is the flattened
// output — by convention, text/image/resource blocks. We only surface text
// for the MVP; other kinds are stringified.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single block inside a CallToolResult.
//
//	{"type": "text", "text": "..."}
//	{"type": "image", "data": "base64", "mimeType": "image/png"}
//	{"type": "resource", "resource": {...}}
//
// For MVP we care about the "text" case; others are serialised back to JSON
// when surfaced to the LLM, so the model can still reason about them.
type ContentItem struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// defaultInitializeCapabilities returns the client capability object we
// advertise during initialize. We claim no client-side features for MVP —
// we are a pure consumer.
func defaultInitializeCapabilities() map[string]interface{} {
	return map[string]interface{}{}
}
