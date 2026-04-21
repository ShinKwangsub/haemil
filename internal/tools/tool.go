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
// default. Phase 2 shipped with bash; Phase 3 C1 adds file_ops
// (read/write/edit/glob/grep). Later cycles add MCP-sourced tools.
//
// The call is cheap — each constructor does no I/O — so cli.Run can call
// it unconditionally during wiring.
func Default() []runtime.Tool {
	return []runtime.Tool{
		NewBash(),
		NewReadFile(),
		NewWriteFile(),
		NewEditFile(),
		NewGlobSearch(),
		NewGrepSearch(),
	}
}
