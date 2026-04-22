// Package store is haemil's multi-tenant DAL. It wraps database/sql with
// a thin Dialect abstraction (SQLite concrete, Postgres hooks reserved
// for a later cycle) and a context-carried tenant ID that every
// tenant-scoped query is expected to consume.
//
// Design intent (goclaw god-node "TenantIDFromContext" pattern port):
// every write that belongs to a tenant must derive the tenant ID from
// ctx, not from a function argument. Arguments get forgotten, ctx gets
// threaded through middleware. This way a missing WithTenantID up the
// stack fails loudly (ErrMissingTenant) instead of silently writing
// rows tagged with an empty string.
package store

// Dialect absorbs SQL grammar differences between backends. MVP
// implements only SQLite; Postgres will add NewPostgresDialect() without
// touching consumers.
type Dialect interface {
	// Name returns a short backend identifier ("sqlite", "postgres").
	Name() string

	// Placeholder returns the param placeholder for position n (1-based).
	// SQLite: "?" regardless of n. Postgres: "$1", "$2", ...
	Placeholder(n int) string

	// SupportsReturning reports whether INSERT ... RETURNING works on
	// this backend. SQLite 3.35+ supports it, but modernc versions vary,
	// so MVP conservatively says false and consumers do a separate
	// SELECT when they need the new row.
	SupportsReturning() bool

	// QuoteIdent wraps an identifier (table/column) in backend-correct
	// quoting. Double quotes are portable enough for MVP.
	QuoteIdent(s string) string
}

type sqliteDialect struct{}

// NewSQLiteDialect returns the SQLite dialect singleton.
func NewSQLiteDialect() Dialect { return sqliteDialect{} }

func (sqliteDialect) Name() string           { return "sqlite" }
func (sqliteDialect) Placeholder(int) string { return "?" }
func (sqliteDialect) SupportsReturning() bool {
	// modernc.org/sqlite supports RETURNING (SQLite ≥ 3.35) but we
	// standardise on the subset that also works on older PG, MySQL-via-
	// pg-wire, etc. Consumers write `INSERT … ; SELECT … WHERE id=?`
	// instead.
	return false
}
func (sqliteDialect) QuoteIdent(s string) string { return `"` + s + `"` }
