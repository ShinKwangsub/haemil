package provider

import (
	"bufio"
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

// anthropicAPIVersion is the exact value of the "anthropic-version" header.
// Must be sent on every request (skeleton.md §9 gotcha #4).
const anthropicAPIVersion = "2023-06-01"

// anthropicMessagesURL is the /v1/messages endpoint.
const anthropicMessagesURL = "https://api.anthropic.com/v1/messages"

// anthropicProvider implements runtime.Provider backed by the Anthropic
// Messages API. Uses raw net/http — NO external SDK.
type anthropicProvider struct {
	apiKey string
	model  string
	http   *http.Client

	// endpointURL overrides anthropicMessagesURL. Set by tests via the
	// unexported setEndpoint helper. Empty in production.
	endpointURL string
}

// Name returns "anthropic". Part of runtime.Provider.
func (p *anthropicProvider) Name() string { return "anthropic" }

// setEndpoint overrides the default messages URL. Test-only hook.
func (p *anthropicProvider) setEndpoint(url string) { p.endpointURL = url }

// anthropicRequest is the on-wire request body.
//
// Notes (skeleton.md §9):
//   - "system" is top-level, NOT inside messages (gotcha #1)
//   - "max_tokens" is required (gotcha #3)
//   - "stream": true for SSE streaming (gotcha #12)
type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []runtime.Message  `json:"messages"`
	Tools       []runtime.ToolSpec `json:"tools,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream"`
}

