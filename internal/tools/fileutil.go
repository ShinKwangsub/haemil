package tools

import (
	"bytes"
	"os"
	"path/filepath"
)

// fileMaxBytes caps file read/write sizes at 10 MiB. Same limit as claw-code
// analysis/platforms/claw-code.md §3.3. Larger files would blow up tool
// outputs and the model's context window.
const fileMaxBytes = 10 * 1024 * 1024

// isBinaryBytes returns true if the first 8 KiB of content looks like a
// binary file. Heuristic: a NUL byte in the sample, or >30% of bytes are
// non-printable ASCII. This mirrors what `file`/`grep` do in practice.
func isBinaryBytes(b []byte) bool {
	sample := b
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if len(sample) == 0 {
		return false
	}
	nonPrint := 0
	for _, c := range sample {
		switch {
		case c == '\t' || c == '\n' || c == '\r':
			// whitespace — printable
		case c < 0x20 || c == 0x7f:
			nonPrint++
		}
	}
	return nonPrint*10 > len(sample)*3 // >30%
}

// resolveFilePath normalizes a user-supplied path. Absolute paths are used
// verbatim; relative paths are resolved against cwd. The caller is expected
// to apply any sandbox/permission checks — this helper only handles the
// relative→absolute rewrite.
func resolveFilePath(p string) (string, error) {
	if p == "" {
		return "", errFileEmptyPath
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(cwd, p)), nil
}

// errFileEmptyPath is returned when a tool is called with no path.
type toolErr string

func (e toolErr) Error() string { return string(e) }

const (
	errFileEmptyPath   = toolErr("file: path is required")
	errFileTooLarge    = toolErr("file: content exceeds 10 MiB cap")
	errFileIsBinary    = toolErr("file: binary content not supported (use bash for hex dump)")
	errFileIsDirectory = toolErr("file: path is a directory, not a file")
)
