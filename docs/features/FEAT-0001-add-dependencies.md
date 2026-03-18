# FEAT-0001 — Add Dependencies

**Epic:** [EPIC-0000 — Project Foundation](../epics/EPIC-0000-foundation.md)
**Status:** Not started
**Dependencies:** None
**Dependents:** FEAT-0002, FEAT-0003, FEAT-0004 (all need imports available)

---

## Problem

The project currently has only `cobra` in `go.mod`. Every downstream epic requires libraries for git operations, SQLite, ULID generation, YAML parsing, and MCP transport. These must be added and verified before any feature work begins.

The overriding constraint is **pure Go, single binary, zero runtime dependencies**. Every dependency must compile with `CGO_ENABLED=0`. This rules out `mattn/go-sqlite3` and any library that shells out or links to C.

---

## Dependencies

### 1. go-git — Git plumbing

**Module:** `github.com/go-git/go-git/v5`

**Used by:** E1 (Git Plumbing Layer) — orphan branch management, tree manipulation, ref updates, notes operations.

**Why go-git:** Pure Go. Provides plumbing-level access (hash-object, mktree, commit-tree, update-ref) without touching the working tree. The alternative is shelling out to `git`, which is the fallback if go-git's tree manipulation proves too awkward for E1.3's write mechanics.

**Smoke test:** Open an existing git repository via `git.PlainOpen()`, read HEAD, verify it returns a valid commit hash.

**Risk:** Tree manipulation complexity is the highest-risk item in Phase 0 (see E1.3). The smoke test should verify basic plumbing operations: create a blob, create a tree entry, read it back.

### 2. modernc.org/sqlite — Embedded SQLite

**Module:** `modernc.org/sqlite`

**Used by:** E5 (SQLite Materialization) — materialized view at `.git/opax/opax.db`, FTS5 full-text search.

**Why modernc.org/sqlite:** Pure Go SQLite implementation. No CGo, no native dependencies. The `mattn/go-sqlite3` alternative requires CGo and a C compiler at build time, breaking the single-binary constraint.

**Smoke test:** Open an in-memory database, create a table, insert a row, query it back. **Critically:** create an FTS5 virtual table and verify it works — FTS5 support is a Phase 0 risk (E5.3). If FTS5 doesn't work with modernc.org/sqlite, the fallback is `crawshaw.io/sqlite` with CGo (last resort, violates pure-Go constraint).

**Risk:** Medium. FTS5 support must be verified in this feature, not deferred to E5.

### 3. oklog/ulid — ID generation

**Module:** `github.com/oklog/ulid/v2`

**Used by:** FEAT-0002 (Core Domain Types) — all record IDs (`ses_`, `sav_`, plugin prefixes).

**Why ULID over UUID:** Lexicographically sortable (natural ordering in SQLite queries and directory listings), embeds a millisecond timestamp (extractable without a lookup), and is URL-safe. UUID v7 would also work but ULID has a more established Go library.

**Smoke test:** Generate a ULID with `crypto/rand` entropy, verify it parses back, verify timestamp extraction returns a time close to `time.Now()`, verify monotonic ordering (two ULIDs generated in the same millisecond sort correctly).

### 4. yaml.v3 — Configuration parsing

**Module:** `gopkg.in/yaml.v3`

**Used by:** FEAT-0003 (Configuration System) — parsing `config.yaml` files.

**Why yaml.v3:** Supports strict mode (rejects unknown keys when decoding into a struct with `KnownFields(true)`), handles anchors/aliases, well-maintained. The `yaml.v2` predecessor lacks strict mode.

**Smoke test:** Parse a YAML string into a struct. Verify strict mode rejects an unknown key. Verify a valid config round-trips correctly.

### 5. mcp-go — MCP SDK

**Module:** `github.com/mark3labs/mcp-go`

**Used by:** E10 (MCP Server) — stdio MCP server for web-only agent platforms.

**Why this library:** The MCP protocol is standardized JSON-RPC over stdio. This SDK provides typed request/response handling. If the library is immature or its API is unstable, a thin custom JSON-RPC implementation over stdio is feasible — the protocol surface for E10 is small (3 tools: `search_sessions`, `list_sessions`, `get_session`).

**Smoke test:** Import the package, verify it compiles. Instantiate the server type (without starting it) to confirm the API surface exists. This is a lighter verification than the others because E10 is far downstream.

**Risk:** Low-Medium. MCP Go SDK maturity is a known risk from the roadmap. The smoke test here is intentionally shallow — deep validation happens in E10.

---

## Implementation

### Steps

1. `go get` each dependency
2. Create `internal/smoke_test.go` (temporary, removed after E0 is complete) with one test function per dependency
3. Run `go mod tidy`
4. Run `CGO_ENABLED=0 go build ./cmd/opax/`
5. Run `go test ./internal/...`
6. Run `go vet ./...`

### Smoke Test File

`internal/deps_smoke_test.go` — a single test file verifying each dependency works. This file is temporary scaffolding; once downstream epics have real tests exercising these libraries, the smoke tests can be removed.

Test functions:
- `TestSmokeGoGit` — open a repo, read HEAD
- `TestSmokeSQLite` — open in-memory DB, create table, query
- `TestSmokeSQLiteFTS5` — create FTS5 virtual table, insert, MATCH query
- `TestSmokeULID` — generate, parse, extract timestamp
- `TestSmokeYAML` — parse into struct, strict mode rejection
- `TestSmokeMCPGo` — import compiles, server type instantiates

---

## Edge Cases

- **Transitive dependency conflicts** — `go-git` pulls in many transitive deps. Run `go mod graph` after adding all dependencies and check for version conflicts. Resolve with explicit `require` directives if needed.
- **Build tags** — some dependencies use build tags for platform-specific code. Verify `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64` and `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` both compile.
- **modernc.org/sqlite version** — pin to a version known to support FTS5. Check release notes or issues for FTS5-specific bugs.

---

## Acceptance Criteria

- [ ] `go mod tidy` succeeds with no errors
- [ ] `make build` produces `bin/opax`
- [ ] `CGO_ENABLED=0 go build ./cmd/opax/` succeeds (pure Go verified)
- [ ] `make test` passes including all smoke tests
- [ ] `make lint` (`go vet ./...`) reports no issues
- [ ] `TestSmokeSQLiteFTS5` passes — FTS5 works with modernc.org/sqlite
- [ ] `TestSmokeGoGit` passes — basic plumbing operations work
- [ ] `TestSmokeULID` passes — generation, parsing, timestamp extraction, monotonic ordering
- [ ] `TestSmokeYAML` passes — parsing and strict mode rejection
- [ ] `TestSmokeMCPGo` passes — package compiles and server type instantiates
- [ ] No CGo dependencies in the dependency tree (`go list -deps ./cmd/opax/ | grep -v vendor` shows no CGo packages)

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestSmokeGoGit` | go-git can open a repo and read HEAD | Returns a valid commit hash |
| `TestSmokeSQLite` | modernc.org/sqlite opens, creates table, queries | Row round-trips correctly |
| `TestSmokeSQLiteFTS5` | FTS5 virtual tables work | MATCH query returns inserted row |
| `TestSmokeULID` | ULID generation with crypto/rand | Parses back, timestamp ~now, monotonic |
| `TestSmokeYAML` | yaml.v3 strict mode | Known struct parses; unknown key errors |
| `TestSmokeMCPGo` | mcp-go compiles and instantiates | No panic, server object non-nil |
| `CGO_ENABLED=0 build` | Pure Go constraint | Build succeeds on darwin + linux |
