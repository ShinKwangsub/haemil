package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------- read_file ----------------

func TestReadFileFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("line one\nline two\nline three\n"), 0o644)

	tool := NewReadFile()
	out, err := tool.Execute(context.Background(), jsonStr(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"3 lines total", "1\tline one", "2\tline two", "3\tline three"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestReadFileLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")
	os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644)

	tool := NewReadFile()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","start_line":2,"end_line":4}`, path)))
	if !strings.Contains(out, "showing 2–4") {
		t.Errorf("header should mention range: %s", out)
	}
	if strings.Contains(out, "one") || strings.Contains(out, "five") {
		t.Errorf("range filter leaked out-of-range: %s", out)
	}
	if !strings.Contains(out, "two") || !strings.Contains(out, "three") || !strings.Contains(out, "four") {
		t.Errorf("range filter missed lines: %s", out)
	}
}

func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03}, 0o644)

	tool := NewReadFile()
	_, err := tool.Execute(context.Background(), jsonStr(`{"path":"`+path+`"}`))
	if err == nil {
		t.Fatal("expected binary-file rejection")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error should mention binary, got %v", err)
	}
}

func TestReadFileMissing(t *testing.T) {
	tool := NewReadFile()
	_, err := tool.Execute(context.Background(), jsonStr(`{"path":"/no/such/path/here"}`))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFileIsDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFile()
	_, err := tool.Execute(context.Background(), jsonStr(`{"path":"`+dir+`"}`))
	if err == nil {
		t.Fatal("expected error when reading a directory")
	}
}

// ---------------- write_file ----------------

func TestWriteFileCreateNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "new.txt")
	tool := NewWriteFile()
	out, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","content":"a\nb\nc\n"}`, path)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("output should say created: %s", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "a\nb\nc\n" {
		t.Errorf("file content: %q", got)
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tool := NewWriteFile()
	out, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","content":"new"}`, path)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "overwrote") {
		t.Errorf("output should say overwrote: %s", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("file content: %q", got)
	}
}

// ---------------- edit_file ----------------

func TestEditFileUnique(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("foo bar baz\n"), 0o644)

	tool := NewEditFile()
	_, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","old_string":"bar","new_string":"qux"}`, path)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "foo qux baz\n" {
		t.Errorf("file content: %q", got)
	}
}

func TestEditFileAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	os.WriteFile(path, []byte("x=1\nx=2\nx=3\n"), 0o644)

	tool := NewEditFile()
	_, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","old_string":"x=","new_string":"y="}`, path)))
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "appears 3 times") {
		t.Errorf("error should mention count: %v", err)
	}
}

func TestEditFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	os.WriteFile(path, []byte("x=1\nx=2\nx=3\n"), 0o644)

	tool := NewEditFile()
	_, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","old_string":"x=","new_string":"y=","replace_all":true}`, path)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "y=1\ny=2\ny=3\n" {
		t.Errorf("file content: %q", got)
	}
}

func TestEditFileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.txt")
	os.WriteFile(path, []byte("hello"), 0o644)

	tool := NewEditFile()
	_, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"path":"%s","old_string":"xxx","new_string":"yyy"}`, path)))
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// ---------------- glob_search ----------------

func TestGlobSearchBasic(t *testing.T) {
	dir := t.TempDir()
	// Build a small tree.
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "top.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "mid.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "readme.txt"), []byte("notes"), 0o644)

	tool := NewGlobSearch()
	out, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"**/*.go","cwd":"%s"}`, dir)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "top.go") {
		t.Errorf("missing top.go in %s", out)
	}
	if !strings.Contains(out, "mid.go") {
		t.Errorf("missing mid.go in %s", out)
	}
	if !strings.Contains(out, "deep.go") {
		t.Errorf("missing deep.go in %s", out)
	}
	if strings.Contains(out, "readme.txt") {
		t.Errorf("readme.txt should not match *.go: %s", out)
	}
}

