// Package memory implements a minimal persistent memory layer.
//
// Two stores:
//   - UserStore    — cross-project user facts (preferences, communication style,
//                    identity). Lives at ~/.haemil/USER.md.
//   - ProjectStore — project-specific facts (architecture, active work,
//                    conventions). Lives at <cwd>/.haemil/MEMORY.md by default.
//
// Both files are append-oriented Markdown bullet lists. Load merges the
// two into a single context blob that the REPL injects into the system
// prompt with a <memory-context>...</memory-context> wrapper (Hermes
// pattern, so the model distinguishes recall from fresh user input).
//
// Out of scope for C8 MVP: the review loop that Hermes uses to
// auto-generate new memories every N turns. Appending is manual via
// the /remember slash command.
package memory

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultUserMemoryPath returns ~/.haemil/USER.md.
func DefaultUserMemoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "USER.md"
	}
	return filepath.Join(home, ".haemil", "USER.md")
}

// DefaultProjectMemoryPath returns <cwd>/.haemil/MEMORY.md.
func DefaultProjectMemoryPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "MEMORY.md"
	}
	return filepath.Join(cwd, ".haemil", "MEMORY.md")
}

// Store is a single memory file.
//
// The underlying file is human-editable Markdown; we don't enforce a
// schema beyond "one bullet per fact" (lines starting with `- `). Empty
// lines and section headers are allowed and preserved on read/render.
type Store struct {
	Path  string
	Label string // display label used by /memory output, e.g. "user", "project"
}

// NewStore builds a Store. Neither the directory nor the file need to
// exist — Load returns empty content for a missing file, and Append
// creates the dir/file with 0700/0600 on first write.
func NewStore(path, label string) *Store {
	return &Store{Path: path, Label: label}
}

// Load returns the raw Markdown content. A missing file yields "" without
// an error — absence is normal, not a fault.
func (s *Store) Load() (string, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read %q: %w", s.Path, err)
	}
	return string(b), nil
}

// Append adds one bullet to the store, creating the directory/file if
// missing. Bullets are timestamped at write time so the file reads like
// a chronological notebook. Blank input is rejected.
func (s *Store) Append(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("memory: empty text")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("memory: mkdir %q: %w", filepath.Dir(s.Path), err)
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("memory: open %q: %w", s.Path, err)
	}
	defer f.Close()

	// If the file is new, seed it with a header.
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("memory: stat %q: %w", s.Path, err)
	}
	if info.Size() == 0 {
		header := fmt.Sprintf("# %s memory\n\n", s.Label)
		if _, err := f.WriteString(header); err != nil {
			return fmt.Errorf("memory: write header: %w", err)
		}
	}

	ts := time.Now().UTC().Format("2006-01-02")
	line := fmt.Sprintf("- [%s] %s\n", ts, text)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("memory: write bullet: %w", err)
	}
	return f.Sync()
}

// Bullets returns every `- ` line in the file, order preserved. Useful
// for structured access (vs. Load which returns the raw blob). Leading
// `- ` is stripped from each entry.
func (s *Store) Bullets() ([]string, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: open %q: %w", s.Path, err)
	}
	defer f.Close()
	return readBullets(f)
}

func readBullets(r io.Reader) ([]string, error) {
	var out []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if strings.HasPrefix(line, "- ") {
			out = append(out, strings.TrimSpace(line[2:]))
		}
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// Context bundles a user + project store and renders the combined block
// for system-prompt injection.
type Context struct {
	User    *Store
	Project *Store
}

// NewContext returns a Context with the conventional default paths.
func NewContext() *Context {
	return &Context{
		User:    NewStore(DefaultUserMemoryPath(), "user"),
		Project: NewStore(DefaultProjectMemoryPath(), "project"),
	}
}

// Render returns a single string suitable for embedding in a system
// prompt. Empty when both files are empty/missing, so callers can
// unconditionally concatenate without extra blank sections.
//
// Format (Hermes-style <memory-context> wrapper so the LLM treats the
// content as recalled data, not a fresh user turn):
//
//	<memory-context>
//	[user memory]
//	- ...
//	[project memory]
//	- ...
//	</memory-context>
func (c *Context) Render() (string, error) {
	if c == nil {
		return "", nil
	}
	userBullets, err := c.User.Bullets()
	if err != nil {
		return "", fmt.Errorf("memory: load user: %w", err)
	}
	projectBullets, err := c.Project.Bullets()
	if err != nil {
		return "", fmt.Errorf("memory: load project: %w", err)
	}
	if len(userBullets) == 0 && len(projectBullets) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("<memory-context>\n")
	if len(userBullets) > 0 {
		b.WriteString("[user memory]\n")
		for _, bullet := range userBullets {
			b.WriteString("- ")
			b.WriteString(bullet)
			b.WriteByte('\n')
		}
	}
	if len(projectBullets) > 0 {
		if len(userBullets) > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[project memory]\n")
		for _, bullet := range projectBullets {
			b.WriteString("- ")
			b.WriteString(bullet)
			b.WriteByte('\n')
		}
	}
	b.WriteString("</memory-context>")
	return b.String(), nil
}
