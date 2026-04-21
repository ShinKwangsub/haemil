package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// defaultOpenAIBase is the public OpenAI endpoint. Local servers (oMLX,
// llama.cpp, Ollama, LM Studio, vLLM, SGLang, ...) all speak the same
// wire format, just on a different base URL.
const defaultOpenAIBase = "https://api.openai.com/v1"

// openaiProvider implements runtime.Provider for the OpenAI chat/completions
// API and any compatible server. Differences from anthropicProvider:
//
//   - Auth is Bearer in Authorization header
//   - System prompts are the first message in messages[] with role="system"
//     (Anthropic puts them as a top-level system field — gotcha flip)
//   - Tools wrap the schema as {"type":"function","function":{...}}
//   - Streaming uses delta.content + delta.tool_calls; terminated by [DONE]
//
// apiKey may be empty for local servers that don't require auth (e.g. oMLX).
type openaiProvider struct {
	apiKey      string
	model       string
	http        *http.Client
	endpointURL string // override for base URL (e.g. http://127.0.0.1:8080)
}

// Name returns "openai". This is the logical provider name, not the
// deployment. oMLX/Ollama/vLLM all report "openai" here — callers
// distinguish by endpoint and model.
func (p *openaiProvider) Name() string { return "openai" }

// setEndpoint overrides the default base URL. Test-only hook, mirrors anthropic.
func (p *openaiProvider) setEndpoint(url string) { p.endpointURL = url }

// ---- on-wire request types ----

type openaiReqMessage struct {
	Role       string                 `json:"role"` // system | user | assistant | tool
	Content    string                 `json:"content,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"` // role=tool
	ToolCalls  []openaiReqToolCallOut `json:"tool_calls,omitempty"`   // role=assistant echo
	Name       string                 `json:"name,omitempty"`
}

type openaiReqToolCallOut struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // always "function"
	Function openaiReqToolCallF `json:"function"`
}

type openaiReqToolCallF struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiReqTool struct {
	Type     string            `json:"type"` // "function"
	Function openaiReqToolSpec `json:"function"`
}

type openaiReqToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiRequest struct {
	Model       string             `json:"model"`
	Messages    []openaiReqMessage `json:"messages"`
	Tools       []openaiReqTool    `json:"tools,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream"`
}

// Chat implements runtime.Provider.
func (p *openaiProvider) Chat(ctx context.Context, req runtime.ChatRequest) (*runtime.ChatResponse, error) {
	if req.Model == "" {
		req.Model = p.model
	}

	body := openaiRequest{
		Model:       req.Model,
		Messages:    buildOpenAIMessages(req),
		Tools:       buildOpenAITools(req.Tools),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}

	base := p.endpointURL
	if base == "" {
		base = defaultOpenAIBase
	}
	url := strings.TrimRight(base, "/") + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	// Only send Authorization if a key is configured — local servers often
	// reject bogus bearer tokens but accept no Authorization header at all.
	if p.apiKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, classifyOpenAIError(resp.StatusCode, bodyBytes)
	}

	return accumulateOpenAISSE(resp.Body, req.Model)
}

// buildOpenAIMessages converts runtime.Messages to OpenAI wire format.
//
// Key conversions:
//   - runtime.System (top-level) → prepend {role:"system",content:...}
//   - user text block → {role:"user",content:"..."}
//   - user tool_result block → {role:"tool",tool_call_id:...,content:...}
//   - assistant text block → {role:"assistant",content:"..."}
//   - assistant tool_use block → {role:"assistant",tool_calls:[{...}]}
//
// When an assistant message has both text and tool_use, we emit them as a
// single OpenAI message with both content and tool_calls (OpenAI allows this).
func buildOpenAIMessages(req runtime.ChatRequest) []openaiReqMessage {
	out := make([]openaiReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, openaiReqMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case runtime.RoleUser:
			// Split into text parts (role:user) and tool_result parts (role:tool).
			// Each OpenAI message carries ONE role, so split into multiple.
			var textParts []string
			for _, b := range m.Content {
				switch b.Type {
				case runtime.BlockTypeText:
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				case runtime.BlockTypeToolResult:
					out = append(out, openaiReqMessage{
						Role:       "tool",
						ToolCallID: b.ToolUseID,
						Content:    b.Content,
					})
				}
			}
			if len(textParts) > 0 {
				out = append(out, openaiReqMessage{
					Role:    "user",
					Content: strings.Join(textParts, "\n"),
				})
			}
		case runtime.RoleAssistant:
			var textParts []string
			var toolCalls []openaiReqToolCallOut
			for _, b := range m.Content {
				switch b.Type {
				case runtime.BlockTypeText:
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				case runtime.BlockTypeToolUse:
					toolCalls = append(toolCalls, openaiReqToolCallOut{
						ID:   b.ID,
						Type: "function",
						Function: openaiReqToolCallF{
							Name:      b.Name,
							Arguments: string(b.Input),
						},
					})
				}
			}
			msg := openaiReqMessage{Role: "assistant"}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "\n")
			}
			if len(toolCalls) > 0 {
				msg.ToolCalls = toolCalls
			}
			// Emit only if the message has any content at all.
			if msg.Content != "" || len(msg.ToolCalls) > 0 {
				out = append(out, msg)
			}
		}
	}
	return out
}

