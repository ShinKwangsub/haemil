package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"time"
)

// EventLog is the append-only tail of domain events, per-tenant. MVP
// consumer of the DAL — future C13 Outbox relay will read it, future
// C14 SLA tracker will write into a sibling table that reuses the same
// tenant/ctx conventions.
type EventLog struct{ s *Store }

// LoggedEvent is the row shape returned by queries.
type LoggedEvent struct {
	ID        string
	TenantID  string
	Type      string
	Payload   []byte
	CreatedAt time.Time
}

// NewEventLog binds an EventLog to a Store. Stores can have any number
// of EventLog instances; they are stateless.
func NewEventLog(s *Store) *EventLog { return &EventLog{s: s} }

// Append inserts one row into event_log, deriving tenant_id from ctx.
// Returns the persisted row (ID + CreatedAt filled in). Missing tenant
// surfaces as ErrMissingTenant — the caller forgot to WithTenantID.
func (l *EventLog) Append(ctx context.Context, typ string, payload []byte) (*LoggedEvent, error) {
	tenantID, err := TenantIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("event_log.Append: %w", err)
	}
	id, err := newEventID()
	if err != nil {
		return nil, fmt.Errorf("event_log.Append: id: %w", err)
	}
	now := time.Now()
	// SQLite prefers INTEGER time for cheap ORDER BY; we store
	// UnixNano and reconstruct on read.
	_, err = l.s.db.ExecContext(ctx,
		`INSERT INTO event_log (id, tenant_id, type, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, tenantID, typ, payload, now.UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("event_log.Append: insert: %w", err)
	}
	return &LoggedEvent{
		ID:        id,
		TenantID:  tenantID,
		Type:      typ,
		Payload:   payload,
		CreatedAt: now,
	}, nil
}

// Since returns events at-or-after t for the tenant in ctx, oldest
// first, up to limit rows. limit <= 0 falls back to 100.
//
// Tenant scoping is baked in: there is no cross-tenant variant of this
// method by design. Callers who need cross-tenant reads use CountAll
// (name-flagged) or drop down to Store.DB() with eyes open.
func (l *EventLog) Since(ctx context.Context, t time.Time, limit int) ([]LoggedEvent, error) {
	tenantID, err := TenantIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("event_log.Since: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := l.s.db.QueryContext(ctx,
		`SELECT id, tenant_id, type, payload, created_at
		 FROM event_log
		 WHERE tenant_id = ? AND created_at >= ?
		 ORDER BY created_at ASC, id ASC
		 LIMIT ?`,
		tenantID, t.UnixNano(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("event_log.Since: query: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// CountAll returns the total row count **across all tenants**. Name is
// deliberately loud — it is the only API on EventLog that ignores the
// context tenant. Intended for audit, migration checks, and tests. Do
// not use in request handlers.
func (l *EventLog) CountAll(ctx context.Context) (int, error) {
	var n int
	err := l.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_log`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("event_log.CountAll: %w", err)
	}
	return n, nil
}

func scanEvents(rows *sql.Rows) ([]LoggedEvent, error) {
	var out []LoggedEvent
	for rows.Next() {
		var (
			e           LoggedEvent
			createdNano int64
		)
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Type, &e.Payload, &createdNano); err != nil {
			return nil, fmt.Errorf("event_log: scan: %w", err)
		}
		e.CreatedAt = time.Unix(0, createdNano)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event_log: rows: %w", err)
	}
	return out, nil
}

// newEventID returns a 26-char base32 ID with a time prefix. The first
// 6 bytes encode Unix milliseconds (big-endian) so IDs sort roughly by
// creation time; the remaining 10 bytes are random. Close enough to
// ULID for our purposes without the dependency.
func newEventID() (string, error) {
	var b [16]byte
	ts := time.Now().UnixMilli()
	for i := 5; i >= 0; i-- {
		b[i] = byte(ts & 0xff)
		ts >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), nil
}
