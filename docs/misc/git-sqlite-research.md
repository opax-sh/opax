# Research: Opportunities at the Git + SQLite Intersection

## Context

Opax's architecture uses **git as an event store** (orphan branch, notes, trailers, CAS for bulk content) and **SQLite as a materialized query layer** (FTS5, structured queries). The current plan uses **go-git** + **modernc.org/sqlite** as separate libraries connected by a CQRS/event-sourcing pattern. The question: is there innovation to be done here, or are the current options the best ones?

---

## Landscape Survey

### Projects That Use Git as a Database


| Project              | Language   | What It Does                                                                                                | Relevance to Opax                                                                                                                    |
| -------------------- | ---------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Dolt**             | Go         | MySQL-compatible SQL DB with git semantics (branch, merge, diff). Uses Prolly Trees — NOT actual git format | Proves "versioned SQL" is viable. But Dolt replaced git internals with custom storage — Opax uses real git, which is the whole point |
| **gitgres/omni_git** | C/PL/pgSQL | Stores git objects in Postgres tables, supports `git push/clone` via libgit2 backend                        | Worth evaluating for Phase 2 hosted tier (mentioned in PRD). Key finding: **20x storage bloat without delta compression**            |
| **ChronDB**          | Clojure    | Chronological KV database on git internals. Recently added Postgres wire protocol                           | Most conceptually similar to Opax. Validates the approach but limited scope                                                          |
| **GQL/GitQL**        | Rust       | SQL-like query language against .git files. On-the-fly, no materialization                                  | Validates the desire for SQL-over-git. Performance confirms materialization is better for analytical queries                         |
| **gitbase**          | Go         | SQL over git repos via go-git + go-mysql-server. Dead (source{d} shut down)                                 | Cautionary tale: ambitious scope, company died. Also confirms go-git performance issues at scale                                     |
| **Irmin**            | OCaml      | Mergeable, branchable data structures. Academic gold (used by Tezos blockchain)                             | Foundational "Mergeable Persistent Data Structures" paper. Interesting for theory, not practical for Opax                            |
| **Fossil**           | C          | SCM with built-in wiki, bug tracker, forum — all in a single SQLite database                                | Proves "development tool with embedded SQLite" works. But Fossil replaces git; Opax builds on it                                     |


### SQLite + Git Integrations


| Project                       | What It Does                                                                                                                                                      | Relevance                                                                                                       |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| **mergestat-lite / gitqlite** | SQLite virtual tables exposing git objects (commits, files, refs) as SQL tables                                                                                   | **Directly relevant.** Proved the concept in Go. mergestat used libgit2; gitqlite used go-git                   |
| **gitoxide `ein tool query`** | Materializes git data into SQLite at `.git/ein.query`. ~250 bytes/commit, ~281MB for Linux kernel's 1.1M commits. Initial build: ~16 min, subsequent queries: <1s | **Strongest validation of Opax's approach.** gitoxide independently arrived at the same architecture            |
| **libgit2-backends/sqlite**   | Official pluggable ODB storing git objects in SQLite tables                                                                                                       | Reference implementation for git objects IN SQLite. Different direction (SQLite as git backend vs. query layer) |
| **sql.js-httpvfs**            | HTTP Range requests to query remote SQLite — fetches ~24KB per query on 1GB database                                                                              | Could be relevant for hosted tier read path                                                                     |
| **branch_base**               | Ruby — syncs git repo data INTO SQLite for querying                                                                                                               | Validates approach but far simpler than Opax                                                                    |


### Content-Addressed Storage


| Project                  | Relevance                                                                                       |
| ------------------------ | ----------------------------------------------------------------------------------------------- |
| **Dolt Prolly Trees**    | Content-addressed B-trees with probabilistic chunking. Impressive but overkill — git IS the CAS |
| **bobg/hashsplit**       | Content-defined chunking in Go. Evaluated in detail below                                       |
| **Perkeep (Camlistore)** | Three-layer architecture (CAS → schema → search) mirrors Opax's tiered model                    |


---

## Analysis: Where Innovation Is and Isn't

### The current plan is validated — and validated hard

The git-as-event-store + SQLite-as-materialized-view pattern has been independently discovered by:

- **gitoxide** (`ein tool query`) — same architecture, proven at Linux kernel scale
- **mergestat/gitqlite** — virtual table approach that proved materialization is better for analytics
- **ChronDB** — chronological KV store on git internals
- **Entire.io** — same orphan branch pattern for agent data

**go-git + modernc.org/sqlite is the right choice for Phase 0.** Pure Go, single binary, no cgo. The libraries are mature. The CQRS pattern between them is sound.

### Where innovation IS NOT worth pursuing

