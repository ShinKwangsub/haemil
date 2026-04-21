// Command haemil is the Phase 2 CLI entry point for the Haemil integrated
// AI agent engine. See analysis/integration/skeleton.md for the full design.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ShinKwangsub/haemil/internal/cli"
	"github.com/ShinKwangsub/haemil/internal/provider"
)

func main() {
	var (
		providerName  = flag.String("provider", "anthropic", "LLM provider: anthropic | openai | omlx")
		model         = flag.String("model", "", "LLM model identifier (default depends on provider)")
		endpoint      = flag.String("endpoint", "", "override provider base URL (e.g. http://127.0.0.1:8080 for oMLX)")
		maxIterations  = flag.Int("max-iterations", 10, "max tool-use rounds per turn")
		sessionDir     = flag.String("session-dir", "", "session directory (default ~/.haemil/sessions)")
		resumeID       = flag.String("session", "", "resume an existing session by id")
		permissionMode = flag.String("permission-mode", "danger-full", "tool permission preset: readonly | workspace-write | danger-full")
		mcpConfigPath  = flag.String("mcp-config", "", "path to mcp.json (default ~/.haemil/mcp.json)")
		hooksPath      = flag.String("hooks", "", "path to hooks.json (default <cwd>/.haemil/hooks.json)")
	)
	flag.Parse()

	// "omlx" is a convenience alias that points the OpenAI-compat client at
	// the local gateway on 127.0.0.1:8080 with the gemma-4 default model.
	// It's not a distinct provider — it's just a shortcut for
	//   -provider openai -endpoint http://127.0.0.1:8080 -model gemma-4-...
	effectiveProvider := *providerName
	effectiveEndpoint := *endpoint
	effectiveModel := *model
	if *providerName == "omlx" {
		effectiveProvider = "openai"
		if effectiveEndpoint == "" {
			effectiveEndpoint = "http://127.0.0.1:8080/v1"
		}
		if effectiveModel == "" {
			effectiveModel = "gemma-4-26b-a4b-it-8bit"
		}
	}

	// Per-provider default models if the user didn't set one.
	if effectiveModel == "" {
		switch effectiveProvider {
		case "anthropic":
			effectiveModel = "claude-sonnet-4-6"
		case "openai":
			effectiveModel = "gpt-4o-mini"
		}
	}

	// API key loading: each provider has its own env var. Local servers may
	// not need any.
	var apiKey, keyEnvName string
	switch effectiveProvider {
	case "anthropic":
		keyEnvName = "ANTHROPIC_API_KEY"
		apiKey = os.Getenv(keyEnvName)
	case "openai":
		keyEnvName = "OPENAI_API_KEY"
		apiKey = os.Getenv(keyEnvName)
	}

	// Warn if a cloud endpoint is expected but no key is set. Skip the
	// warning for clearly local endpoints.
	isLocal := strings.Contains(effectiveEndpoint, "127.0.0.1") ||
		strings.Contains(effectiveEndpoint, "localhost") ||
		strings.HasPrefix(effectiveEndpoint, "http://")
	if apiKey == "" && !isLocal {
		fmt.Fprintf(os.Stderr, "warning: %s not set — API calls will fail\n", keyEnvName)
	} else if apiKey != "" {
		fmt.Fprintf(os.Stderr, "using %s=%s\n", keyEnvName, provider.RedactAPIKey(apiKey))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := cli.Config{
		ProviderName:   effectiveProvider,
		APIKey:         apiKey,
		Model:          effectiveModel,
		Endpoint:       effectiveEndpoint,
		MaxIterations:  *maxIterations,
		SessionDir:     *sessionDir,
		ResumeID:       *resumeID,
		PermissionMode: *permissionMode,
		MCPConfigPath:  *mcpConfigPath,
		HooksPath:      *hooksPath,
	}

	if err := cli.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "haemil: %v\n", err)
		os.Exit(1)
	}
}
