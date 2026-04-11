package provider

import (
	"bufio"
	"context"
	"io"
	"net/http"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// anthropicAPIVersion is the exact value of the "anthropic-version" header.
// Must be sent on every request (see skeleton.md "알아둘 함정").
const anthropicAPIVersion = "2023-06-01"

// anthropicMessagesURL is the /v1/messages endpoint.
const anthropicMessagesURL = "https://api.anthropic.com/v1/messages"

// anthropicProvider implements runtime.Provider backed by the Anthropic
// Messages API. Uses raw net/http — NO external SDK, following the pattern
// used by GoClaw (analysis/platforms/goclaw.md).
//
// The Phase 2 stub keeps the factory and Name() real but leaves Chat() as
// a panic TODO. Phase 2b fills in:
//   - JSON request body serialization (ChatRequest → Anthropic wire format)
//   - HTTP POST with "x-api-key", "anthropic-version", "content-type" headers
//   - SSE stream parsing via sseScanner below
//   - Response accumulation (ChatResponse from streamed events)
//   - Error classification (see error.rs from claw-code for reference)
type anthropicProvider struct {
	apiKey string
	model  string
	http   *http.Client
}

// Name returns "anthropic". Part of runtime.Provider.
func (p *anthropicProvider) Name() string { return "anthropic" }

// Chat runs one chat completion. Stub until Phase 2b.
//
// Implementation plan (see skeleton.md "SSE 이벤트 처리표"):
//  1. Build anthropic request body from runtime.ChatRequest
//  2. POST with streaming (Accept: text/event-stream)
//  3. Read response body through sseScanner
//  4. Dispatch each SSE event into a ChatResponse accumulator
//  5. On message_stop return the accumulated ChatResponse
//  6. On ctx.Done() cancel the in-flight request and return ctx.Err()
func (p *anthropicProvider) Chat(ctx context.Context, req runtime.ChatRequest) (*runtime.ChatResponse, error) {
	panic("TODO: anthropic.Chat not implemented (Phase 2b)")
}

// sseEvent is one parsed Server-Sent Events record.
type sseEvent struct {
	Event string
	Data  []byte
}

// sseScanner reads an SSE stream line-by-line and emits complete events.
// Pattern adapted from GoClaw's internal/providers/sse_reader.go (analysis
// notes in analysis/platforms/goclaw.md §3.7).
//
// Stub until Phase 2b — the struct shape is pinned now so the conversation
// loop and tests can reference it without further churn.
type sseScanner struct {
	r *bufio.Reader
}

// newSSEScanner wraps an io.Reader for SSE parsing.
func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{r: bufio.NewReader(r)}
}

// Next returns the next SSE event, io.EOF when the stream is exhausted, or
// an error for malformed input.
func (s *sseScanner) Next() (*sseEvent, error) {
	panic("TODO: sseScanner.Next not implemented (Phase 2b)")
}
