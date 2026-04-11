// Package tools contains concrete runtime.Tool implementations.
//
// Each tool implements runtime.Tool (declared in internal/runtime, following
// the "consumer defines the interface" Go idiom). Tools may import runtime
// but MUST NOT import the provider package.
package tools

import (
	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// Default returns the tools that are registered with every Runtime by
// default. Phase 2 ships with bash only. Phase 3 will add file_ops, grep,
// glob, and eventually MCP-sourced tools (see analysis/platforms/goose.md).
//
// The call is cheap — each tool's constructor does no I/O — so cli.Run can
// call it unconditionally during wiring.
func Default() []runtime.Tool {
	return []runtime.Tool{
		NewBash(),
	}
}
