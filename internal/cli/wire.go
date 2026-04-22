package cli

import (
	"context"
	"fmt"

	"github.com/ShinKwangsub/haemil/internal/hooks"
	"github.com/ShinKwangsub/haemil/internal/mcp"
	"github.com/ShinKwangsub/haemil/internal/memory"
	"github.com/ShinKwangsub/haemil/internal/provider"
	"github.com/ShinKwangsub/haemil/internal/runtime"
	"github.com/ShinKwangsub/haemil/internal/tools"
)

// BuildResult bundles the wired components produced by BuildRuntime so
// multiple entry points (CLI REPL, HTTP server, future RPC) can share
// the same construction logic without each one re-implementing the 8
// wiring steps.
type BuildResult struct {
	Runtime     *runtime.Runtime
	Tenant      runtime.TenantContext
	Mode        runtime.PermissionMode
	MCPRegistry *mcp.Registry
	HooksRunner *hooks.Runner
	HooksConfig *hooks.Config
	HooksPath   string
}

// BuildRuntime performs the full wiring dance (tenant → provider →
// session → policy → tools → mcp → hooks → memory → runtime) and
// returns the pieces plus a cleanup func. Cleanup closes MCP child
// processes; it does NOT close the Session — callers decide session
// ownership (CLI defers session.Close(); server mode hands the session
// to a Supervisor which closes it on Unregister).
//
// cfg.Stderr is used for non-fatal warnings (missing mcp/hooks configs,
// memory read errors). cfg.Stdin/Stdout are only touched by the REPL
// loop, not by BuildRuntime.
func BuildRuntime(ctx context.Context, cfg Config) (*BuildResult, func(), error) {
	tenant, err := runtime.ResolveTenant(cfg.Workspace, cfg.HomeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cli: tenant: %w", err)
	}
	tenant.ID = cfg.TenantID

	p, err := provider.New(cfg.ProviderName, cfg.APIKey, cfg.Model, provider.Options{
		Endpoint: cfg.Endpoint,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cli: provider: %w", err)
	}

	if cfg.SessionDir == "" {
		cfg.SessionDir = tenant.SessionDir()
	}
	var session *runtime.Session
	if cfg.ResumeID != "" {
		session, err = runtime.OpenSession(cfg.SessionDir, cfg.ResumeID)
	} else {
		session, err = runtime.NewSession(cfg.SessionDir)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cli: session: %w", err)
	}

	modeStr := cfg.PermissionMode
	if modeStr == "" {
		modeStr = "danger-full"
	}
	mode, err := runtime.ParseMode(modeStr)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("cli: permission-mode: %w", err)
	}
	policy := runtime.NewPolicy(mode, nil)

	toolList := tools.Default(mode, tenant.Workspace)

	mcpPath := cfg.MCPConfigPath
	if mcpPath == "" {
		mcpPath = tenant.MCPConfigPath()
	}
	mcpCfg, mcpErr := mcp.LoadConfig(mcpPath)
	if mcpErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: mcp config: %v\n", mcpErr)
	}
	mcpReg := mcp.BootstrapFromConfig(ctx, mcpCfg)
	if len(mcpReg.Tools) > 0 {
		toolList = append(toolList, mcpReg.Tools...)
	}

	hooksPath := cfg.HooksPath
	if hooksPath == "" {
		hooksPath = tenant.HooksConfigPath()
	}
	hooksCfg, hooksErr := hooks.LoadConfig(hooksPath)
	if hooksErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: hooks config: %v\n", hooksErr)
	}
	hookRunner := hooks.NewRunner(hooksCfg)

	memCtx := memory.NewContextFor(tenant)
	memBlock, memErr := memCtx.Render()
	if memErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: memory: %v\n", memErr)
	}
	effectiveSystem := systemPrompt
	if memBlock != "" {
		effectiveSystem = systemPrompt + "\n\n" + memBlock
	}

	rt := runtime.New(p, toolList, session, runtime.Options{
		Model:         cfg.Model,
		MaxIterations: cfg.MaxIterations,
		SystemPrompt:  effectiveSystem,
		MaxTokens:     4096,
		Policy:        policy,
		Hooks:         hookRunner,
	})

	cleanup := func() {
		mcpReg.Close()
	}

	return &BuildResult{
		Runtime:     rt,
		Tenant:      tenant,
		Mode:        mode,
		MCPRegistry: mcpReg,
		HooksRunner: hookRunner,
		HooksConfig: hooksCfg,
		HooksPath:   hooksPath,
	}, cleanup, nil
}
