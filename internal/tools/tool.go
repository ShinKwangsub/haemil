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
// mode is the active PermissionMode — passed to bash so its validation
// pipeline (bash_validation.go) can enforce mode-aware rules. workspace
// is the resolved absolute path of the workspace root; pass "" if
// unknown. Both are captured at construction time, so callers must
// rebuild the tool set when mode or workspace change.
//
// The call is cheap — each constructor does no I/O — so cli.Run can call
// it unconditionally during wiring.
func Default(mode runtime.PermissionMode, workspace string) []runtime.Tool {
	return []runtime.Tool{
		NewBash(mode, workspace),
		NewReadFile(),
		NewWriteFile(),
		NewEditFile(),
		NewGlobSearch(),
		NewGrepSearch(),
	}
}
