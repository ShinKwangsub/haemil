package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func openMemory(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), "sqlite://:memory:")
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openFile(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "haemil.db")
	s, err := Open(context.Background(), "sqlite://"+path)
	if err != nil {
		t.Fatalf("Open file: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenAndMigrateSQLiteMemory(t *testing.T) {
	s := openMemory(t)

	// Sanity: event_log exists + is queryable.
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM event_log`).Scan(&n); err != nil {
		t.Fatalf("count event_log: %v", err)
	}
	if n != 0 {
		t.Errorf("expected empty table, got %d rows", n)
	}
}

func TestOpenRejectsUnknownScheme(t *testing.T) {
	_, err := Open(context.Background(), "mysql://ignored")
	if err == nil {
		t.Fatal("expected error for mysql://")
	}
	if !strings.Contains(err.Error(), "unsupported DSN") {
		t.Errorf("error text: %q", err.Error())
	}
}

func TestOpenFilePersists(t *testing.T) {
	// Open, write one row, close, reopen, read it back.
	path := filepath.Join(t.TempDir(), "persist.db")
	dsn := "sqlite://" + path

	ctx := WithTenantID(context.Background(), "tenantP")

	s1, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	log1 := NewEventLog(s1)
	if _, err := log1.Append(ctx, "persist.test", []byte(`{"v":1}`)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	log2 := NewEventLog(s2)
	total, err := log2.CountAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("CountAll after reopen: got %d, want 1", total)
	}
}

func TestStoreDialectExposed(t *testing.T) {
	s := openMemory(t)
	if s.Dialect().Name() != "sqlite" {
		t.Errorf("dialect: %q", s.Dialect().Name())
	}
}

func TestParseDSN(t *testing.T) {
	cases := []struct {
		in   string
		drv  string
		path string
		ok   bool
	}{
		{"sqlite://:memory:", "sqlite", ":memory:", true},
		{"sqlite:///abs/haemil.db", "sqlite", "/abs/haemil.db", true},
		{"sqlite://rel.db", "sqlite", "rel.db", true},
		{"postgres://x", "", "", false},
		{"not-a-dsn", "", "", false},
	}
	for _, c := range cases {
		drv, path, err := parseDSN(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("%q: unexpected err %v", c.in, err)
				continue
			}
			if drv != c.drv || path != c.path {
				t.Errorf("%q: got (%q,%q), want (%q,%q)", c.in, drv, path, c.drv, c.path)
			}
		} else {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
		}
	}
}