func TestGlobSearchExcludesNoise(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "x.js"), []byte("// x"), 0o644)
	os.WriteFile(filepath.Join(dir, "src.js"), []byte("// src"), 0o644)

	tool := NewGlobSearch()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"**/*.js","cwd":"%s"}`, dir)))
	if strings.Contains(out, "node_modules") {
		t.Errorf("node_modules should be excluded: %s", out)
	}
	if !strings.Contains(out, "src.js") {
		t.Errorf("src.js missing: %s", out)
	}
}

func TestGlobMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "sub/main.go", true},
		{"**/*.go", "sub/deep/main.go", true},
		{"internal/**/*_test.go", "internal/tools/bash_test.go", true},
		{"internal/**/*_test.go", "cmd/haemil/main.go", false},
		{"internal/*.go", "internal/tools/bash.go", false}, // single * doesn't cross /
		{"internal/*.go", "internal/bash.go", true},
	}
	for _, c := range cases {
		got := matchGlob(c.pattern, c.path)
		if got != c.want {
			t.Errorf("matchGlob(%q, %q): got %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// ---------------- grep_search ----------------

func TestGrepSearchBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("foo\nbar\nbaz foo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing interesting\n"), 0o644)

	tool := NewGrepSearch()
	out, err := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"foo","path":"%s"}`, dir)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "2 match") {
		t.Errorf("should find 2 matches: %s", out)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("should list a.txt: %s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("b.txt should not appear (no match): %s", out)
	}
	if !strings.Contains(out, "1: foo") || !strings.Contains(out, "3: baz foo") {
		t.Errorf("line matches missing: %s", out)
	}
}

func TestGrepSearchCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("Hello\nHELLO\nhello\n"), 0o644)
	tool := NewGrepSearch()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"hello","path":"%s","case_insensitive":true}`, dir)))
	if !strings.Contains(out, "3 match") {
		t.Errorf("should find 3 matches: %s", out)
	}
}

func TestGrepSearchContext(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "d.txt"), []byte("a\nb\nTARGET\nc\nd\n"), 0o644)
	tool := NewGrepSearch()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"TARGET","path":"%s","context":1}`, dir)))
	// With context=1 we expect lines 2 (b), 3 (TARGET), 4 (c) in output.
	for _, want := range []string{"2: b", "3: TARGET", "4: c"} {
		if !strings.Contains(out, want) {
			t.Errorf("context missing %q: %s", want, out)
		}
	}
	// Lines 1 and 5 should NOT be present.
	if strings.Contains(out, "1: a") || strings.Contains(out, "5: d") {
		t.Errorf("context too wide: %s", out)
	}
}

func TestGrepSearchSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello world\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 'h', 'e', 'l', 'l', 'o', 0, 3}, 0o644)

	tool := NewGrepSearch()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"hello","path":"%s"}`, dir)))
	if !strings.Contains(out, "text.txt") {
		t.Errorf("text.txt should be found: %s", out)
	}
	if strings.Contains(out, "bin.dat") {
		t.Errorf("bin.dat (binary) should be skipped: %s", out)
	}
}

func TestGrepSearchIncludeFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keep.go"), []byte("needle\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "skip.md"), []byte("needle\n"), 0o644)

	tool := NewGrepSearch()
	out, _ := tool.Execute(context.Background(), jsonStr(fmt.Sprintf(`{"pattern":"needle","path":"%s","include":"*.go"}`, dir)))
	if !strings.Contains(out, "keep.go") {
		t.Errorf("keep.go should be searched: %s", out)
	}
	if strings.Contains(out, "skip.md") {
		t.Errorf("skip.md excluded by include: %s", out)
	}
}

// ---------------- util ----------------

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\n\nb\n", 3},
	}
	for _, c := range cases {
		got := countLines(c.in)
		if got != c.want {
			t.Errorf("countLines(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

// jsonStr wraps a raw JSON string in json.RawMessage for test ergonomics.
func jsonStr(s string) json.RawMessage { return json.RawMessage(s) }
