package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Protocol / helpers ----------------------------------------------

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"with space":    "with_space",
		"dot.separator": "dot_separator",
		"UPPER_CASE-1":  "UPPER_CASE-1",
		"unicode✨":      "unicode_",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestFormatToolName(t *testing.T) {
	got := formatToolName("filesystem", "read_file")
	want := "mcp__filesystem__read_file"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = formatToolName("my server", "weird.name")
	want = "mcp__my_server__weird_name"
	if got != want {
		t.Errorf("sanitised: got %q, want %q", got, want)
	}
}

func TestFlattenContent(t *testing.T) {
	items := []ContentItem{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
	}
	got := flattenContent(items)
	if got != "hello\nworld" {
		t.Errorf("text-only: got %q", got)
	}

	items = []ContentItem{
		{Type: "image", Data: "abc", MimeType: "image/png"},
	}
	got = flattenContent(items)
	if !strings.Contains(got, "image") {
		t.Errorf("non-text: got %q, want to contain 'image'", got)
	}
}

// --- LoadConfig -------------------------------------------------------

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected empty config, got %d servers", len(cfg.Servers))
	}
}

func TestLoadConfigRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	body := `{"servers":{"fs":{"command":"npx","args":["a","b"],"env":{"DEBUG":"1"}}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	sc, ok := cfg.Servers["fs"]
	if !ok {
		t.Fatal("server 'fs' missing")
	}
	if sc.Command != "npx" || len(sc.Args) != 2 || sc.Env["DEBUG"] != "1" {
		t.Errorf("config: %+v", sc)
	}
}

// --- End-to-end: mock server ------------------------------------------

// TestServerInitializeAndListTools spawns the mock server subprocess,
// runs the real handshake + tools/list flow, and inspects the result.
func TestServerInitializeAndListTools(t *testing.T) {
	defer resetStderr(silenceStderr(t))

	cmd, args, env := spawnMockArgs()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv, err := NewServer(ctx, StdioConfig{
		Name:    "mock",
		Command: cmd,
		Args:    args,
		Env:     env,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	// Tighten timeouts so a hung mock doesn't slow the suite.
	srv.Timeouts = Timeouts{Initialize: 5 * time.Second, ListTools: 5 * time.Second, CallTool: 5 * time.Second}

	if err := srv.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if srv.Info().Name != "mock-server" {
		t.Errorf("server info: got %+v", srv.Info())
	}

	tools, err := srv.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools: got %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names["echo"] || !names["fail"] {
		t.Errorf("missing expected tools: got %v", names)
	}
}

// TestServerCallToolSuccess: echo tool returns the arg unchanged.
func TestServerCallToolSuccess(t *testing.T) {
	defer resetStderr(silenceStderr(t))
	srv := mustBootMock(t)
	defer srv.Close()

	ctx := context.Background()
	out, err := srv.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi there"}`))
	if err != nil {
		t.Fatalf("CallTool echo: %v", err)
	}
	if out != "echo: hi there" {
		t.Errorf("echo output: got %q", out)
	}
}

// TestServerCallToolError: fail tool returns isError=true + content. The
// client surfaces BOTH the joined content AND an error (matching the
// runtime.RunTurn contract).
func TestServerCallToolError(t *testing.T) {
	defer resetStderr(silenceStderr(t))
	srv := mustBootMock(t)
	defer srv.Close()

	ctx := context.Background()
	out, err := srv.CallTool(ctx, "fail", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for fail tool")
	}
	if !strings.Contains(out, "something broke") {
		t.Errorf("output should include content: got %q", out)
	}
}

// TestToolAdapter: wraps an MCP tool and invokes Execute through the
// runtime.Tool surface.
func TestToolAdapter(t *testing.T) {
	defer resetStderr(silenceStderr(t))
	srv := mustBootMock(t)
	defer srv.Close()

	tl := NewTool(srv, ToolDef{
		Name:        "echo",
		Description: "Echoes input.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if tl.Spec().Name != "mcp__mock__echo" {
		t.Errorf("spec name: got %q", tl.Spec().Name)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"msg":"via adapter"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "echo: via adapter" {
		t.Errorf("adapter output: got %q", out)
	}
}

// TestBootstrapFromConfig: build a Config pointing at the mock, bootstrap,
// and verify the Registry exposes 2 tools.
func TestBootstrapFromConfig(t *testing.T) {
	defer resetStderr(silenceStderr(t))

	cmd, args, env := spawnMockArgs()
	// Config env field can't carry arbitrary parent env easily — we write
	// a parent-env + mock marker manually and pass an env-less Config.
	// The registry helper will merge parent env via os.Environ().
	_ = env

	// For the registry we rely on the subprocess to pick up the
	// GO_TEST_MCP_MOCK variable. Inject it via the parent env for this test.
	t.Setenv("GO_TEST_MCP_MOCK", "1")

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"mock": {Command: cmd, Args: args},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reg := BootstrapFromConfig(ctx, cfg)
	defer reg.Close()

	if len(reg.Servers) != 1 {
		t.Fatalf("servers: got %d, want 1", len(reg.Servers))
	}
	if len(reg.Tools) != 2 {
		t.Fatalf("tools: got %d, want 2", len(reg.Tools))
	}
	// Names should be namespaced.
	for _, tl := range reg.Tools {
		if !strings.HasPrefix(tl.Spec().Name, "mcp__mock__") {
			t.Errorf("namespacing missing: %q", tl.Spec().Name)
		}
	}
}

// mustBootMock is a test helper: spawn, init, return.
func mustBootMock(t *testing.T) *Server {
	t.Helper()
	cmd, args, env := spawnMockArgs()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// Caller is expected to cancel via srv.Close + its own deadline; we
	// leak the cancel if we don't store it. Wire it to t.Cleanup.
	t.Cleanup(cancel)
	srv, err := NewServer(ctx, StdioConfig{Name: "mock", Command: cmd, Args: args, Env: env})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Timeouts = Timeouts{Initialize: 5 * time.Second, ListTools: 5 * time.Second, CallTool: 5 * time.Second}
	if err := srv.Initialize(ctx); err != nil {
		srv.Close()
		t.Fatalf("Initialize: %v", err)
	}
	return srv
}
