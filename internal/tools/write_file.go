package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// write_file tool — creates or overwrites a file with the given content.
//
// Input:
//   {
//     "path":    string (required),
//     "content": string (required, UTF-8),
//     "mkdir":   boolean (optional, default true — create parent dirs)
//   }
//
// 10 MiB cap applies to content. Parent directories are created with 0755
// unless mkdir=false. File mode is 0644.

const writeFileSchema = `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "Absolute or cwd-relative path."},
    "content": {"type": "string", "description": "File content. UTF-8."},
    "mkdir":   {"type": "boolean", "description": "Create parent directories if missing. Default true."}
  },
  "required": ["path", "content"]
}`

const writeFileDescription = "Write (create or overwrite) a UTF-8 text file with the given content. Parent directories are created automatically by default. Content is capped at 10 MiB."

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mkdir   *bool  `json:"mkdir,omitempty"` // pointer so we can distinguish absent from false
}

type WriteFileTool struct{ spec runtime.ToolSpec }

func NewWriteFile() *WriteFileTool {
	return &WriteFileTool{spec: runtime.ToolSpec{
		Name:        "write_file",
		Description: writeFileDescription,
		InputSchema: json.RawMessage(writeFileSchema),
	}}
}

func (t *WriteFileTool) Spec() runtime.ToolSpec { return t.spec }

// Capability classifies write_file as a workspace write.
func (t *WriteFileTool) Capability() runtime.Capability { return runtime.CapWrite }

func (t *WriteFileTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", errFileEmptyPath
	}
	var in writeFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("write_file: parse input: %w", err)
	}
	if len(in.Content) > fileMaxBytes {
		return "", fmt.Errorf("write_file: %w (content_size=%d)", errFileTooLarge, len(in.Content))
	}
	path, err := resolveFilePath(in.Path)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}

	// If path already exists as a directory, refuse.
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return "", fmt.Errorf("write_file: %w", errFileIsDirectory)
	}

	// Create parent dirs if requested (default true).
	shouldMkdir := in.Mkdir == nil || *in.Mkdir
	if shouldMkdir {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("write_file: mkdir: %w", err)
		}
	}

	// Record prior size for the diff-stat.
	var priorSize int64 = -1
	if info, err := os.Stat(path); err == nil {
		priorSize = info.Size()
	}

	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: write: %w", err)
	}

	verb := "created"
	if priorSize >= 0 {
		verb = "overwrote"
	}
	lines := countLines(in.Content)
	return fmt.Sprintf("%s %s (%d lines, %d bytes)", verb, path, lines, len(in.Content)), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	// If the content ends with a newline, the last "line" is empty — don't count it.
	if s[len(s)-1] == '\n' {
		n--
	}
	return n
}
