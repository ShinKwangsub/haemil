package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Session is a JSONL-backed conversation log.
//
// File layout: one JSON object per line, shaped as:
//
//	{"ts":"2026-04-11T12:34:56.789Z","message":{"role":"user","content":[...]}}
//
// Durability policy (Phase 2 — safety over performance):
//   - Directory: 0700
//   - File:      0600, O_APPEND|O_CREATE|O_WRONLY
//   - fsync on every append (file.Sync() after WriteString)
//   - Corrupt lines during replay are skipped with a warning — never fatal.
//
// Replay is NOT implemented in Phase 2; OpenSession opens the file for append
// but leaves the in-memory message list empty. Append*/Messages are stubs
// until Phase 2b.
type Session struct {
	id   string
	dir  string
	path string
	file *os.File
	msgs []Message
}

// ID returns the session identifier (16 hex chars, ~8 bytes of entropy).
func (s *Session) ID() string { return s.id }

// Path returns the on-disk JSONL file path for this session.
func (s *Session) Path() string { return s.path }

// NewSession creates a fresh session in dir. Dir is created with 0700 if
// absent. The JSONL file is opened for O_APPEND|O_CREATE|O_WRONLY, 0600.
//
// This constructor does NOT touch the file beyond opening it (no header line,
// no fsync). It is cheap and safe to call in cli.Run wiring.
func NewSession(dir string) (*Session, error) {
	if dir == "" {
		return nil, errors.New("session: dir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: mkdir %q: %w", dir, err)
	}
	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("session: generate id: %w", err)
	}
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	return &Session{
		id:   id,
		dir:  dir,
		path: path,
		file: f,
		msgs: nil,
	}, nil
}

// OpenSession resumes an existing session identified by id. Only the file
// handle is re-opened in append mode; replay of prior messages is left as
// a TODO for Phase 2b (see Append*/Messages stubs).
func OpenSession(dir, id string) (*Session, error) {
	if dir == "" {
		return nil, errors.New("session: dir is empty")
	}
	if id == "" {
		return nil, errors.New("session: id is empty")
	}
	path := filepath.Join(dir, id+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("session: stat %q: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	// TODO(Phase 2b): replay file → populate msgs, skipping corrupt lines
	// with a warning (fmt.Fprintf(os.Stderr, "session: skipping corrupt line...")).
	return &Session{
		id:   id,
		dir:  dir,
		path: path,
		file: f,
		msgs: nil,
	}, nil
}

// AppendUser appends a user message, fsyncing the file.
func (s *Session) AppendUser(msg Message) error {
	panic("TODO: session.AppendUser not implemented (Phase 2b)")
}

// AppendAssistant appends an assistant message, fsyncing the file.
func (s *Session) AppendAssistant(msg Message) error {
	panic("TODO: session.AppendAssistant not implemented (Phase 2b)")
}

// Messages returns the in-memory message list (post-replay).
func (s *Session) Messages() []Message {
	panic("TODO: session.Messages not implemented (Phase 2b)")
}

// Close flushes and closes the underlying file.
func (s *Session) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// generateSessionID returns 16 hex chars (8 bytes of crypto/rand entropy).
// No external UUID dependency — stdlib only.
func generateSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
