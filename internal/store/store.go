// Package store implements SQLite materialization and FTS5 full-text search
// over Opax git data. The database at .git/opax/opax.db is always rebuildable
// from git via `opax db rebuild`.
package store
