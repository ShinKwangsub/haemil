// Package cli wires the runtime, provider, and tools together and hosts the
// REPL loop.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/mcp"
	"github.com/ShinKwangsub/haemil/internal/provider"
	"github.com/ShinKwangsub/haemil/internal/runtime"
	"github.com/ShinKwangsub/haemil/internal/tools"
)

// Config carries the knobs cmd/haemil/main.go parsed from flags and env.
type Config struct {
	ProviderName   string // e.g. "anthropic" or "openai"
	APIKey         string // raw key, already loaded from env — may be empty
	Model          string // e.g. "claude-sonnet-4-6" or "gemma-4-26b-a4b-it-8bit"
	Endpoint       string // override provider base URL (e.g. http://127.0.0.1:8080 for oMLX)
	MaxIterations  int    // cap on tool loop rounds
	SessionDir     string // where JSONL session files live
	ResumeID       string // if non-empty, OpenSession instead of NewSession
	PermissionMode string // "readonly" | "workspace-write" | "danger-full" (default danger-full)
	MCPConfigPath  string // path to mcp.json; empty = default (~/.haemil/mcp.json)

	// Stdin / Stdout / Stderr allow tests to inject. If nil, os.Stdin/out/err.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run wires up provider + session + runtime + tools and runs the REPL loop.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}

	// 1. Provider.
	p, err := provider.New(cfg.ProviderName, cfg.APIKey, cfg.Model, provider.Options{
		Endpoint: cfg.Endpoint,
	})
	if err != nil {
		return fmt.Errorf("cli: provider: %w", err)
	}

	// 2. Session.
	if cfg.SessionDir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return fmt.Errorf("cli: home dir: %w", herr)
		}
		cfg.SessionDir = filepath.Join(home, ".haemil", "sessions")
	}
	var session *runtime.Session
	if cfg.ResumeID != "" {
		session, err = runtime.OpenSession(cfg.SessionDir, cfg.ResumeID)
	} else {
		session, err = runtime.NewSession(cfg.SessionDir)
	}
	if err != nil {
		return fmt.Errorf("cli: session: %w", err)
	}
	defer func() {
		if cerr := session.Close(); cerr != nil {
			fmt.Fprintf(cfg.Stderr, "warning: closing session: %v\n", cerr)
		}
	}()

	// 3. Policy (C2 권한 모드) — parsed first so we can wire it into tool
	// constructors (C3 bash_validation needs the active mode).
	modeStr := cfg.PermissionMode
	if modeStr == "" {
		modeStr = "danger-full"
	}
	mode, err := runtime.ParseMode(modeStr)
	if err != nil {
		return fmt.Errorf("cli: permission-mode: %w", err)
	}
	policy := runtime.NewPolicy(mode, nil)

	// 4. Tools — bash needs mode + workspace for its validation pipeline.
	workspace, _ := os.Getwd()
	toolList := tools.Default(mode, workspace)

	// 4b. MCP registry (C7). Failures on individual servers are logged and
	// skipped; a missing config file is fine. The registry's Close is
	// deferred to this function's return so long-lived servers live for
	// the lifetime of the REPL.
	mcpPath := cfg.MCPConfigPath
	if mcpPath == "" {
		mcpPath = mcp.DefaultConfigPath()
	}
	mcpCfg, mcpErr := mcp.LoadConfig(mcpPath)
	if mcpErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: mcp config: %v\n", mcpErr)
	}
	mcpReg := mcp.BootstrapFromConfig(ctx, mcpCfg)
	defer mcpReg.Close()
	if len(mcpReg.Tools) > 0 {
		toolList = append(toolList, mcpReg.Tools...)
	}

	// 5. Runtime.
	rt := runtime.New(p, toolList, session, runtime.Options{
		Model:         cfg.Model,
		MaxIterations: cfg.MaxIterations,
		SystemPrompt:  systemPrompt,
		MaxTokens:     4096,
		Policy:        policy,
	})

	// 6. Greeting + REPL.
	fmt.Fprintf(cfg.Stdout, "haemil — Phase 2b REPL (session %s, mode %s)\n", session.ID(), mode)
	if len(mcpReg.Servers) > 0 {
		fmt.Fprintf(cfg.Stdout, "mcp: %d server(s) connected, %d tool(s) registered\n",
			len(mcpReg.Servers), len(mcpReg.Tools))
	}
	fmt.Fprintln(cfg.Stdout, "type /exit to quit, /help for commands")
	fmt.Fprintln(cfg.Stdout)

	return runREPL(ctx, cfg, rt)
}

// systemPrompt is the fixed system prompt for Phase 2. Phase 3 will
// replace this with a dynamic prompt builder (claw-code pattern).
const systemPrompt = "You are Haemil, an AI assistant running as a Phase 2 skeleton CLI. " +
	"You have one tool: bash, which runs commands on the local machine. " +
	"Use it sparingly and explain what you're about to run before doing so. " +
	"Keep responses concise."

