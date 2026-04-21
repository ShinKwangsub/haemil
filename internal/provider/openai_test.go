package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// TestOpenAIChatTextOnly: typical non-tool text stream, terminated by [DONE].
func TestOpenAIChatTextOnly(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"gemma-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"id":"c1","model":"gemma-4","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`{"id":"c1","model":"gemma-4","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`{"id":"c1","model":"gemma-4","choices":[{"index":0,"delta":{"content":"!"}}]}`,
		`{"id":"c1","model":"gemma-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":15,"completion_tokens":3}}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Header + body validation
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		if auth := r.Header.Get("authorization"); auth != "Bearer test-key" {
			t.Errorf("auth: got %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		_ = json.Unmarshal(body, &reqMap)
		if reqMap["stream"] != true {
			t.Errorf("stream: got %v", reqMap["stream"])
		}
		// system must be prepended into messages[]
		msgs, _ := reqMap["messages"].([]any)
		if len(msgs) < 1 {
			t.Fatal("no messages")
		}
		firstMsg, _ := msgs[0].(map[string]any)
		if firstMsg["role"] != "system" {
			t.Errorf("first message role: got %v, want system", firstMsg["role"])
		}

		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(buildOpenAIStream(chunks))
	}))
	defer srv.Close()

	p := &openaiProvider{
		apiKey: "test-key",
		model:  "gemma-4",
		http:   &http.Client{Timeout: 5 * time.Second},
	}
	p.setEndpoint(srv.URL + "/v1")

	resp, err := p.Chat(context.Background(), runtime.ChatRequest{
		Model:     "gemma-4",
		System:    "you are a test",
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "hi"}}}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if resp.ID != "c1" {
		t.Errorf("id: got %q", resp.ID)
	}
	if resp.Model != "gemma-4" {
		t.Errorf("model: got %q", resp.Model)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q, want end_turn (from finish_reason=stop)", resp.StopReason)
	}
	if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage: %+v", resp.Usage)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1", len(resp.Content))
	}
	if resp.Content[0].Type != runtime.BlockTypeText {
		t.Errorf("block[0] type: %q", resp.Content[0].Type)
	}
	if resp.Content[0].Text != "Hello world!" {
		t.Errorf("block[0] text: got %q", resp.Content[0].Text)
	}
}

// TestOpenAIChatToolCalls: streaming tool call with arguments delivered in
// partial JSON fragments. Verifies accumulation + finish_reason mapping
// (tool_calls → tool_use).
func TestOpenAIChatToolCalls(t *testing.T) {
	chunks := []string{
		`{"id":"c2","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"bash"}}]}}]}`,
		`{"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"comm"}}]}}]}`,
		`{"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls"}}]}}]}`,
		`{"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]}}]}`,
		`{"id":"c2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(buildOpenAIStream(chunks))
	}))
	defer srv.Close()

	p := &openaiProvider{apiKey: "k", model: "m", http: &http.Client{Timeout: 5 * time.Second}}
	p.setEndpoint(srv.URL + "/v1")

	resp, err := p.Chat(context.Background(), runtime.ChatRequest{
		Tools: []runtime.ToolSpec{{
			Name:        "bash",
			Description: "run bash",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		}},
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "ls"}}}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason: got %q, want tool_use (from finish_reason=tool_calls)", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1 (tool_use only)", len(resp.Content))
	}
	block := resp.Content[0]
	if block.Type != runtime.BlockTypeToolUse {
		t.Errorf("block type: got %q", block.Type)
	}
	if block.ID != "call_abc" {
		t.Errorf("id: got %q", block.ID)
	}
	if block.Name != "bash" {
		t.Errorf("name: got %q", block.Name)
	}
	var args map[string]string
	if err := json.Unmarshal(block.Input, &args); err != nil {
		t.Fatalf("input not valid JSON: %v (raw=%s)", err, block.Input)
	}
	if args["command"] != "ls" {
		t.Errorf("args.command: got %q", args["command"])
	}
}

// TestBuildOpenAIMessagesConversion pins the conversion rules.
func TestBuildOpenAIMessagesConversion(t *testing.T) {
	req := runtime.ChatRequest{
		System: "you are test",
		Messages: []runtime.Message{
			// user text
			{Role: runtime.RoleUser, Content: []runtime.ContentBlock{{Type: runtime.BlockTypeText, Text: "run ls"}}},
			// assistant with text + tool_use
			{Role: runtime.RoleAssistant, Content: []runtime.ContentBlock{
				{Type: runtime.BlockTypeText, Text: "sure"},
				{Type: runtime.BlockTypeToolUse, ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
			}},
			// user carrying tool_result
			{Role: runtime.RoleUser, Content: []runtime.ContentBlock{
				{Type: runtime.BlockTypeToolResult, ToolUseID: "call_1", Content: "file1\nfile2"},
			}},
		},
	}
	out := buildOpenAIMessages(req)

	if len(out) != 4 {
		t.Fatalf("len: got %d, want 4 (system + user + assistant + tool)", len(out))
	}
	if out[0].Role != "system" || out[0].Content != "you are test" {
		t.Errorf("system: %+v", out[0])
	}
	if out[1].Role != "user" || out[1].Content != "run ls" {
		t.Errorf("user: %+v", out[1])
	}
	if out[2].Role != "assistant" {
		t.Errorf("assistant role: %+v", out[2])
	}
	if out[2].Content != "sure" {
		t.Errorf("assistant content: %q", out[2].Content)
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool_calls: %+v", out[2].ToolCalls)
	}
	if out[3].Role != "tool" || out[3].ToolCallID != "call_1" || out[3].Content != "file1\nfile2" {
		t.Errorf("tool: %+v", out[3])
	}
}

// TestOpenAIChatHTTPError verifies error envelope parsing.
func TestOpenAIChatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"model not found","code":"model_not_found"}}`))
	}))
	defer srv.Close()

	p := &openaiProvider{apiKey: "k", model: "m", http: &http.Client{Timeout: 5 * time.Second}}
	p.setEndpoint(srv.URL + "/v1")

	_, err := p.Chat(context.Background(), runtime.ChatRequest{MaxTokens: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error message: %q", err.Error())
	}
}

// TestOpenAINoKeyNoAuthHeader verifies empty apiKey causes NO Authorization
// header to be sent (for local servers that reject bogus bearer tokens).
func TestOpenAINoKeyNoAuthHeader(t *testing.T) {
	authSeen := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("authorization")
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(buildOpenAIStream([]string{
			`{"id":"c","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}))
	}))
	defer srv.Close()

	p := &openaiProvider{apiKey: "", model: "m", http: &http.Client{Timeout: 5 * time.Second}}
	p.setEndpoint(srv.URL + "/v1")

	_, err := p.Chat(context.Background(), runtime.ChatRequest{MaxTokens: 1})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if authSeen != "" {
		t.Errorf("expected no Authorization header for empty apiKey, got %q", authSeen)
	}
}

// ---- helpers ----

// buildOpenAIStream concatenates chunks in SSE format with a trailing [DONE].
func buildOpenAIStream(chunks []string) []byte {
	var b bytes.Buffer
	for _, c := range chunks {
		b.WriteString("data: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.Bytes()
}
