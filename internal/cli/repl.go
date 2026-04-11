// Package cli wires the runtime, provider, and tools together and hosts the
// REPL loop. Phase 2 is wiring-only — the actual input loop is deferred to
// Phase 2b (see runtime/conversation.go and skeleton.md).
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ShinKwangsub/haemil/internal/provider"
	"github.com/ShinKwangsub/haemil/internal/runtime"
	"github.com/ShinKwangsub/haemil/internal/tools"
)

// Config carries the knobs cmd/haemil/main.go parsed from flags and env.
type Config struct {
	ProviderName  string // e.g. "anthropic"
	APIKey        string // raw key, already loaded from env — may be empty
	Model         string // e.g. "claude-sonnet-4-6"
	MaxIterations int    // cap on tool loop rounds
	SessionDir    string // where JSONL session files live
	ResumeID      string // if non-empty, OpenSession instead of NewSession
}

// Run wires up provider + session + runtime + tools and hands control to
// the REPL loop. Phase 2: the REPL is a stub that prints
// "haemil skeleton ready — REPL not yet implemented" and exits cleanly,
// so we can verify end-to-end wiring (including defer Close) without
// implementing the input loop.
//
// Every constructor called here is REAL (no panic TODOs) — that's the
// whole point of the "skeleton ready" smoke test. If any of these panics
// the skeleton is broken and the test line below is never reached.
func Run(ctx context.Context, cfg Config) error {
	// 1. Provider: real factory, real http.Client wiring, no network I/O.
	p, err := provider.New(cfg.ProviderName, cfg.APIKey, cfg.Model)
	if err != nil {
		return fmt.Errorf("cli: provider: %w", err)
	}

	// 2. Session: real MkdirAll + OpenFile, creates ~/.haemil/sessions/<id>.jsonl.
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
	// defer Close covers both the happy path and any error path below.
	defer func() {
		if cerr := session.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "warning: closing session: %v\n", cerr)
		}
	}()

	// 3. Tools: registry is a plain slice, cheap to build.
	toolList := tools.Default()

	// 4. Runtime: pure field assignment + tool name lookup map.
	rt := runtime.New(p, toolList, session, runtime.Options{
		Model:         cfg.Model,
		MaxIterations: cfg.MaxIterations,
		SystemPrompt:  "You are Haemil, an AI business partner. This is a Phase 2 skeleton — not yet wired for real conversations.",
		MaxTokens:     4096,
	})

	// Smoke-test anchor: if we reach this line, every constructor above
	// executed without panic. The session file exists on disk. The REPL
	// input loop and the provider.Chat body are still stubs (Phase 2b),
	// but the wiring is end-to-end verified.
	fmt.Println("haemil skeleton ready — REPL not yet implemented")
	_ = rt // silence "declared and not used" if future edits remove usage

	// 5. Future: actual input loop here.
	//    for {
	//        line, err := readLine(...)
	//        summary, err := rt.RunTurn(ctx, line)
	//        render(summary)
	//    }
	return nil
}