// runREPL is the interactive input loop. Read a line, run a turn, render,
// repeat. Exits cleanly on /exit, EOF, or ctx cancellation.
func runREPL(ctx context.Context, cfg Config, rt *runtime.Runtime) error {
	scanner := bufio.NewScanner(cfg.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // support long paste
	isTTY := isTerminal(cfg.Stdin)

	for {
		// Check ctx between turns.
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(cfg.Stdout)
			return nil
		}

		if isTTY {
			fmt.Fprint(cfg.Stdout, "you > ")
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("cli: stdin: %w", err)
			}
			// EOF — treat as graceful exit.
			fmt.Fprintln(cfg.Stdout)
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Slash commands handled locally. Only recognise /<word> shapes
		// (ASCII letters + optional digits/underscore/dash) so user input
		// like "/tmp/foo" or "/Users/..." is treated as a normal message.
		if isSlashCommand(line) {
			if done := handleSlash(cfg, rt, line); done {
				return nil
			}
			continue
		}

		// Real turn.
		summary, err := rt.RunTurn(ctx, line)
		if err != nil {
			// Partial renders first so user sees what was completed before the
			// failure, then the error itself.
			if summary != nil {
				renderSummary(cfg.Stdout, summary)
			}
			fmt.Fprintf(cfg.Stderr, "error: %v\n", err)
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		renderSummary(cfg.Stdout, summary)
	}
}

// isSlashCommand returns true if line starts with '/' followed by a bare
// single word (letters, digits, underscore, hyphen) and optional whitespace
// before any arguments. This distinguishes REPL commands like "/exit" from
// user input like "/tmp/foo" or "/Users/name/file.txt" that happens to
// start with a slash.
func isSlashCommand(line string) bool {
	if !strings.HasPrefix(line, "/") || len(line) < 2 {
		return false
	}
	for i, r := range line[1:] {
		if r == ' ' || r == '\t' {
			return i > 0
		}
		if !(r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// handleSlash returns true if the REPL should exit.
func handleSlash(cfg Config, rt *runtime.Runtime, line string) bool {
	switch line {
	case "/exit", "/quit":
		fmt.Fprintln(cfg.Stdout, "bye.")
		return true
	case "/help":
		fmt.Fprintln(cfg.Stdout, "commands:")
		fmt.Fprintln(cfg.Stdout, "  /exit     — quit")
		fmt.Fprintln(cfg.Stdout, "  /help     — this message")
		fmt.Fprintln(cfg.Stdout, "  /compact  — summarise older messages, preserve the recent tail")
		return false
	case "/compact":
		handleCompact(cfg, rt)
		return false
	default:
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", line)
		return false
	}
}

// handleCompact invokes runtime.Compact on the active session and persists
// the result. No-op (with a friendly note) if no session is wired or the
// current history is already below threshold.
func handleCompact(cfg Config, rt *runtime.Runtime) {
	sess := rt.Session()
	if sess == nil {
		fmt.Fprintln(cfg.Stdout, "compact: no session wired — nothing to compact.")
		return
	}
	before := sess.Messages()
	beforeTokens := runtime.EstimateSessionTokens(before)
	cfgC := runtime.DefaultCompactionConfig()
	result := runtime.Compact(before, cfgC)
	if result.RemovedCount == 0 {
		fmt.Fprintf(cfg.Stdout, "compact: skipped (%d messages, ~%d tokens — below threshold: preserve=%d, max=%d)\n",
			len(before), beforeTokens, cfgC.PreserveRecent, cfgC.MaxEstimatedTokens)
		return
	}
	if err := sess.ApplyCompaction(result); err != nil {
		fmt.Fprintf(cfg.Stderr, "compact: apply failed: %v\n", err)
		return
	}
	afterTokens := runtime.EstimateSessionTokens(result.Messages)
	fmt.Fprintf(cfg.Stdout, "compact: %d → %d messages, ~%d → ~%d tokens (removed %d)\n",
		len(before), len(result.Messages), beforeTokens, afterTokens, result.RemovedCount)
}

// renderSummary prints the assistant messages and tool call records in a
// simple way. Phase 3 will replace this with a richer renderer.
func renderSummary(w io.Writer, summary *runtime.TurnSummary) {
	if summary == nil {
		return
	}
	for i, msg := range summary.AssistantMessages {
		for _, block := range msg.Content {
			switch block.Type {
			case runtime.BlockTypeText:
				if strings.TrimSpace(block.Text) != "" {
					fmt.Fprintf(w, "haemil > %s\n", block.Text)
				}
			case runtime.BlockTypeToolUse:
				fmt.Fprintf(w, "  [tool] %s %s\n", block.Name, singleLine(string(block.Input), 100))
			}
		}
		// Show tool results that followed this assistant message.
		// (ToolCalls are in order, one-to-one with tool_use blocks across rounds.)
		if i < len(summary.AssistantMessages)-1 {
			// Between rounds, find the tool calls triggered by this message.
			// Simple approach: print all recorded tool calls once, after the
			// first assistant msg that triggered them. Phase 3 will align
			// properly; for Phase 2 we just print them all after the loop.
		}
	}
	for _, tc := range summary.ToolCalls {
		marker := "result"
		if tc.IsError {
			marker = "error"
		}
		fmt.Fprintf(w, "  [%s] %s\n", marker, singleLine(tc.Output, 400))
	}
	if summary.StopReason != "" && summary.StopReason != "end_turn" {
		fmt.Fprintf(w, "  (stop: %s, iters: %d, in: %d tok, out: %d tok)\n",
			summary.StopReason, summary.Iterations, summary.Usage.InputTokens, summary.Usage.OutputTokens)
	}
}

// singleLine collapses a string to a single line for compact display, with
// a length cap.
func singleLine(s string, max int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ⏎ ")
	if len(s) > max {
		s = s[:max] + " …"
	}
	return s
}

// isTerminal reports whether r is an interactive terminal (for the "you >"
// prompt). We only check os.Stdin; other readers (pipes, files, test
// buffers) return false.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
