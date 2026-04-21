package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// glob_search tool — list files matching a glob pattern.
//
// Input:
//   {
//     "pattern": string  (required, e.g. "**/*.go", "internal/**/*_test.go"),
//     "cwd":     string  (optional, base directory; default current directory),
//     "limit":   integer (optional, max results; default 200)
//   }
//
// Results are sorted by modification time, newest first. Directories like
// .git, node_modules, vendor are auto-excluded to keep output useful.

const globSearchSchema = `{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Glob pattern. ** matches any path segments recursively. Example: \"**/*.go\" or \"internal/**/*_test.go\"."},
    "cwd":     {"type": "string", "description": "Base directory. Default: current working directory."},
    "limit":   {"type": "integer", "description": "Max results (default 200)."}
  },
  "required": ["pattern"]
}`

const globSearchDescription = "Find files matching a glob pattern. Returns paths sorted by modification time (newest first). Auto-excludes .git/, node_modules/, vendor/, and other noise dirs."

type globSearchInput struct {
	Pattern string `json:"pattern"`
	Cwd     string `json:"cwd,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type GlobSearchTool struct{ spec runtime.ToolSpec }

func NewGlobSearch() *GlobSearchTool {
	return &GlobSearchTool{spec: runtime.ToolSpec{
		Name:        "glob_search",
		Description: globSearchDescription,
		InputSchema: json.RawMessage(globSearchSchema),
	}}
}

func (t *GlobSearchTool) Spec() runtime.ToolSpec { return t.spec }

// excludedDirs are skipped during walks. These are near-universally noise in
// search results — the caller can still glob inside them explicitly if needed.
var excludedDirs = map[string]bool{
	".git":          true,
	".svn":          true,
	".hg":           true,
	"node_modules":  true,
	"vendor":        true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".pytest_cache": true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	".idea":         true,
	".vscode":       true,
	// Haemil-specific: reference/ holds 7 cloned platforms (~1.6GB) that are
	// for analysis/reading only, never for grep/glob during agent operation.
	"reference":    true,
	// Haemil-specific: graphify-out/ is auto-generated knowledge-graph output.
	"graphify-out": true,
}

func (t *GlobSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		return "", fmt.Errorf("glob_search: empty input")
	}
	var in globSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("glob_search: parse input: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("glob_search: pattern is required")
	}
	if in.Limit <= 0 {
		in.Limit = 200
	}

	base := in.Cwd
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("glob_search: getwd: %w", err)
		}
		base = cwd
	} else if !filepath.IsAbs(base) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("glob_search: getwd: %w", err)
		}
		base = filepath.Join(cwd, base)
	}
	base = filepath.Clean(base)

	// We implement a simple doublestar-style walker rather than pulling in a
	// dependency. filepath.Glob doesn't support **; filepath.Match does
	// per-segment. So we walk the tree and match each candidate's
	// relative path against the pattern using matchGlob.
	type hit struct {
		Path    string
		ModTime int64
	}
	var hits []hit

	err := filepath.WalkDir(base, func(path string, d os.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			// Skip unreadable entries but keep walking.
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
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		// Normalize to forward slashes for pattern matching (cross-platform).
		rel = filepath.ToSlash(rel)
		if !matchGlob(in.Pattern, rel) {
			return nil
		}
		info, ierr := d.Info()
		var mt int64
		if ierr == nil {
			mt = info.ModTime().UnixNano()
		}
		hits = append(hits, hit{Path: path, ModTime: mt})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("glob_search: walk: %w", err)
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].ModTime > hits[j].ModTime })

	truncated := false
	if len(hits) > in.Limit {
		hits = hits[:in.Limit]
		truncated = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "glob %q in %s: %d match(es)", in.Pattern, base, len(hits))
	if truncated {
		fmt.Fprintf(&b, " (truncated at limit=%d)", in.Limit)
	}
	b.WriteByte('\n')
	for _, h := range hits {
		b.WriteString(h.Path)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// matchGlob implements a simple doublestar matcher. Supports:
//   - ? matches one non-separator character
//   - * matches zero or more non-separator characters
//   - ** matches zero or more path segments (with separators)
//   - All other characters match literally.
//
// This is deliberately small and dependency-free. For complex pattern needs
// a future cycle can swap in github.com/bmatcuk/doublestar.
func matchGlob(pattern, path string) bool {
	// Normalize both to forward slashes.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	return globMatch(pattern, path)
}

func globMatch(pattern, name string) bool {
	// Fast path: literal (no wildcards).
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == name
	}

	pParts := strings.Split(pattern, "/")
	nParts := strings.Split(name, "/")
	return segMatch(pParts, nParts)
}

func segMatch(pattern, name []string) bool {
	pi, ni := 0, 0
	for pi < len(pattern) {
		p := pattern[pi]
		if p == "**" {
			// ** matches zero or more segments. Try every possible consumption.
			if pi == len(pattern)-1 {
				return true // trailing ** matches everything
			}
			for k := ni; k <= len(name); k++ {
				if segMatch(pattern[pi+1:], name[k:]) {
					return true
				}
			}
			return false
		}
		if ni >= len(name) {
			return false
		}
		if !matchOneSegment(p, name[ni]) {
			return false
		}
		pi++
		ni++
	}
	return ni == len(name)
}

// matchOneSegment applies ?/* patterns to a single path segment.
func matchOneSegment(pattern, name string) bool {
	// filepath.Match is per-segment and supports ? and *.
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}
