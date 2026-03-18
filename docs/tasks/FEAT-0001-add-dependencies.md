# FEAT-0001 — Add Dependencies: Task Breakdown

**Feature:** [FEAT-0001 — Add Dependencies](../features/FEAT-0001-add-dependencies.md)
**Status:** Completed

---

## Tasks

### T1 — Create feature branch
```bash
git checkout -b feat/FEAT-0001-add-dependencies
```
Done when: branch exists and is checked out.

---

### T2 — Add production dependencies

Run each `go get` individually so failures are isolated:

```bash
go get github.com/go-git/go-git/v5
go get modernc.org/sqlite
go get github.com/oklog/ulid/v2
go get gopkg.in/yaml.v3
go get github.com/mark3labs/mcp-go
go mod tidy
```

Done when: `go mod tidy` exits 0 and all five modules appear in `go.mod`.

---

### T3 — Create smoke test file

Create `internal/deps_smoke_test.go` with package `deps_smoke_test`.

One test function per dependency:

| Function | Dependency | What it checks |
|---|---|---|
| `TestSmokeGoGit` | `github.com/go-git/go-git/v5` | `git.PlainOpen("..")` returns repo; HEAD resolves to a valid commit hash |
| `TestSmokeSQLite` | `modernc.org/sqlite` | Open `:memory:` DB; `CREATE TABLE`, `INSERT`, `SELECT` round-trips |
| `TestSmokeSQLiteFTS5` | `modernc.org/sqlite` | Create FTS5 virtual table; insert row; `MATCH` query returns it |
| `TestSmokeULID` | `github.com/oklog/ulid/v2` | Generate with `crypto/rand`; parse back; timestamp within 5 s of `time.Now()`; monotonic ordering holds |
| `TestSmokeYAML` | `gopkg.in/yaml.v3` | Parse known struct; `KnownFields(true)` rejects unknown key |
| `TestSmokeMCPGo` | `github.com/mark3labs/mcp-go/server` | `server.NewMCPServer()` returns non-nil |

---

### T4 — Verify CGO_ENABLED=0 build

```bash
CGO_ENABLED=0 go build ./cmd/opax/
```

Done when: command exits 0 and `bin/opax` (or the output binary) exists.

---

### T5 — Run test suite

```bash
make test   # go test ./...
```

All tests must pass, including smoke tests.

---

### T6 — Run linter

```bash
make lint   # go vet ./...
```

Done when: exits 0 with no output.

---

### T7 — Commit

Single commit covering: `go.mod`, `go.sum`, `internal/deps_smoke_test.go`, and this task doc.

Commit message format:
```
feat: add dependencies for go-git, sqlite, ulid, yaml, and mcp-go (FEAT-0001)
```

---

## Acceptance Criteria

- [x] `go mod tidy` succeeds
- [x] `CGO_ENABLED=0 go build ./cmd/opax/` succeeds
- [x] `make test` passes (all six smoke tests green)
- [x] `make lint` (`go vet ./...`) reports no issues
- [x] `TestSmokeSQLiteFTS5` passes — FTS5 confirmed working with `modernc.org/sqlite`
- [x] `TestSmokeGoGit` passes — repo opens, HEAD resolves
- [x] `TestSmokeULID` passes — generation, parsing, timestamp, monotonic ordering
- [x] `TestSmokeYAML` passes — strict mode rejects unknown keys
- [x] `TestSmokeMCPGo` passes — server type instantiates without panic
