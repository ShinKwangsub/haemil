package mcp

import (
	"io"
	"os"
)

// stderrOf returns the writer MCP subprocesses should inherit for their
// stderr. Defaulting to os.Stderr makes server diagnostics visible to the
// operator; tests can override via SetStderr.
var stderrSink io.Writer = os.Stderr

// SetStderr replaces the writer that newly-spawned MCP subprocesses use
// for stderr. Intended for tests; safe to call concurrently with no
// live clients (there is no mutex — don't race with an in-flight spawn).
func SetStderr(w io.Writer) { stderrSink = w }

func stderrOf() io.Writer { return stderrSink }