1. **Git-backed SQLite VFS** — Read-write VFS is just materialization with extra steps. SQLite needs fixed-size pages with random-access; git objects are immutable blobs. Fundamental impedance mismatch.
2. **Replacing go-git** — go-git's `Storer` interface already provides exactly the right abstraction. Custom storage backends exist.
3. **SQL over git objects directly (skip materialization)** — Virtual tables (gitqlite, mergestat) proved the concept but hit walls: no indexing, bad JOIN performance, no statistics for query planner. Materialization is strictly better for analytical queries.
4. **Dolt-style Prolly Trees** — They exist because Dolt needed to REPLACE git's storage. Opax uses git AS-IS. Different problem.

---

## Deep Dive: Content-Defined Chunking for the CAS Tier

### Verdict: CDC is not worth it. Use zstd compression instead.

### The math

At Opax's scale (1.2 GB/month CAS for a 5-dev team, ~~100KB text files, 30-50% repetitive content), CDC with 8KB chunks yields **~~5-20% net dedup savings** after manifest and filesystem overhead. That is 60-240 MB/month saved — against substantial implementation complexity.

### Go CDC libraries evaluated


| Library                               | Algorithm                                  | Throughput   | Best For                           | Phase 0 Fit               |
| ------------------------------------- | ------------------------------------------ | ------------ | ---------------------------------- | ------------------------- |
| **bobg/hashsplit** v2                 | Rolling hash, `SplitBits=13` (~8KB chunks) | Moderate     | Text files, hierarchical manifests | Best if CDC were adopted  |
| **restic/chunker** v0.4               | Rabin fingerprint                          | 477-523 MB/s | Binary backup dedup                | Overkill for text         |
| **PlakarKorp/go-cdc-chunkers** v1.0.3 | FastCDC/UltraCDC                           | 9-13 GB/s    | Large files (256KB+ chunks)        | Wrong chunk size for Opax |


### Why CDC loses to simpler alternatives


| Phase           | Approach                                                                | Monthly CAS (5 devs)                      | Complexity                          |
| --------------- | ----------------------------------------------------------------------- | ----------------------------------------- | ----------------------------------- |
| **Phase 0**     | zstd compression (no dictionary)                                        | **~400 MB** (from 1.2 GB, ~67% reduction) | **~20 LOC**                         |
| **Post-launch** | + Transcript normalization (strip repeated system prompts/tool schemas) | ~320-360 MB                               | Medium                              |
| **If needed**   | + zstd dictionary compression                                           | **~150-200 MB** (~85-87% reduction)       | Low-medium                          |
| **Never**       | CDC                                                                     | ~130-180 MB                               | Very high (marginal gain over dict) |


**Key numbers:**

- zstd on text: 60-70% compression without dictionary, 85-90% with trained dictionary
- CDC overhead: ~0.2ms compute per 100KB file, but 13x filesystem operations per write
- Per-chunk metadata cost: ~40 bytes manifest entry + ~4KB filesystem block minimum
- Transcript normalization alone: 10-20% savings by stripping repeated system prompts

### Recommendation

**Phase 0:** Add zstd compression to CAS writes. `klauspost/compress/zstd` is the standard Go library — fast, well-maintained, ~20 lines of integration code. Compresses 1.2 GB/month → ~400 MB/month.

**Later if needed:** Train a zstd dictionary on a corpus of agent transcripts. This exploits the repetitive structure (system prompts, tool call formats, common code patterns) far more effectively than CDC, achieving 85-87% compression with near-zero complexity.

**Skip CDC entirely.** The marginal gain over dictionary compression doesn't justify the manifest format, chunk store, reassembly logic, and migration path.

---

## Deep Dive: SQLite Virtual Tables with modernc.org/sqlite

### Verdict: Now viable. Worth experimenting with in Phase 0.

### The big news: modernc.org/sqlite supports virtual tables

As of **v1.45.0 (released 2026-02-09)**, modernc.org/sqlite has a `vtab` subpackage with a complete Go API. This is new — only 5 weeks old. The current version is v1.47.0.

### API

```go
import "modernc.org/sqlite/vtab"

// Registration (global, applies to all new connections)
vtab.RegisterModule(db *sql.DB, name string, m Module) error
```

**Three core interfaces:**

- `**Module`** — `Create(ctx, args)` and `Connect(ctx, args)` returning a `Table`
- `**Table`** — `BestIndex(*IndexInfo)`, `Open() Cursor`, `Disconnect()`, `Destroy()`
- `**Cursor**` — `Filter(idxNum, idxStr, vals)`, `Next()`, `Eof()`, `Column(col)`, `Close()`

Full `BestIndex` planning with constraint push-down and `ColUsed` bitmask. Optional `Updater`, `Transactional`, and `Renamer` interfaces on Table.

### Go SQLite library comparison (virtual table support)


