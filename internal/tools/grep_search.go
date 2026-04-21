package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// grep_search tool — regex search across files.
//
// Input:
//   {
//     "pattern":       string  (required, Go RE2 regex),
//     "path":          string  (optional, base directory; default cwd),
//     "include":       string  (optional, glob to filter file paths; default all),
//     "case_insensitive": boolean (optional),
//     "context":       integer (optional, lines of context before/after; default 0),
//     "max_matches":   integer (optional, cap on matches; default 200)
//   }
//
// Output: one block per matching file, each block headed by the path and
// followed by (optionally context-prefixed) line matches.

const grepSearchSchema = `{
  "type": "object",
  "properties": {
    "pattern":          {"type": "string", "description": "Go RE2 regex pattern."},
    "path":             {"type": "string", "description": "Base directory. Default: current working directory."},
    "include":          {"type": "string", "description": "Optional glob to filter which files to search, e.g. \"**/*.go\"."},
    "case_insensitive": {"type": "boolean", "description": "Case-insensitive match."},
    "context":          {"type": "integer", "description": "Lines of context before and after each match (default 0)."},
    "max_matches":      {"type": "integer", "description": "Cap on total matches (default 200)."}
  },
  "required": ["pattern"]
}`

const grepSearchDescription = "Search for a regex pattern across files in a directory tree. Returns matching lines with optional before/after context. Skips binary files and noise directories (.git, node_modules, vendor, etc.). Use `include` to narrow the file set by glob."

type grepSearchInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Include         string `json:"include,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	Context         int    `json:"context,omitempty"`
	MaxMatches      int    `json:"max_matches,omitempty"`
}

type GrepSearchTool struct{ spec runtime.ToolSpec }

type lineHit struct {
	LineNo int
	Line   string
}

type fileHit struct {
	Path  string
	Lines []lineHit
}

func NewGrepSearch() *GrepSearchTool {
	return &GrepSearchTool{spec: runtime.ToolSpec{
		Name:        "grep_search",
		Description: grepSearchDescription,
		InputSchema: json.RawMessage(grepSearchSchema),
	}}
}

func (t *GrepSearchTool) Spec() runtime.ToolSpec { return t.spec }

func (t *GrepSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", fmt.Errorf("grep_search: empty input")
	}
	var in grepSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("grep_search: parse input: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("grep_search: pattern is required")
	}
	if in.MaxMatches <= 0 {
		in.MaxMatches = 200
	}
	if in.Context < 0 {
		in.Context = 0
	}

	pat := in.Pattern
	if in.CaseInsensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return "", fmt.Errorf("grep_search: compile pattern: %w", err)
	}

	base := in.Path
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("grep_search: getwd: %w", err)
		}
		base = cwd
	} else if !filepath.IsAbs(base) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("grep_search: getwd: %w", err)
		}
		base = filepath.Join(cwd, base)
	}
	base = filepath.Clean(base)

	results := map[string]*fileHit{}
	var order []string // insertion order
	totalMatches := 0
	truncated := false

	err = filepath.WalkDir(base, func(path string, d os.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if truncated {
			return filepath.SkipAll
		}
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if path == base {
				return nil
			}
			if excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Apply include glob if given.
		if in.Include != "" {
			rel, _ := filepath.Rel(base, path)
			if !matchGlob(in.Include, filepath.ToSlash(rel)) {
				return nil
			}
		}
		// Skip files that are obviously not text. Cheap heuristic on extension
		// first (no stat), then peek the first few KB.
		if isLikelyBinaryExt(path) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > fileMaxBytes {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		peek := make([]byte, 4096)
		n, _ := f.Read(peek)
		if isBinaryBytes(peek[:n]) {
			return nil
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if scanner.Err() != nil {
			return nil
		}

		var localHits []lineHit
		for i, line := range lines {
			if re.MatchString(line) {
				localHits = append(localHits, lineHit{LineNo: i + 1, Line: line})
				totalMatches++
				if totalMatches >= in.MaxMatches {
					truncated = true
					break
				}
			}
		}
		if len(localHits) > 0 {
			fh, ok := results[path]
			if !ok {
				fh = &fileHit{Path: path}
				results[path] = fh
				order = append(order, path)
			}
			// Expand with context if requested.
			if in.Context > 0 {
				fh.Lines = append(fh.Lines, expandContext(lines, localHits, in.Context)...)
			} else {
				fh.Lines = append(fh.Lines, localHits...)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("grep_search: walk: %w", err)
	}

	sort.Strings(order)
	var b strings.Builder
	fmt.Fprintf(&b, "grep %q in %s: %d match(es) across %d file(s)", in.Pattern, base, totalMatches, len(order))
	if truncated {
		fmt.Fprintf(&b, " (truncated at max_matches=%d)", in.MaxMatches)
	}
	b.WriteByte('\n')
	for _, p := range order {
		fh := results[p]
		fmt.Fprintf(&b, "\n%s\n", fh.Path)
		for _, lh := range fh.Lines {
			fmt.Fprintf(&b, "  %d: %s\n", lh.LineNo, lh.Line)
		}
	}
	return b.String(), nil
}

// expandContext returns the merged set of hit lines + N lines of context
// before/after each hit, deduplicated.
func expandContext(allLines []string, hits []lineHit, ctx int) []lineHit {
	if ctx <= 0 || len(hits) == 0 {
		return hits
	}
	want := map[int]bool{}
	for _, h := range hits {
		for d := -ctx; d <= ctx; d++ {
			ln := h.LineNo + d
			if ln >= 1 && ln <= len(allLines) {
				want[ln] = true
			}
		}
	}
	lineNums := make([]int, 0, len(want))
	for ln := range want {
		lineNums = append(lineNums, ln)
	}
	sort.Ints(lineNums)
	out := make([]lineHit, 0, len(lineNums))
	for _, ln := range lineNums {
		out = append(out, lineHit{LineNo: ln, Line: allLines[ln-1]})
	}
	return out
}

// isLikelyBinaryExt is a cheap pre-filter to skip obvious non-text files
// before stat/open. Not a security boundary.
func isLikelyBinaryExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp", ".tiff",
		".pdf", ".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar",
		".mp3", ".mp4", ".mov", ".avi", ".mkv", ".webm", ".flac", ".wav",
		".so", ".dylib", ".dll", ".a", ".o", ".exe", ".class", ".jar",
		".pyc", ".pyo", ".wasm", ".bin":
		return true
	}
	return false
}
