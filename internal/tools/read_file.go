package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// read_file tool — reads a UTF-8 text file with optional line range.
//
// Input schema:
//   {
//     "path":       string (required),
//     "start_line": integer (optional, 1-based, inclusive),
//     "end_line":   integer (optional, 1-based, inclusive; -1 = EOF)
//   }
//
// Output (plain text): line-numbered file content. When the file is
// truncated (binary, too large), an explicit marker replaces the content
// so the model doesn't hallucinate bytes it can't see.

const readFileSchema = `{
  "type": "object",
  "properties": {
    "path":       {"type": "string", "description": "Absolute or cwd-relative path."},
    "start_line": {"type": "integer", "description": "First line to include (1-based, inclusive). Default 1."},
    "end_line":   {"type": "integer", "description": "Last line to include (1-based, inclusive). -1 means EOF. Default EOF."}
  },
  "required": ["path"]
}`

const readFileDescription = "Read the contents of a text file, optionally restricted to a line range. Returns line-numbered content. Binary files and files larger than 10 MiB are rejected."

type readFileInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

// ReadFileTool implements runtime.Tool.
type ReadFileTool struct{ spec runtime.ToolSpec }

func NewReadFile() *ReadFileTool {
	return &ReadFileTool{spec: runtime.ToolSpec{
		Name:        "read_file",
		Description: readFileDescription,
		InputSchema: json.RawMessage(readFileSchema),
	}}
}

func (t *ReadFileTool) Spec() runtime.ToolSpec { return t.spec }

// Capability classifies read_file as a pure read.
func (t *ReadFileTool) Capability() runtime.Capability { return runtime.CapRead }

func (t *ReadFileTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", errFileEmptyPath
	}
	var in readFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("read_file: parse input: %w", err)
	}
	path, err := resolveFilePath(in.Path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file: stat: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file: %w", errFileIsDirectory)
	}
	if info.Size() > fileMaxBytes {
		return "", fmt.Errorf("read_file: %w (size=%d)", errFileTooLarge, info.Size())
	}

	// Peek first chunk to detect binary before loading the whole file.
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read_file: open: %w", err)
	}
	defer f.Close()

	peek := make([]byte, 8192)
	n, _ := f.Read(peek)
	if isBinaryBytes(peek[:n]) {
		return "", fmt.Errorf("read_file: %w (peeked %d bytes)", errFileIsBinary, n)
	}
	// Rewind.
	if _, err := f.Seek(0, 0); err != nil {
		return "", fmt.Errorf("read_file: seek: %w", err)
	}

	// Read all lines.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), fileMaxBytes)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read_file: scan: %w", err)
	}

	total := len(lines)
	start := in.StartLine
	if start <= 0 {
		start = 1
	}
	end := in.EndLine
	if end == 0 || end == -1 || end > total {
		end = total
	}
	if start > total {
		return fmt.Sprintf("%s\n(file has %d lines; requested start_line=%d is past EOF)\n", path, total, start), nil
	}
	if start > end {
		return "", fmt.Errorf("read_file: start_line (%d) > end_line (%d)", start, end)
	}

	// Format: <path> (<N> lines)  +  per-line "  nnn→content".
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d lines total", path, total)
	if start != 1 || end != total {
		fmt.Fprintf(&b, ", showing %d–%d", start, end)
	}
	b.WriteString(")\n")
	width := numDigits(end)
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%*d\t%s\n", width, i+1, lines[i])
	}
	return b.String(), nil
}

func numDigits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		n /= 10
		d++
	}
	return d
}
