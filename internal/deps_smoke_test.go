// Package deps_smoke_test verifies that each production dependency compiles and
// behaves correctly under CGO_ENABLED=0. This file is temporary scaffolding;
// once downstream epics have real tests exercising these libraries the smoke
// tests can be removed.
package deps_smoke_test

import (
	"crypto/rand"
	"database/sql"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
	"gopkg.in/yaml.v3"
)

// TestSmokeGoGit opens this repository via git.PlainOpen and reads HEAD,
// verifying that a valid commit hash is returned.
func TestSmokeGoGit(t *testing.T) {
	// Test runs from internal/, so ".." reaches the repo root.
	repo, err := gogit.PlainOpen("..")
	if err != nil {
		t.Fatalf("git: PlainOpen failed: %v", err)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("git: Head failed: %v", err)
	}

	hash := ref.Hash()
	if hash.IsZero() {
		t.Fatal("git: HEAD hash is zero")
	}

	// A valid SHA-1 hex string is 40 characters.
	hashStr := hash.String()
	if len(hashStr) != 40 {
		t.Fatalf("git: expected 40-char hash, got %q (len %d)", hashStr, len(hashStr))
	}
}

// TestSmokeSQLite opens an in-memory SQLite database, creates a table, inserts
// a row, and queries it back.
func TestSmokeSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sqlite: open failed: %v", err)
	}
	defer db.Close()

	if _, err = db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT NOT NULL)`); err != nil {
		t.Fatalf("sqlite: CREATE TABLE failed: %v", err)
	}

	if _, err = db.Exec(`INSERT INTO t (id, val) VALUES (1, 'hello')`); err != nil {
		t.Fatalf("sqlite: INSERT failed: %v", err)
	}

	var got string
	if err = db.QueryRow(`SELECT val FROM t WHERE id = 1`).Scan(&got); err != nil {
		t.Fatalf("sqlite: SELECT failed: %v", err)
	}

	if got != "hello" {
		t.Fatalf("sqlite: expected %q, got %q", "hello", got)
	}
}

// TestSmokeSQLiteFTS5 creates an FTS5 virtual table, inserts a row, and
// verifies a MATCH query returns the inserted row.
func TestSmokeSQLiteFTS5(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sqlite: open failed: %v", err)
	}
	defer db.Close()

	if _, err = db.Exec(`CREATE VIRTUAL TABLE docs USING fts5(body)`); err != nil {
		t.Fatalf("sqlite: CREATE VIRTUAL TABLE (fts5) failed: %v", err)
	}

	if _, err = db.Exec(`INSERT INTO docs (body) VALUES ('the quick brown fox')`); err != nil {
		t.Fatalf("sqlite: INSERT into fts5 table failed: %v", err)
	}

	var got string
	if err = db.QueryRow(`SELECT body FROM docs WHERE docs MATCH 'fox'`).Scan(&got); err != nil {
		t.Fatalf("sqlite: FTS5 MATCH query failed: %v", err)
	}

	if !strings.Contains(got, "fox") {
		t.Fatalf("sqlite: FTS5 MATCH returned unexpected row: %q", got)
	}
}

// TestSmokeULID generates a ULID with crypto/rand entropy, parses it back,
// verifies the embedded timestamp is close to now, and verifies monotonic
// ordering holds for two ULIDs generated in the same millisecond.
func TestSmokeULID(t *testing.T) {
	before := time.Now().Add(-time.Second)

	id1, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		t.Fatalf("ulid: New failed: %v", err)
	}

	after := time.Now().Add(time.Second)

	// Parse it back from its string representation.
	parsed, err := ulid.ParseStrict(id1.String())
	if err != nil {
		t.Fatalf("ulid: ParseStrict failed: %v", err)
	}

	if parsed != id1 {
		t.Fatalf("ulid: round-trip mismatch: got %v, want %v", parsed, id1)
	}

	// Timestamp should be within the before/after window.
	ts := id1.Timestamp()
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("ulid: timestamp %v is outside expected window [%v, %v]", ts, before, after)
	}

	// Monotonic ordering: two ULIDs at the same millisecond must sort correctly.
	ms := ulid.Timestamp(time.Now())
	mono := ulid.Monotonic(rand.Reader, 0)

	id2, err := ulid.New(ms, mono)
	if err != nil {
		t.Fatalf("ulid: monotonic New(1) failed: %v", err)
	}

	id3, err := ulid.New(ms, mono)
	if err != nil {
		t.Fatalf("ulid: monotonic New(2) failed: %v", err)
	}

	if id2.Compare(id3) >= 0 {
		t.Fatalf("ulid: monotonic ordering violated: %v >= %v", id2, id3)
	}
}

// TestSmokeYAML parses a YAML string into a struct and verifies that strict
// mode (KnownFields(true)) rejects an unknown key.
func TestSmokeYAML(t *testing.T) {
	type Config struct {
		Name    string `yaml:"name"`
		Version int    `yaml:"version"`
	}

	validYAML := "name: opax\nversion: 1\n"
	var cfg Config
	if err := yaml.Unmarshal([]byte(validYAML), &cfg); err != nil {
		t.Fatalf("yaml: Unmarshal of valid input failed: %v", err)
	}
	if cfg.Name != "opax" || cfg.Version != 1 {
		t.Fatalf("yaml: unexpected parsed value: %+v", cfg)
	}

	// Strict mode must reject unknown keys.
	unknownYAML := "name: opax\nunknown_field: bad\n"
	dec := yaml.NewDecoder(strings.NewReader(unknownYAML))
	dec.KnownFields(true)
	var strict Config
	if err := dec.Decode(&strict); err == nil {
		t.Fatal("yaml: expected error for unknown field in strict mode, got nil")
	}
}

// TestSmokeMCPGo verifies that the mcp-go package compiles and that
// server.NewMCPServer returns a non-nil server instance.
func TestSmokeMCPGo(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0")
	if srv == nil {
		t.Fatal("mcp-go: NewMCPServer returned nil")
	}
}
