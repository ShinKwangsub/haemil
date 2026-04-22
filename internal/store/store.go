package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	// modernc.org/sqlite is pure-Go (no CGO), so haemil binaries stay
	// trivial to cross-compile. The blank import registers the "sqlite"
	// driver name on database/sql.
	_ "modernc.org/sqlite"
)

// Store is a connection + dialect bundle. Callers open it once, share
// it across goroutines (database/sql is safe), and close it on
// shutdown. Concrete stores (EventLog, future TaskStore, etc.) take
// *Store and carry no state of their own.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Open parses a DSN and returns a migrated, ready-to-use Store. MVP
// accepts only "sqlite://" schemes:
//
//	"sqlite://:memory:"              — ephemeral (tests)
//	"sqlite:///abs/path/haemil.db"   — file-backed
//	"sqlite://relative/haemil.db"    — relative to cwd
//
// Postgres is reserved; adding it is a separate cycle (new dialect +
// parser branch here).
func Open(ctx context.Context, dsn string) (*Store, error) {
	driver, path, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driver, path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", driver, err)
	}

	// SQLite serialises writes at the file level anyway, and busy_timeout
	// PRAGMAs set on one sql.DB connection do NOT propagate to other
	// connections the pool opens — the classic "SQLITE_BUSY under
	// concurrent writes" footgun. Pinning to a single connection makes
	// sql.DB queue writes cleanly in Go and avoids the issue entirely.
	// Read-heavy workloads will want WAL mode + multiple connections in
	// a later cycle; for C11 correctness trumps throughput.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	// Still set busy_timeout defensively — if we ever bump MaxOpenConns,
	// this gives a forgiving window before surfacing lock contention as
	// an error.
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: busy_timeout: %w", err)
	}

	s := &Store{db: db, dialect: NewSQLiteDialect()}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

// parseDSN splits the haemil-flavour DSN (scheme://payload) into driver
// name + driver-specific path. Isolated for test visibility.
func parseDSN(dsn string) (driver, path string, err error) {
	const sqlitePrefix = "sqlite://"
	switch {
	case strings.HasPrefix(dsn, sqlitePrefix):
		return "sqlite", strings.TrimPrefix(dsn, sqlitePrefix), nil
	default:
		return "", "", fmt.Errorf("store: unsupported DSN scheme in %q (MVP accepts sqlite:// only)", dsn)
	}
}

// Close releases the underlying sql.DB.
func (s *Store) Close() error { return s.db.Close() }

// Dialect exposes the active SQL grammar so concrete stores can build
// backend-portable queries via Placeholder(n).
func (s *Store) Dialect() Dialect { return s.dialect }

// DB is the escape hatch for queries concrete stores haven't promoted
// into typed methods yet. Prefer the typed APIs; use DB() sparingly.
func (s *Store) DB() *sql.DB { return s.db }

// Migrate applies the DDL list in migrations.go. Idempotent — re-running
// on a migrated DB is a no-op.
func (s *Store) Migrate(ctx context.Context) error {
	for i, stmt := range migrations {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migration %d: %w", i, err)
		}
	}
	return nil
}