// anthropicErrorEnvelope matches {"type":"error","error":{...}} (gotcha #13).
type anthropicErrorEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Chat performs one streaming chat completion.
func (p *anthropicProvider) Chat(ctx context.Context, req runtime.ChatRequest) (*runtime.ChatResponse, error) {
	if p.apiKey == "" {
		return nil, errors.New("anthropic: ANTHROPIC_API_KEY not set")
	}
	if req.Model == "" {
		req.Model = p.model
	}
	if req.MaxTokens == 0 {
		// Gotcha #3: max_tokens is required. Use a sensible default.
		req.MaxTokens = 4096
	}

	body := anthropicRequest{
		Model:       req.Model,
		System:      req.System,
		Messages:    req.Messages,
		Tools:       req.Tools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	url := p.endpointURL
	if url == "" {
		url = anthropicMessagesURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	// Gotcha #4, #5, #7: x-api-key (not Authorization Bearer), anthropic-version,
	// accept: text/event-stream.
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, classifyAnthropicError(resp.StatusCode, bodyBytes)
	}

	return accumulateSSE(resp.Body)
}

// classifyAnthropicError parses the error envelope and returns a
// descriptive error. Handles gotcha #13 + #11 (429/529 rate limit).
func classifyAnthropicError(status int, body []byte) error {
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return fmt.Errorf("anthropic: %d %s: %s", status, env.Error.Type, env.Error.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 256 {
		snippet = snippet[:256] + "..."
	}
	return fmt.Errorf("anthropic: http %d: %s", status, snippet)
}

// accumulateSSE reads an SSE stream and accumulates events into a single
// runtime.ChatResponse. See skeleton.md §6 for the full event-to-field
// mapping.
func accumulateSSE(r io.Reader) (*runtime.ChatResponse, error) {
	scanner := newSSEScanner(r)

	resp := &runtime.ChatResponse{}
	// Per-block JSON input buffer (gotcha #6: input_json_delta accumulation).
	// Keyed by content block index.
	inputBufs := map[int]*bytes.Buffer{}

	for {
		ev, err := scanner.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("anthropic: sse: %w", err)
		}
		if ev == nil {
			break
		}

		if err := applySSEEvent(resp, inputBufs, ev); err != nil {
			return nil, err
		}
		// message_stop signals end-of-stream.
		if ev.Event == "message_stop" {
			break
		}
	}

	// Finalize any tool_use blocks whose input JSON we accumulated but
	// never received a content_block_stop for (defensive — well-formed
	// streams always stop the block).
	for idx, buf := range inputBufs {
		if idx < len(resp.Content) && resp.Content[idx].Type == runtime.BlockTypeToolUse && len(resp.Content[idx].Input) == 0 && buf.Len() > 0 {
			resp.Content[idx].Input = json.RawMessage(append([]byte(nil), buf.Bytes()...))
		}
	}
	return resp, nil
}

// sseEventPayload is the generic shape of one SSE event's data field.
type sseEventPayload struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"`

	// message_start
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message,omitempty"`

	// content_block_start
	ContentBlock struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content_block,omitempty"`

	// content_block_delta / message_delta
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		StopReason  string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`

	Usage struct {
		InputTokens  int `json:"input_tokens,omitempty"`
		OutputTokens int `json:"output_tokens,omitempty"`
	} `json:"usage,omitempty"`

	// error
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// applySSEEvent mutates resp and inputBufs based on one SSE event.
func applySSEEvent(resp *runtime.ChatResponse, inputBufs map[int]*bytes.Buffer, ev *sseEvent) error {
	if len(ev.Data) == 0 {
		return nil
	}
	var p sseEventPayload
	if err := json.Unmarshal(ev.Data, &p); err != nil {
		return fmt.Errorf("parse sse data (event=%s): %w", ev.Event, err)
	}

	switch ev.Event {
	case "message_start":
		resp.ID = p.Message.ID
		resp.Model = p.Message.Model
		resp.Usage.InputTokens = p.Message.Usage.InputTokens
		resp.Usage.OutputTokens = p.Message.Usage.OutputTokens

	case "content_block_start":
		block := runtime.ContentBlock{}
		switch p.ContentBlock.Type {
		case "text":
			block.Type = runtime.BlockTypeText
			block.Text = p.ContentBlock.Text
		case "tool_use":
			block.Type = runtime.BlockTypeToolUse
			block.ID = p.ContentBlock.ID
			block.Name = p.ContentBlock.Name
			// input may be included here as an empty object; we'll overwrite
			// from accumulated partial_json at content_block_stop.
			if len(p.ContentBlock.Input) > 0 {
				block.Input = p.ContentBlock.Input
			}
			inputBufs[p.Index] = &bytes.Buffer{}
		default:
			// Unknown block type — fall back to raw text marker so we can
			// see it if it happens in practice.
			block.Type = runtime.BlockType(p.ContentBlock.Type)
		}
		// Ensure the slice has room at the given index.
		for len(resp.Content) <= p.Index {
			resp.Content = append(resp.Content, runtime.ContentBlock{})
		}
		resp.Content[p.Index] = block

	case "content_block_delta":
		if p.Index >= len(resp.Content) {
			// Stream is misaligned. Defensive grow rather than panic.
			for len(resp.Content) <= p.Index {
				resp.Content = append(resp.Content, runtime.ContentBlock{})
			}
		}
		switch p.Delta.Type {
		case "text_delta":
			resp.Content[p.Index].Text += p.Delta.Text
		case "input_json_delta":
			// Gotcha #6: accumulate partial JSON — parse later.
			buf, ok := inputBufs[p.Index]
			if !ok {
				buf = &bytes.Buffer{}
				inputBufs[p.Index] = buf
			}
			buf.WriteString(p.Delta.PartialJSON)
		}

	case "content_block_stop":
		if p.Index < len(resp.Content) && resp.Content[p.Index].Type == runtime.BlockTypeToolUse {
			if buf, ok := inputBufs[p.Index]; ok && buf.Len() > 0 {
				// Verify it parses; if not, store raw bytes anyway so the
				// caller can see what the model produced.
				raw := append([]byte(nil), buf.Bytes()...)
				var tmp any
				if err := json.Unmarshal(raw, &tmp); err != nil {
					return fmt.Errorf("anthropic: tool_use[%d] input is not valid JSON: %w", p.Index, err)
				}
				resp.Content[p.Index].Input = json.RawMessage(raw)
				delete(inputBufs, p.Index)
			}
		}

	case "message_delta":
		if p.Delta.StopReason != "" {
			resp.StopReason = p.Delta.StopReason
		}
		if p.Usage.OutputTokens != 0 {
			resp.Usage.OutputTokens = p.Usage.OutputTokens
		}
		if p.Usage.InputTokens != 0 {
			resp.Usage.InputTokens = p.Usage.InputTokens
		}

	case "message_stop":
		// Nothing to do — caller breaks out of the loop.

	case "ping":
		// Keepalive — ignore.

	case "error":
		return fmt.Errorf("anthropic: stream error: %s: %s", p.Error.Type, p.Error.Message)

	default:
		// Unknown event — ignore but do not abort.
	}

	return nil
}

// sseEvent is one parsed Server-Sent Events record.
type sseEvent struct {
	Event string
	Data  []byte
}

// sseScanner reads an SSE stream and emits complete events. Pattern adapted
// from GoClaw's internal/providers/sse_reader.go.
//
// SSE format per frame (separated by blank lines):
//
//	event: content_block_delta
//	data: {"type":"content_block_delta",...}
type sseScanner struct {
	r *bufio.Reader
}

// newSSEScanner wraps an io.Reader for SSE parsing.
func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next SSE event, io.EOF when the stream is exhausted.
func (s *sseScanner) Next() (*sseEvent, error) {
	var (
		ev        sseEvent
		dataLines [][]byte
	)

	for {
		line, err := s.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Flush a pending event on EOF if we had partial data.
				if ev.Event != "" || len(dataLines) > 0 {
					ev.Data = joinDataLines(dataLines)
					return &ev, nil
				}
				return nil, io.EOF
			}
			return nil, err
		}

		// Strip trailing CR (CRLF line endings).
		line = bytes.TrimRight(line, "\r")

		// Blank line = end of frame.
		if len(line) == 0 {
			if ev.Event == "" && len(dataLines) == 0 {
				// Consecutive blank lines — skip.
				continue
			}
			ev.Data = joinDataLines(dataLines)
			return &ev, nil
		}

		// Comment line starts with ':'.
		if line[0] == ':' {
			continue
		}

		// field: value  (colon may be followed by a single optional space).
		colon := bytes.IndexByte(line, ':')
		var field, value string
		if colon < 0 {
			field = string(line)
			value = ""
		} else {
			field = string(line[:colon])
			value = string(line[colon+1:])
			// Per SSE spec: if value starts with a single space, strip it.
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch field {
		case "event":
			ev.Event = value
		case "data":
			dataLines = append(dataLines, []byte(value))
		case "id", "retry":
			// Unused by Anthropic's API.
		}
	}
}

// readLine returns one line without the trailing \n. Long lines are
// supported (ReadString handles them, just slower for very long lines).
func (s *sseScanner) readLine() ([]byte, error) {
	line, err := s.r.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(line) == 0 {
		return nil, io.EOF
	}
	if line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if err != nil && errors.Is(err, io.EOF) && len(line) == 0 {
		return nil, io.EOF
	}
	return line, nil
}

// joinDataLines concatenates multiple data: lines with newlines (per SSE spec).
func joinDataLines(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) == 1 {
		// Return a copy so mutations to the bufio buffer don't affect us.
		out := make([]byte, len(lines[0]))
		copy(out, lines[0])
		return out
	}
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	out := make([]byte, 0, total)
	for i, l := range lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, l...)
	}
	return out
}
