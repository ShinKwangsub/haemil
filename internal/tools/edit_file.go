package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// edit_file tool — exact find-and-replace on an existing file.
//
// Input:
//   {
//     "path":        string  (required),
//     "old_string":  string  (required, exact match),
//     "new_string":  string  (required),
//     "replace_all": boolean (optional, default false)
//   }
//
// Safety: when replace_all=false (default), old_string MUST appear exactly
// once. Multiple matches return an error. This is claw-code's guard against
// ambiguous edits — same behavior as Claude Code's Edit tool.

const editFileSchema = `{
  "type": "object",
  "properties": {
    "path":        {"type": "string", "description": "Absolute or cwd-relative path to an existing text file."},
    "old_string":  {"type": "string", "description": "Exact substring to replace. Must match literally (whitespace and all)."},
    "new_string":  {"type": "string", "description": "Replacement text."},
    "replace_all": {"type": "boolean", "description": "If true, replace every occurrence. If false (default), old_string must appear exactly once."}
  },
  "required": ["path", "old_string", "new_string"]
}`

const editFileDescription = "Replace an exact substring inside an existing text file. By default fails if old_string matches more than once (safety); pass replace_all:true to allow multi-match. Binary files and files >10 MiB are rejected."

type editFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type EditFileTool struct{ spec runtime.ToolSpec }

func NewEditFile() *EditFileTool {
	return &EditFileTool{spec: runtime.ToolSpec{
		Name:        "edit_file",
		Description: editFileDescription,
		InputSchema: json.RawMessage(editFileSchema),
	}}
}

func (t *EditFileTool) Spec() runtime.ToolSpec { return t.spec }

// Capability classifies edit_file as a workspace write.
func (t *EditFileTool) Capability() runtime.Capability { return runtime.CapWrite }

func (t *EditFileTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", errFileEmptyPath
	}
	var in editFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("edit_file: parse input: %w", err)
	}
	if in.OldString == "" {
		return "", fmt.Errorf("edit_file: old_string is required and must be non-empty")
	}
	if in.OldString == in.NewString {
		return "", fmt.Errorf("edit_file: old_string and new_string are identical — nothing to do")
	}
	path, err := resolveFilePath(in.Path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: stat: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("edit_file: %w", errFileIsDirectory)
	}
	if info.Size() > fileMaxBytes {
		return "", fmt.Errorf("edit_file: %w (size=%d)", errFileTooLarge, info.Size())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: read: %w", err)
	}
	if isBinaryBytes(raw) {
		return "", fmt.Errorf("edit_file: %w", errFileIsBinary)
	}
	content := string(raw)

	count := strings.Count(content, in.OldString)
	if count == 0 {
		return "", fmt.Errorf("edit_file: old_string not found in %s", path)
	}
	if count > 1 && !in.ReplaceAll {
		return "", fmt.Errorf("edit_file: old_string appears %d times in %s — pass replace_all:true to edit all, or include more context to make it unique", count, path)
	}

	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(content, in.OldString, in.NewString)
	} else {
		updated = strings.Replace(content, in.OldString, in.NewString, 1)
	}

	// Preserve original file mode.
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("edit_file: write: %w", err)
	}

	replaced := 1
	if in.ReplaceAll {
		replaced = count
	}
	diffLines := len(strings.Split(updated, "\n")) - len(strings.Split(content, "\n"))
	return fmt.Sprintf("edited %s: replaced %d occurrence(s) (%+d lines)", path, replaced, diffLines), nil
}