// buildOpenAITools wraps runtime.ToolSpec into OpenAI's nested function form.
func buildOpenAITools(tools []runtime.ToolSpec) []openaiReqTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openaiReqTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openaiReqTool{
			Type: "function",
			Function: openaiReqToolSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// ---- response accumulation ----

type openaiErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func classifyOpenAIError(status int, body []byte) error {
	var env openaiErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return fmt.Errorf("openai: %d %s: %s", status, env.Error.Type, env.Error.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 256 {
		snippet = snippet[:256] + "..."
	}
	return fmt.Errorf("openai: http %d: %s", status, snippet)
}

// openaiChunk is one SSE chunk's JSON payload from /chat/completions stream.
type openaiChunk struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	// Non-standard but common: some servers (e.g. oMLX) emit usage only on
	// the final chunk; OpenAI itself requires stream_options to enable it.
	Usage *openaiChunkUsage `json:"usage,omitempty"`
	// Choices is an array but streaming only ever has one element.
	Choices []struct {
		Index        int                `json:"index"`
		Delta        openaiStreamDelta  `json:"delta"`
		FinishReason string             `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

type openaiChunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	InputTokens      int `json:"input_tokens"`  // oMLX style
	OutputTokens     int `json:"output_tokens"` // oMLX style
}

type openaiStreamDelta struct {
	Role      string                  `json:"role,omitempty"`
	Content   string                  `json:"content,omitempty"`
	ToolCalls []openaiStreamToolCallD `json:"tool_calls,omitempty"`
}

type openaiStreamToolCallD struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"` // partial JSON per delta
	} `json:"function,omitempty"`
}

// accumulateOpenAISSE reads the SSE stream and builds a runtime.ChatResponse.
// Reuses sseScanner (same frame format as Anthropic). Differences from
// Anthropic's SSE:
//   - Stream ends when a line `data: [DONE]` is received.
//   - Text arrives as {"choices":[{"delta":{"content":"..."}}]}
//   - Tool calls arrive as partial JSON in delta.tool_calls[].function.arguments
//     — accumulated per index, finalized when the stream ends.
func accumulateOpenAISSE(r io.Reader, fallbackModel string) (*runtime.ChatResponse, error) {
	scanner := newSSEScanner(r)

	resp := &runtime.ChatResponse{Model: fallbackModel}
	// Keyed by choice index. For text we keep a builder; for tool_calls we
	// keep per-tool-call-index slots that carry id/name and a partial-JSON
	// arguments buffer.
	var textBuf strings.Builder
	type toolAcc struct {
		ID   string
		Name string
		Args strings.Builder
	}
	toolCalls := map[int]*toolAcc{}
	toolOrder := []int{} // preserve first-seen order
	var finishReason string

	for {
		ev, err := scanner.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("openai: sse: %w", err)
		}
		if ev == nil {
			break
		}
		// Stream terminator.
		if bytes.Equal(ev.Data, []byte("[DONE]")) {
			break
		}
		if len(ev.Data) == 0 {
			continue
		}
		var chunk openaiChunk
		if err := json.Unmarshal(ev.Data, &chunk); err != nil {
			return nil, fmt.Errorf("openai: parse chunk: %w", err)
		}
		if resp.ID == "" && chunk.ID != "" {
			resp.ID = chunk.ID
		}
		if chunk.Model != "" {
			resp.Model = chunk.Model
		}
		if chunk.Usage != nil {
			// Prefer explicit *_tokens fields; fall back to OpenAI's
			// prompt/completion_tokens naming.
			if chunk.Usage.InputTokens > 0 {
				resp.Usage.InputTokens = chunk.Usage.InputTokens
			} else if chunk.Usage.PromptTokens > 0 {
				resp.Usage.InputTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.OutputTokens > 0 {
				resp.Usage.OutputTokens = chunk.Usage.OutputTokens
			} else if chunk.Usage.CompletionTokens > 0 {
				resp.Usage.OutputTokens = chunk.Usage.CompletionTokens
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				textBuf.WriteString(ch.Delta.Content)
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc, ok := toolCalls[tc.Index]
				if !ok {
					acc = &toolAcc{}
					toolCalls[tc.Index] = acc
					toolOrder = append(toolOrder, tc.Index)
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function.Name != "" {
					acc.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.Args.WriteString(tc.Function.Arguments)
				}
			}
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
		}
	}

	// Build content blocks: text first (if any), then each tool call in index order.
	if textBuf.Len() > 0 {
		resp.Content = append(resp.Content, runtime.ContentBlock{
			Type: runtime.BlockTypeText,
			Text: textBuf.String(),
		})
	}
	for _, idx := range toolOrder {
		acc := toolCalls[idx]
		if acc == nil || acc.Name == "" {
			continue
		}
		args := acc.Args.String()
		if args == "" {
			args = "{}"
		}
		// Validate JSON so downstream consumers don't parse garbage.
		var tmp any
		if err := json.Unmarshal([]byte(args), &tmp); err != nil {
			return nil, fmt.Errorf("openai: tool_calls[%d] arguments not valid JSON: %w (raw=%s)", idx, err, args)
		}
		resp.Content = append(resp.Content, runtime.ContentBlock{
			Type:  runtime.BlockTypeToolUse,
			ID:    acc.ID,
			Name:  acc.Name,
			Input: json.RawMessage(args),
		})
	}

	// Map finish_reason to Anthropic-style stop_reason so the conversation
	// loop doesn't need to know which provider responded.
	switch finishReason {
	case "tool_calls":
		resp.StopReason = "tool_use"
	case "stop":
		resp.StopReason = "end_turn"
	case "length":
		resp.StopReason = "max_tokens"
	default:
		resp.StopReason = finishReason
	}

	return resp, nil
}
