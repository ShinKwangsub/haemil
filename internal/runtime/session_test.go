package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionAppendAndReplay verifies the end-to-end write + read loop:
// write messages via Append*, close, reopen via OpenSession, and confirm
// Messages() returns the exact sequence of what was written.
func TestSessionAppendAndReplay(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()

	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockTypeText, Text: "hello"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: BlockTypeText, Text: "hi, running ls"},
			{Type: BlockTypeToolUse, ID: "toolu_01", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: BlockTypeToolResult, ToolUseID: "toolu_01", Content: "file1\nfile2", IsError: false},
		}},
	}

	for i, m := range msgs {
		var err error
		if m.Role == RoleUser {
			err = s.AppendUser(m)
		} else {
			err = s.AppendAssistant(m)
		}
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// In-memory cache works immediately
	inMem := s.Messages()
	if len(inMem) != len(msgs) {
		t.Fatalf("in-memory len: got %d, want %d", len(inMem), len(msgs))
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// File permissions check
	fi, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %o, want %o", mode, 0o600)
	}
	dirFi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat session dir: %v", err)
	}
	if mode := dirFi.Mode().Perm(); mode != 0o700 {
		// TempDir may use a more permissive umask — only fail if clearly wrong.
		t.Logf("dir mode: %o (expected 0700 in production path)", mode)
	}

	// Replay: OpenSession should reconstruct the exact message sequence.
	s2, err := OpenSession(dir, id)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s2.Close()

	back := s2.Messages()
	if len(back) != len(msgs) {
		t.Fatalf("replay len: got %d, want %d", len(back), len(msgs))
	}
	for i, m := range msgs {
		if back[i].Role != m.Role {
			t.Errorf("msg[%d] role: got %q, want %q", i, back[i].Role, m.Role)
		}
		if len(back[i].Content) != len(m.Content) {
			t.Errorf("msg[%d] content len: got %d, want %d", i, len(back[i].Content), len(m.Content))
			continue
		}
		for j, block := range m.Content {
			if back[i].Content[j].Type != block.Type {
				t.Errorf("msg[%d] block[%d] type: got %q, want %q", i, j, back[i].Content[j].Type, block.Type)
			}
			if back[i].Content[j].Text != block.Text {
				t.Errorf("msg[%d] block[%d] text: got %q, want %q", i, j, back[i].Content[j].Text, block.Text)
			}
		}
	}
}

// TestSessionCorruptLineSkip writes a corrupt JSONL line between two good
// ones and verifies replay skips the bad line (with a stderr warning) and
// recovers both good messages.
func TestSessionCorruptLineSkip(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()
	path := s.Path()

	if err := s.AppendUser(Message{Role: RoleUser, Content: []ContentBlock{{Type: BlockTypeText, Text: "first"}}}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	s.Close()

	// Manually append a corrupt line and then a good one.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := f.WriteString("{this is not valid json\n"); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	goodLine := `{"ts":"2026-04-11T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"third"}]}}` + "\n"
	if _, err := f.WriteString(goodLine); err != nil {
		t.Fatalf("write good: %v", err)
	}
	f.Close()

	// Replay should recover the two valid messages, skipping the corrupt one.
	s2, err := OpenSession(dir, id)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s2.Close()

	msgs := s2.Messages()
	if len(msgs) != 2 {
		t.Fatalf("recovered len: got %d, want 2", len(msgs))
	}
	if msgs[0].Content[0].Text != "first" {
		t.Errorf("msg[0]: got %q, want %q", msgs[0].Content[0].Text, "first")
	}
	if msgs[1].Content[0].Text != "third" {
		t.Errorf("msg[1]: got %q, want %q", msgs[1].Content[0].Text, "third")
	}
}

// TestSessionClosedAppend verifies Append* returns an error (not panic)
// after Close.
func TestSessionClosedAppend(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.Close()

	err = s.AppendUser(Message{Role: RoleUser, Content: []ContentBlock{{Type: BlockTypeText, Text: "x"}}})
	if err == nil {
		t.Fatal("expected error appending after close, got nil")
	}
}

// TestSessionIDFormat verifies session IDs are 16 lowercase hex chars.
func TestSessionIDFormat(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	id := s.ID()
	if len(id) != 16 {
		t.Errorf("id length: got %d, want 16", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("id has non-hex-lower char: %c in %q", c, id)
			break
		}
	}

	// File name reflects ID
	base := filepath.Base(s.Path())
	if !strings.HasPrefix(base, id) {
		t.Errorf("file name %q does not start with id %q", base, id)
	}
}
