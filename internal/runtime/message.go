// Package runtime defines the core types and interfaces of the Haemil conversation engine.
//
// This package owns the domain types (Message, ContentBlock, ChatRequest, ChatResponse)
// AND the interfaces (Provider, Tool) that they depend on. Following the Go idiom
// "the consumer defines the interface", runtime is the consumer of both providers
// and tools, so it declares what they must implement.
//
// Providers and tools sub-packages import runtime but never each other.
package runtime

import (
	"context"
	"encoding/json"
)

// Role identifies who produced a message in the conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// BlockType identifies the kind of content inside a ContentBlock.
type BlockType string

const (
	BlockTypeText       BlockType = "text"
	BlockTypeToolUse    BlockType = "tool_use"
	BlockTypeToolResult BlockType = "tool_result"
)

// ContentBlock is a single typed chunk inside a Message.
//
// Anthropic's wire format uses a discriminated union on "type". We model it
// with a flat struct whose fields are omitempty, so marshalling only writes
// the fields relevant to the active block type.
//
// Mapping to Anthropic:
//
//	text:        {"type": "text", "text": "..."}
//	tool_use:    {"type": "tool_use", "id": "...", "name": "...", "input": {...}}
//	tool_result: {"type": "tool_result", "tool_use_id": "...", "content": "...", "is_error": bool}
type ContentBlock struct {
	Type BlockType `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result block
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Message is one turn contributed by the user, assistant, or a tool result set.
//
// Anthropic mapping notes (see skeleton.md "알아둘 함정" section):
//   - System prompts are NOT part of messages; they are a top-level "system"
//     field in ChatRequest.
//   - tool_result blocks belong to messages with role "user", NOT a separate
//     "tool" role.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ToolSpec advertises a tool to the provider (Anthropic tools[] entry).
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ChatRequest is the normalized request shape the conversation loop sends to a Provider.
//
// Providers translate this to their wire format. For Anthropic:
//   - System   → top-level "system" string
//   - Messages → "messages" array
//   - Tools    → "tools" array
//   - MaxTokens → "max_tokens" (REQUIRED by Anthropic — must always be set)
type ChatRequest struct {
	Model       string     `json:"model"`
	System      string     `json:"system,omitempty"`
	Messages    []Message  `json:"messages"`
	Tools       []ToolSpec `json:"tools,omitempty"`
	MaxTokens   int        `json:"max_tokens"`
	Temperature float64    `json:"temperature,omitempty"`
}

// Usage reports token accounting for a single Chat call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ChatResponse is the normalized response shape returned by a Provider.
//
// SSE accumulation (see skeleton.md "SSE 이벤트 처리표"):
//   - message_start              → initialize StopReason zero
//   - content_block_start        → append empty ContentBlock of the right type
//   - content_block_delta text   → append to last block's Text
//   - content_block_delta input  → accumulate JSON fragments, parse at stop
//   - content_block_stop         → finalize the current block
//   - message_delta              → update Usage.OutputTokens, StopReason
//   - message_stop               → flush, return final response
type ChatResponse struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      Usage          `json:"usage"`
}

// Provider is the interface every LLM backend must satisfy.
//
// Runtime owns this interface (consumer-defined). Concrete implementations
// live under internal/provider/.
type Provider interface {
	// Name returns the provider identifier, e.g. "anthropic".
	Name() string

	// Chat performs a single chat completion. Streaming providers MAY collect
	// the stream internally and return the final accumulated ChatResponse —
	// the conversation loop does not care about incremental deltas at this
	// layer (that's a concern for the REPL/UI layer in a later phase).
	//
	// Chat MUST honour ctx cancellation. On ctx.Err() != nil it may return a
	// partial response or an error — documented per-provider in skeleton.md.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// Tool is the interface every executable tool must satisfy.
//
// Runtime owns this interface (consumer-defined). Concrete implementations
// live under internal/tools/.
type Tool interface {
	// Spec returns the static schema used to advertise the tool to the
	// provider. Spec() is called often and should be cheap — implementations
	// typically return a cached struct literal.
	Spec() ToolSpec

	// Execute runs the tool with the given JSON input and returns the output
	// string to be embedded in a tool_result block. Errors are surfaced both
	// as the returned error AND as the tool_result's is_error field (see
	// conversation loop in skeleton.md for the exact policy).
	//
	// Execute MUST honour ctx cancellation (e.g. kill subprocesses on
	// ctx.Done()).
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}
