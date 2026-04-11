package runtime

import (
	"context"
	"errors"
)

// Options configures a Runtime instance.
type Options struct {
	// Model is the provider-specific model identifier, e.g. "claude-sonnet-4-6".
	Model string

	// MaxIterations caps how many provider↔tool rounds RunTurn may perform
	// within a single user turn. Hard cap prevents infinite loops if a tool
	// keeps triggering more tool_use blocks. Default: 10.
	MaxIterations int

	// SystemPrompt is the top-level "system" string sent to the provider.
	// Single string for Phase 2; multi-segment / dynamic prompts are a
	// Phase 3 concern (see prompt builder in claw-code analysis).
	SystemPrompt string

	// MaxTokens caps output tokens per Chat call. Anthropic requires this.
	MaxTokens int
}

// ToolCallRecord captures a single tool invocation during a turn, useful for
// logging, replay, and debugging.
type ToolCallRecord struct {
	ToolName string
	Input    string
	Output   string
	IsError  bool
}

// TurnSummary is the result of one RunTurn call. The conversation loop
// returns it to the REPL so the UI layer can decide what to render.
type TurnSummary struct {
	// AssistantMessages are every assistant message produced during the
	// turn (one per provider round, so N ≥ 1 if any tool calls fired).
	AssistantMessages []Message

	// ToolCalls records every tool invocation in the order it fired.
	ToolCalls []ToolCallRecord

	// Iterations counts how many provider rounds ran (1 = no tools, N = N-1
	// tool rounds then a final text response).
	Iterations int

	// StopReason is the provider's final stop reason, e.g. "end_turn",
	// "tool_use" (should never reach the caller — the loop handles it),
	// "max_tokens", "stop_sequence".
	StopReason string

	// Usage aggregates token accounting across all rounds in this turn.
	Usage Usage
}

// Runtime is the conversation engine. It ties together a Provider, the
// registered Tools, and a persistent Session, and exposes RunTurn as the
// single entry point the REPL (or any other driver) calls.
//
// Construction: call New(...). All fields are private; callers configure
// behaviour through Options at construction time.
type Runtime struct {
	provider Provider
	tools    []Tool
	session  *Session
	opts     Options

	toolByName map[string]Tool
}

// New builds a Runtime. It performs no I/O — no provider calls, no file
// writes — so it is safe to call during cli.Run wiring even when the
// provider's Chat body is still a TODO stub.
func New(provider Provider, tools []Tool, session *Session, opts Options) *Runtime {
	if opts.MaxIterations == 0 {
		opts.MaxIterations = 10
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 4096
	}
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Spec().Name] = t
	}
	return &Runtime{
		provider:   provider,
		tools:      tools,
		session:    session,
		opts:       opts,
		toolByName: byName,
	}
}

// Provider returns the configured provider. Useful for tests and diagnostics.
func (r *Runtime) Provider() Provider { return r.provider }

// Tools returns the registered tools in their original order.
func (r *Runtime) Tools() []Tool { return r.tools }

// Session returns the underlying session, or nil if none was configured.
func (r *Runtime) Session() *Session { return r.session }

// ErrNoProvider is returned by RunTurn when no provider was configured.
var ErrNoProvider = errors.New("runtime: no provider configured")

// RunTurn executes one user turn end-to-end. Pseudocode (see skeleton.md
// "턴 루프 알고리즘" for the canonical spec):
//
//  1. Append user message to session
//  2. For i := 0; i < MaxIterations; i++:
//     a. Call provider.Chat(ctx, req with tools[] advertised)
//     b. Append assistant message to session
//     c. If StopReason != "tool_use" → break
//     d. For each tool_use block: resolve tool, Execute(ctx, input)
//     e. Build a single user message whose Content is tool_result blocks
//     f. Append that user message to session
//  3. Return TurnSummary
//
// On ctx cancellation RunTurn returns whatever it has accumulated so far
// (partial TurnSummary) along with ctx.Err().
func (r *Runtime) RunTurn(ctx context.Context, userInput string) (*TurnSummary, error) {
	panic("TODO: runtime.RunTurn not implemented (Phase 2b)")
}
