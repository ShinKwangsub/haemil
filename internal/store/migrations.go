package store

// migrations is the ordered list of DDL statements applied on Open.
// Every statement is idempotent (CREATE ... IF NOT EXISTS) so we can
// run it on every boot without a schema-version table (YAGNI for MVP —
// if the schema grows, move to goose or golang-migrate).
//
// When adding a new statement: APPEND to the slice. Never edit existing
// entries in place — once a migration has run in production, changing
// it is a silent diverge. The "new column? write an ALTER" rule will
// be enforced once we hit C13/C14.
var migrations = []string{
	// event_log: append-only tail of domain events. Tenant-scoped via
	// the tenant_id column; every query adds a WHERE tenant_id = ?.
	`CREATE TABLE IF NOT EXISTS event_log (
		id          TEXT PRIMARY KEY,
		tenant_id   TEXT NOT NULL,
		type        TEXT NOT NULL,
		payload     BLOB NOT NULL,
		created_at  INTEGER NOT NULL
	)`,

	// Composite index: fast "events for tenant X since T" scans in
	// descending order. Used by EventLog.Since (which we override to
	// ASC explicitly when needed — index still helps as a covering one).
	`CREATE INDEX IF NOT EXISTS event_log_tenant_time
		ON event_log (tenant_id, created_at DESC)`,
}
