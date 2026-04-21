package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// mockServerMain is a minimal MCP stdio server implemented as a test
// helper. When invoked through `go test -run=TestHelperMCPServer` with
// GO_TEST_MCP_MOCK=1 set, it reads JSON-RPC frames from stdin, answers
// them deterministically, and exits on EOF (stdin close).
//
// Supported methods:
//   - initialize     → returns fixed serverInfo + empty capabilities
//   - tools/list     → returns two tools: "echo" and "fail"
//   - tools/call     → dispatches to handleCall: echo returns its arg,
//                      fail returns {isError: true, content: [text]}
//
// Unknown methods return error -32601 Method not found.
func mockServerMain() int {
	reader := bufio.NewReaderSize(os.Stdin, 64*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return 0
			}
			fmt.Fprintf(os.Stderr, "mock: read: %v\n", err)
			return 1
		}
		line = trimNL(line)
		if len(line) == 0 {
			continue
		}
		// Probe for request vs notification.
		var probe struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.ID == nil {
			// Notification — no reply. "notifications/initialized" lands here.
			continue
		}
		resp := handleCall(*probe.ID, probe.Method, probe.Params)
		respBuf, _ := json.Marshal(resp)
		respBuf = append(respBuf, '\n')
		_, _ = writer.Write(respBuf)
		_ = writer.Flush()
	}
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func handleCall(id int64, method string, params json.RawMessage) Response {
	resp := Response{JSONRPC: "2.0", ID: id}
	switch method {
	case "initialize":
		result, _ := json.Marshal(InitializeResult{
			ProtocolVersion: clientProtocolVersion,
			Capabilities:    map[string]interface{}{},
			ServerInfo: ServerInfo{
				Name:    "mock-server",
				Version: "0.0.1",
			},
		})
		resp.Result = result
	case "tools/list":
		result, _ := json.Marshal(ListToolsResult{
			Tools: []ToolDef{
				{
					Name:        "echo",
					Description: "Echoes the given message.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`),
				},
				{
					Name:        "fail",
					Description: "Always returns an error.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
				},
			},
		})
		resp.Result = result
	case "tools/call":
		var p CallToolParams
		_ = json.Unmarshal(params, &p)
		switch p.Name {
		case "echo":
			var arg struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(p.Arguments, &arg)
			result, _ := json.Marshal(CallToolResult{
				Content: []ContentItem{{Type: "text", Text: "echo: " + arg.Msg}},
			})
			resp.Result = result
		case "fail":
			result, _ := json.Marshal(CallToolResult{
				Content: []ContentItem{{Type: "text", Text: "something broke"}},
				IsError: true,
			})
			resp.Result = result
		default:
			resp.Error = &RPCError{Code: -32602, Message: "unknown tool: " + p.Name}
		}
	default:
		resp.Error = &RPCError{Code: -32601, Message: "method not found: " + method}
	}
	return resp
}

// TestHelperMCPServer is the entry point the subprocess invokes. When
// GO_TEST_MCP_MOCK is set, run the mock server and exit. Otherwise this
// test is a no-op so it doesn't affect normal test runs.
//
// This helper pattern is idiomatic for testing subprocess-driven code in
// Go stdlib (see os/exec's TestHelperProcess).
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("GO_TEST_MCP_MOCK") != "1" {
		return
	}
	code := mockServerMain()
	// We can't use t.FailNow from a forked subprocess context reliably,
	// so use os.Exit. The harness process checks wait status.
	if code != 0 {
		os.Exit(code)
	}
	os.Exit(0)
}

// spawnMock returns an exec.Cmd (command, args, env) that will run this
// test binary back on itself in helper mode. Callers pass the triple to
// NewServer via StdioConfig.
func spawnMockArgs() (cmd string, args []string, env []string) {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	// `-test.run=TestHelperMCPServer` filters to the helper test only.
	return exe,
		[]string{"-test.run=TestHelperMCPServer", "-test.count=1"},
		append(os.Environ(), "GO_TEST_MCP_MOCK=1")
}

// mockEnv wraps the stderr writer so subprocess diagnostics don't spam
// the test output. Call like: defer resetStderr(silenceStderr(t)).
func silenceStderr(t *testing.T) io.Writer {
	t.Helper()
	prev := stderrSink
	SetStderr(&strings.Builder{})
	return prev
}

func resetStderr(prev io.Writer) { SetStderr(prev) }
