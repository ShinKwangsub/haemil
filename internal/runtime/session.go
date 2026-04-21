package runtime

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session is a JSONL-backed conversation log.
//
// File layout: one JSON object per line, shaped as:
//
//	{"ts":"2026-04-11T12:34:56.789Z","message":{"role":"user","content":[...]}}
//
// Durability policy (Phase 2 — safety over performance):
//   - Directory: 0700
//   - File:      0600, O_APPEND|O_CREATE|O_WRONLY for writes
//   - fsync on every append (file.Sync() after Write)
//   - Corrupt lines during replay are skipped with a warning — never fatal.
type Session struct {
	id   string
	dir  string
	path string
	file *os.File
	msgs []Message
}

// sessionLine is the wire shape of one JSONL record.
type sessionLine struct {
	TS      string  `json:"ts"`
	Message Message `json:"message"`
}

// ID returns the session identifier (16 hex chars, ~8 bytes of entropy).
func (s *Session) ID() string { return s.id }

// Path returns the on-disk JSONL file path for this session.
func (s *Session) Path() string { return s.path }

// NewSession creates a fresh session in dir. Dir is created with 0700 if
// absent. The JSONL file is opened for O_APPEND|O_CREATE|O_WRONLY, 0600.
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

// OpenSession resumes an existing session identified by id. It replays the
// JSONL file to rebuild the in-memory message list, skipping any corrupt
// lines with a stderr warning (never fatal).
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

	// Replay: open for read, scan, skip corrupt lines.
	msgs, err := replaySession(path)
	if err != nil {
		return nil, fmt.Errorf("session: replay %q: %w", path, err)
	}

	// Reopen for append.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	return &Session{
		id:   id,
		dir:  dir,
		path: path,
		file: f,
		msgs: msgs,
	}, nil
}

// replaySession reads the JSONL file line-by-line. Corrupt lines are
// skipped with a stderr warning so a single bad record doesn't kill the
// session.
func replaySession(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []Message
	scanner := bufio.NewScanner(f)
	// JSONL lines can be large (tool outputs embedded). Bump the buffer.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sessionLine
		if err := json.Unmarshal(line, &rec); err != nil {
			fmt.Fprintf(os.Stderr, "session: skipping corrupt line %d in %s: %v\n", lineNo, path, err)
			continue
		}
		msgs = append(msgs, rec.Message)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// AppendUser appends a user message, fsyncing the file.
func (s *Session) AppendUser(msg Message) error {
	if msg.Role == "" {
		msg.Role = RoleUser
	}
	return s.append(msg)
}

// AppendAssistant appends an assistant message, fsyncing the file.
func (s *Session) AppendAssistant(msg Message) error {
	if msg.Role == "" {
		msg.Role = RoleAssistant
	}
	return s.append(msg)
}

// append is the shared write+fsync path.
func (s *Session) append(msg Message) error {
	if s == nil || s.file == nil {
		return errors.New("session: closed")
	}
	rec := sessionLine{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Message: msg,
	}
	buf, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := s.file.Write(buf); err != nil {
		return fmt.Errorf("session: write: %w", err)
	}
	// Phase 2 policy: fsync after every append — safety over performance.
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("session: fsync: %w", err)
	}
	s.msgs = append(s.msgs, msg)
	return nil
}

// Messages returns a copy of the in-memory message list (post-replay plus
// any appends). Returns nil for a fresh, untouched session.
func (s *Session) Messages() []Message {
	if s == nil || len(s.msgs) == 0 {
		return nil
	}
	out := make([]Message, len(s.msgs))
	copy(out, s.msgs)
	return out
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