| Library                     | cgo?               | Virtual Tables       | Registration   | Notes                                                                   |
| --------------------------- | ------------------ | -------------------- | -------------- | ----------------------------------------------------------------------- |
| **modernc.org/sqlite/vtab** | No (pure Go)       | Full (since v1.45.0) | Global         | **Opax's current choice. New but API maps 1:1 to SQLite's C interface** |
| **ncruces/go-sqlite3**      | No (WASM+wazero)   | Full (mature)        | Per-connection | Generics-based API. Good fallback                                       |
| **zombiezen.com/go/sqlite** | No (wraps modernc) | Full (mature)        | Per-connection | Best query perf of any pure-Go option                                   |
| **mattn/go-sqlite3**        | Yes (cgo)          | Full (battle-tested) | Per-connection | What mergestat/gitqlite used. Requires cgo                              |


### Benchmark snapshot (go-sqlite-bench, 2025-08)


| Driver      | Simple Insert (ms) | Simple Query (ms) | Real-world Query (ms) |
| ----------- | ------------------ | ----------------- | --------------------- |
| mattn (cgo) | 1531               | 1018              | 120                   |
| modernc     | 5288               | 760               | 130                   |
| ncruces     | 3046               | 910               | 127                   |
| zombiezen   | 1791               | 264               | 59                    |


### Proposed hybrid architecture for Opax

**Materialized tables** (heavy analytical queries — unchanged):

- `opax_sessions`, `opax_saves`, `opax_notes`
- FTS5 indexes for full-text search
- Synced from git via dirty-flag + lazy materialization

**Virtual tables** (lightweight, always-current operational queries — new):

- `opax_vtab_refs` — current git refs, no materialization needed
- `opax_vtab_active_workflows` — plugin-owned; reads from `refs/opax/workflows/active` directly
- `opax_vtab_plugins` — registered plugin state

Benefits:

- Operational queries are always accurate (no stale-cache risk)
- No sync overhead for fast-changing state
- Clean separation: materialized = historical/analytical, virtual = current/operational

### Design principle

Implement go-git iteration logic as standalone structs with clean interfaces. The vtab wiring is thin adapter code (~200-300 lines per table). If modernc's vtab proves buggy (it's 5 weeks old), swap to zombiezen.com/go/sqlite (`SetModule`, per-connection) with minimal changes.

**Escape hatch:** If virtual tables prove problematic, populate real tables on-demand. At Opax's scale (most repos <100K commits, operational state is tiny), on-demand refresh takes <10ms.

---

## Summary: What's Changed from the Original Plan


| Area                   | Original Plan                  | Updated Recommendation                                                                 |
| ---------------------- | ------------------------------ | -------------------------------------------------------------------------------------- |
| **Core libraries**     | go-git + modernc.org/sqlite    | **No change.** Validated by multiple independent projects                              |
| **CAS storage**        | Raw files addressed by SHA-256 | **Add zstd compression** (~20 LOC, 67% size reduction). Skip CDC                       |
| **Query architecture** | All materialized tables        | **Hybrid: materialized + virtual tables** for operational state. Experiment in Phase 0 |
| **Innovation focus**   | Library-level                  | **Spec-level.** The git data spec for agent activity is the real innovation            |


### Phase 0 action items

1. **Use go-git + modernc.org/sqlite as planned** — validated, no better alternative
2. **Add zstd compression to CAS writes** — `klauspost/compress/zstd`, trivial integration, 67% storage savings
3. **Experiment with modernc.org/sqlite/vtab** for `opax_vtab_refs` — small scope, validates the vtab API, provides always-current ref queries
4. **Focus innovation energy on the git data spec** — that's the defensible moat, not the plumbing

---

## Key References


| Resource                                                                                                                            | Why It Matters                                                                                       |
| ----------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| gitoxide `ein tool query` ([discussion #767](https://github.com/GitoxideLabs/gitoxide/discussions/767))                             | Strongest architectural validation — same pattern at Linux kernel scale                              |
| mergestat-lite ([GitHub](https://github.com/mergestat/mergestat-lite))                                                              | Proves Go + SQLite virtual tables over git works                                                     |
| modernc.org/sqlite/vtab ([pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite@v1.47.0/vtab))                                          | New pure-Go virtual table API — enables hybrid architecture                                          |
| klauspost/compress/zstd                                                                                                             | Standard Go zstd — the right tool for CAS compression                                                |
| Dolt Prolly Trees ([docs](https://docs.dolthub.com/architecture/storage-engine/prolly-tree))                                        | Reference if Opax ever needs custom CAS indexing                                                     |
| Andrew Nesbitt on package managers + git ([blog](https://nesbitt.io/2025/12/24/package-managers-keep-using-git-as-a-database.html)) | Cautionary tale: git-as-database fails when distributed over network. Opax's local-first avoids this |
| gitgres 20x storage bloat ([blog](https://nesbitt.io/2026/02/26/git-in-postgres.html))                                              | Important for Phase 2 hosted tier planning                                                           |
| go-sqlite-bench ([GitHub](https://github.com/cvilsmeier/go-sqlite-bench))                                                           | Performance comparison of pure-Go SQLite libraries                                                   |
| Perkeep (Camlistore)                                                                                                                | Three-layer architecture (CAS → schema → search) mirrors Opax's tiered model                         |


