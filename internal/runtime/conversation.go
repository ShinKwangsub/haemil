package runtime

import (
	"context"
	"errors"
	"fmt"
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

// RunTurn executes one user turn end-to-end. See skeleton.md §5 for the
// canonical algorithm spec.
//
// On ctx cancellation RunTurn returns whatever it has accumulated so far
// (partial TurnSummary) along with ctx.Err().
func (r *Runtime) RunTurn(ctx context.Context, userInput string) (*TurnSummary, error) {
	if r == nil || r.provider == nil {
		return nil, ErrNoProvider
	}
	if userInput == "" {
		return nil, errors.New("runtime: userInput is empty")
	}

	summary := &TurnSummary{}

	// 1. User message → session + history
	userMsg := Message{
		Role:    RoleUser,
		Content: []ContentBlock{{Type: BlockTypeText, Text: userInput}},
	}
	if r.session != nil {
		if err := r.session.AppendUser(userMsg); err != nil {
			return summary, fmt.Errorf("runtime: append user: %w", err)
		}
	}

	// Build initial history: prior session messages + new user message.
	var history []Message
	if r.session != nil {
		history = r.session.Messages()
	} else {
		history = []Message{userMsg}
	}

	// Advertise tools from registry.
	toolSpecs := make([]ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		toolSpecs = append(toolSpecs, t.Spec())
	}

	// 2. Provider ↔ Tool round loop.
	for i := 0; i < r.opts.MaxIterations; i++ {
		summary.Iterations++

		// Honor ctx cancellation between rounds.
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		// 2a. Call provider.Chat.
		req := ChatRequest{
			Model:     r.opts.Model,
			System:    r.opts.SystemPrompt,
			Messages:  history,
			Tools:     toolSpecs,
			MaxTokens: r.opts.MaxTokens,
		}
		resp, err := r.provider.Chat(ctx, req)
		if err != nil {
			return summary, fmt.Errorf("runtime: provider.Chat (iter %d): %w", i, err)
		}
		summary.Usage.InputTokens += resp.Usage.InputTokens
		summary.Usage.OutputTokens += resp.Usage.OutputTokens
		summary.StopReason = resp.StopReason

		// 2b. Assistant message → session + history + summary.
		assistantMsg := Message{Role: RoleAssistant, Content: resp.Content}
		if r.session != nil {
			if err := r.session.AppendAssistant(assistantMsg); err != nil {
				return summary, fmt.Errorf("runtime: append assistant: %w", err)
			}
		}
		history = append(history, assistantMsg)
		summary.AssistantMessages = append(summary.AssistantMessages, assistantMsg)

		// 2c. Collect tool_use blocks.
		var toolUses []ContentBlock
		for _, block := range resp.Content {
			if block.Type == BlockTypeToolUse {
				toolUses = append(toolUses, block)
			}
		}
		if len(toolUses) == 0 {
			// No tools requested → turn complete.
			return summary, nil
		}

		// 2d. Execute each tool, build tool_result blocks.
		resultBlocks := make([]ContentBlock, 0, len(toolUses))
		for _, use := range toolUses {
			tool, ok := r.toolByName[use.Name]
			if !ok {
				resultBlocks = append(resultBlocks, ContentBlock{
					Type:      BlockTypeToolResult,
					ToolUseID: use.ID,
					Content:   "unknown tool: " + use.Name,
					IsError:   true,
				})
				summary.ToolCalls = append(summary.ToolCalls, ToolCallRecord{
					ToolName: use.Name,
					Input:    string(use.Input),
					Output:   "unknown tool: " + use.Name,
					IsError:  true,
				})
				continue
			}
			output, execErr := tool.Execute(ctx, use.Input)
			isErr := execErr != nil
			content := output
			if isErr {
				if content != "" {
					content = content + "\n" + execErr.Error()
				} else {
					content = execErr.Error()
				}
			}
			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      BlockTypeToolResult,
				ToolUseID: use.ID,
				Content:   content,
				IsError:   isErr,
			})
			summary.ToolCalls = append(summary.ToolCalls, ToolCallRecord{
				ToolName: use.Name,
				Input:    string(use.Input),
				Output:   content,
				IsError:  isErr,
			})
		}

		// 2e. User message carrying tool_result blocks.
		toolResultMsg := Message{Role: RoleUser, Content: resultBlocks}
		if r.session != nil {
			if err := r.session.AppendUser(toolResultMsg); err != nil {
				return summary, fmt.Errorf("runtime: append tool_result: %w", err)
			}
		}
		history = append(history, toolResultMsg)
	}

	// 3. Hit max_iterations — return with whatever we accumulated.
	summary.StopReason = "max_iterations"
	return summary, nil
}
