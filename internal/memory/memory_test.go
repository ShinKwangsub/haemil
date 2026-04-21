package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreLoadMissing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "never.md"), "x")
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func TestStoreAppendCreatesDirAndHeader(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	path := filepath.Join(dir, "USER.md")
	s := NewStore(path, "user")

	if err := s.Append("photography is a hobby"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	body, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(body, "# user memory") {
		t.Errorf("expected header, got %q", body)
	}
	if !strings.Contains(body, "photography is a hobby") {
		t.Errorf("expected bullet text, got %q", body)
	}
	// File should be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		// On some filesystems the mode may be affected by umask at mkdir, but
		// the file mode should be 0600.
		t.Errorf("file mode: got %v, want 0600", mode)
	}
}

func TestStoreAppendRejectsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "x.md"), "x")
	if err := s.Append(""); err == nil {
		t.Error("expected error for empty text")
	}
	if err := s.Append("   \t\n"); err == nil {
		t.Error("expected error for whitespace-only text")
	}
}

func TestStoreBullets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.md")
	content := `# project memory

some blurb text

- first fact
- second fact

# another section
- third fact

not a bullet
-not-a-bullet-either
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path, "project")
	got, err := s.Bullets()
	if err != nil {
		t.Fatalf("Bullets: %v", err)
	}
	want := []string{"first fact", "second fact", "third fact"}
	if len(got) != len(want) {
		t.Fatalf("count: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

func TestStoreBulletsMissingFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "never.md"), "x")
	got, err := s.Bullets()
	if err != nil {
		t.Fatalf("Bullets missing: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestContextRenderEmpty(t *testing.T) {
	c := &Context{
		User:    NewStore(filepath.Join(t.TempDir(), "u.md"), "user"),
		Project: NewStore(filepath.Join(t.TempDir(), "p.md"), "project"),
	}
	got, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "" {
		t.Errorf("empty context should render \"\", got %q", got)
	}
}

func TestContextRenderUserOnly(t *testing.T) {
	dir := t.TempDir()
	c := &Context{
		User:    NewStore(filepath.Join(dir, "u.md"), "user"),
		Project: NewStore(filepath.Join(dir, "p.md"), "project"),
	}
	_ = c.User.Append("uses Korean")
	_ = c.User.Append("prefers short responses")

	got, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(got, "<memory-context>") {
		t.Errorf("expected <memory-context> wrapper, got %q", got)
	}
	if !strings.Contains(got, "[user memory]") {
		t.Errorf("missing user section: %q", got)
	}
	if strings.Contains(got, "[project memory]") {
		t.Errorf("project section should be absent: %q", got)
	}
	if !strings.Contains(got, "uses Korean") || !strings.Contains(got, "prefers short responses") {
		t.Errorf("bullets missing: %q", got)
	}
}

func TestContextRenderBoth(t *testing.T) {
	dir := t.TempDir()
	c := &Context{
		User:    NewStore(filepath.Join(dir, "u.md"), "user"),
		Project: NewStore(filepath.Join(dir, "p.md"), "project"),
	}
	_ = c.User.Append("user fact A")
	_ = c.Project.Append("project fact B")

	got, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got, "[user memory]") || !strings.Contains(got, "[project memory]") {
		t.Errorf("both sections should appear: %q", got)
	}
	// Bullets preserved.
	if !strings.Contains(got, "user fact A") || !strings.Contains(got, "project fact B") {
		t.Errorf("bullets missing: %q", got)
	}
	if !strings.HasSuffix(got, "</memory-context>") {
		t.Errorf("expected trailing </memory-context>, got %q", got)
	}
}

func TestAppendTimestampPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.md")
	s := NewStore(path, "x")
	if err := s.Append("dated bullet"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	body, _ := s.Load()
	// Expect something like "- [2026-04-22] dated bullet"
	if !strings.Contains(body, "- [") || !strings.Contains(body, "] dated bullet") {
		t.Errorf("expected date prefix in bullet, got %q", body)
	}
}
