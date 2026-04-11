// Command haemil is the Phase 2 skeleton CLI entry point for the Haemil
// integrated AI agent engine. See analysis/integration/skeleton.md for the
// full design.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ShinKwangsub/haemil/internal/cli"
	"github.com/ShinKwangsub/haemil/internal/provider"
)

func main() {
	var (
		model         = flag.String("model", "claude-sonnet-4-6", "LLM model identifier")
		maxIterations = flag.Int("max-iterations", 10, "max tool-use rounds per turn")
		sessionDir    = flag.String("session-dir", "", "session directory (default ~/.haemil/sessions)")
		resumeID      = flag.String("session", "", "resume an existing session by id")
	)
	flag.Parse()

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "warning: ANTHROPIC_API_KEY not set — API calls will fail (skeleton stage does not invoke them)")
	} else {
		// Redacted — never log the raw key. See provider.RedactAPIKey policy.
		fmt.Fprintf(os.Stderr, "using ANTHROPIC_API_KEY=%s\n", provider.RedactAPIKey(apiKey))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := cli.Config{
		ProviderName:  "anthropic",
		APIKey:        apiKey,
		Model:         *model,
		MaxIterations: *maxIterations,
		SessionDir:    *sessionDir,
		ResumeID:      *resumeID,
	}

	if err := cli.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "haemil: %v\n", err)
		os.Exit(1)
	}
}
