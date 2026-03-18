# Opax Implementation Roadmap

**Version:** 1.0.0-draft
**Date:** March 17, 2026
**Companion to:** Opax PRD v2.0.0

---

## Context

This roadmap defines the build order from current state through Phase 0 (MVP) and outlines Phases 1-3 at epic level. It will be broken into epics and features for execution.

**Phase 0 exit criteria (from PRD):** Developer uses Claude Code with passive capture. On commit, save is created with session metadata + transcript hash. `opax search "auth"` retrieves relevant sessions. Another agent in same repo gets same results. Storage compaction runs. Secret scrubbing catches API keys.

---

## Phase 0: Core SDK + Passive Capture + Memory Plugin + CLI

### Dependency Graph

```
E0: Foundation
 │
 E1: Git Plumbing
 ├── E2: Content-Addressed Storage
 │    └── E3: Hygiene Pipeline
 │         └── E4: Integrated Write Path
 │              ├── E5: SQLite Materialization
 │              │    ├── E6: Search & Query ──── E9: CLI Integration
 │              │    └── E7: Passive Capture
 │              │         └── E8: Memory Plugin
 │              │              ├── E10: MCP Server
 │              │              └── E11: Hooks & Init
 │              └────────────────── E12: Polish & Validation
```

**Critical path:** E0 → E1 → E2 → E3 → E4 → E5 → E6 (first demo: manual write + search)
**Full path:** ...→ E7 → E8 → E9 → E11 → E12 (passive capture loop working)

---

### Epic 0: Project Foundation

**Goal:** Types, config, dependencies. Nothing user-visible but everything downstream needs this.


| #    | Feature              | Description                                                                                                                                                                                                                |
| ---- | -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E0.1 | Add dependencies     | go-git, modernc.org/sqlite, oklog/ulid, yaml.v3, MCP Go SDK                                                                                                                                                                |
| E0.2 | Core domain types    | `internal/types/` — record ID types (ses_, sav_), Hygiene metadata on Session/Save, SessionMetadata (includes files_touched), SaveMetadata (sessions array with attribution), NoteContent, enums (ScrubMode, AttrReason), ULID generation helper. Plugin ID prefixes (wrk_, act_) registered at plugin load |
| E0.3 | Configuration system | `internal/config/` — OpaxConfig struct (hygiene, storage, capture, trailers), single `config.yaml` with hierarchy (SDK defaults → team `.opax/config.yaml` → personal `~/.config/opax/config.yaml`), strict validation |
| E0.4 | File lock utility    | `internal/lock/` — .git/opax.lock for write serialization, advisory locking with timeout, deferred cleanup                                                                                                                 |


---

### Epic 1: Git Plumbing Layer

**Goal:** Read/write to `opax/v1` orphan branch and git notes via go-git plumbing. Never touch working tree. **Riskiest epic** — tree manipulation is the hardest code.


| #    | Feature                  | Description                                                                                                                                                                                                                                                                    |
| ---- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| E1.1 | Repo discovery           | Open git repo via go-git, validate, locate .git/, create .git/opax/                                                                                                                                                                                                            |
| E1.2 | Orphan branch mgmt       | Create `opax/v1` if absent (first commit with version marker), read current tip, idempotent                                                                                                                                                                                    |
| E1.3 | Write records to branch  | **Hardest task.** hash-object → mktree → commit-tree → update-ref. Shard directory (first 2 hex chars of sha256(record_id), 256 buckets). Build full tree from current tip + new subtree. Acquire .git/opax.lock. Fallback: shell out to git plumbing if go-git is too awkward |
| E1.4 | Read records from branch | Navigate tree at branch tip to shard/id path, read blob contents                                                                                                                                                                                                               |
| E1.5 | Git notes operations     | Write/read JSON notes under namespaces (refs/opax/notes/sessions, etc.), handle missing notes ref                                                                                                                                                                              |
| E1.6 | Commit trailer support   | Write `Opax-Save` trailer via prepare-commit-msg hook. Parse trailers from existing commits. Default session linkage mechanism                                                                                                                                                                                             |
| E1.7 | Refspec configuration    | Generate refspec config for .git/config — push notes refs, exclude opax/v1 from default fetch                                                                                                                                                                                  |


---

### Epic 2: Content-Addressed Storage

**Goal:** `internal/cas/` — store/retrieve bulk content by SHA-256 hash at `.git/opax/content/`.


| #    | Feature              | Description                                                                             |
| ---- | -------------------- | --------------------------------------------------------------------------------------- |
| E2.1 | CAS write            | SHA-256 hash, shard to .git/opax/content/{hash[0:2]}/{hash[2:]}, skip if exists (dedup) |
| E2.2 | CAS read             | Retrieve by hash, optional integrity verification (recompute + compare)                 |
| E2.3 | Directory management | Ensure dirs exist, create shards on demand, stats (count, total size)                   |
| E2.4 | Size threshold logic | 4 KB threshold utility: inline on branch (< 4KB) or CAS (>= 4KB)                        |


---

### Epic 3: Hygiene Pipeline

**Goal:** `internal/hygiene/` — secret detection and scrubbing on all content before storage.


| #    | Feature                | Description                                                                                                                      |
| ---- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| E3.1 | Detector interface     | Detector interface (Detect → []Finding), registry, config-driven loading                                                         |
| E3.2 | Built-in detectors (6) | AWS keys, GitHub tokens, JWTs, PEM private keys, connection strings, generic API keys                                            |
| E3.3 | Entropy detection      | Shannon entropy calculator, configurable threshold (default 4.5), min length 20                                                  |
| E3.4 | Source file scanning   | Read .env/.env.local, extract key-value pairs, flag exact matches in content                                                     |
| E3.5 | Allowlist filtering    | Exact strings + regex patterns, applied after detection before scrubbing                                                         |
| E3.6 | Scrubbing action       | Redact (default: `[REDACTED:{detector}]`), reject (error), warn (pass through + log). Returns scrubbed content + hygiene metadata |
| E3.7 | Pipeline orchestrator  | `Scrub(content, config) → (scrubbed, Hygiene, error)`. Order: source scan → pattern match → entropy → allowlist → scrub  |


---

### Epic 4: Integrated Write Path

**Goal:** Compose git + CAS + hygiene into a single write operation. The actual Opax storage pipeline.


| #    | Feature               | Description                                                                                                                                                          |
| ---- | --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E4.1 | Write orchestrator    | Accept record + content → scrub → size threshold → CAS or inline → set content_hash + hygiene metadata → serialize → write to orphan branch. All under .git/opax.lock |
| E4.2 | Session archive write | sessions/{shard}/{id}/ with metadata.json + summary.md. Transcript → CAS (always large). Generate ses_ ULID                                                          |
| E4.4 | save write            | saves/{shard}/{id}/ with metadata.json. Link to commit hash + session ID. Generate sav_ ULID                                                                         |
| E4.5 | Commit linkage        | Attach `Opax-Save` trailer (default) or session-link note (fallback when --no-trailers) to commit after save creation. Save fans out to sessions via many-to-many linkage                                                           |


---

### Epic 5: SQLite Materialization

**Goal:** `internal/store/` — SQLite at `.git/opax/opax.db` as materialized view of git data. FTS5 search.


| #    | Feature                  | Description                                                                                                                                                                              |
| ---- | ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E5.1 | DB initialization        | Open/create at .git/opax/opax.db, WAL mode, foreign keys, page size                                                                                                                      |
| E5.2 | Core schema              | Tables: opax_sessions, opax_session_tags, opax_saves, opax_notes, opax_materializer_state. All indexes. Idempotent (IF NOT EXISTS). Plugins create views over opax_notes, not new tables |
| E5.3 | FTS5 setup               | Virtual table opax_sessions_fts. AFTER INSERT/DELETE triggers. **Verify FTS5 works with modernc.org/sqlite early**                                                                       |
| E5.4 | StorageBackend interface | InitSchema, Query, QueryOne, Execute, Search, Sync, Rebuild, Transaction. SearchOptions + SearchResult types                                                                             |
| E5.5 | SQLite adapter           | Implement StorageBackend against modernc.org/sqlite. FTS5 MATCH, json_extract, transactions                                                                                              |
| E5.6 | Full rebuild             | Walk all commits on opax/v1, parse metadata.json files, insert into tables, walk notes refs, update materializer_state. The "always rebuildable from git" guarantee                      |
| E5.7 | Incremental sync         | Compare current HEAD vs stored git_head, walk only new commits, materialize new records                                                                                                  |
| E5.8 | Dirty flag mechanism     | Write: touch .git/opax/dirty. Read: check flag → incremental sync → remove flag. No daemon needed                                                                                        |


---

### Epic 6: Search & Query

**Goal:** Make `opax search` and `opax session list/get` work. **First demo milestone.**


| #    | Feature                  | Description                                                                                                           |
| ---- | ------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| E6.1 | SearchStrategy interface | search_mode field (keyword/semantic/hybrid). Phase 0: FTS5Strategy only                                               |
| E6.2 | FTS5 search              | Query opax_sessions_fts. Ranked results with snippets. Filters: agent, branch, tags, date range, --limit              |
| E6.3 | `opax search` command    | Wire CLI stub → FTS5 search. Text output (default) + JSON (--json). Show id, agent, branch, tags, created_at, snippet |
| E6.4 | `opax session list`      | Query opax_sessions with filters, pagination. Table or JSON output                                                    |
| E6.5 | `opax session get`       | Query by ID, fetch summary + transcript from CAS if content_hash set. Metadata + content output                       |


---

### Epic 7: Passive Capture Engine

**Goal:** `internal/capture/` — read agent session files after agent writes them, normalize to common format.


| #    | Feature                  | Description                                                                                                                                             |
| ---- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E7.1 | Common transcript format | NormalizedSession struct: agent, model, timestamps, duration, messages, files_changed, commits. NormalizedMessage: role, content, timestamp, tool_calls |
| E7.2 | CaptureSource interface  | Detect() bool, ReadSessions(since time.Time) → []NormalizedSession                                                                                      |
| E7.3 | Claude Code reader       | Read JSONL from ~/.claude/projects/{hash}/. Parse messages, extract model/timestamps/tool usage. Handle partial sessions                                |
| E7.4 | Codex reader             | Read Codex session logs. Lower fidelity initially until format is better understood                                                                     |
| E7.5 | Capture coordinator      | Enumerate sources, call Detect/ReadSessions, track last capture timestamp per source                                                                    |
| E7.6 | Transcript summarization | Simple extraction: first user message as title, key stats, files changed. No LLM in Phase 0                                                             |


---

### Epic 8: Memory Plugin

**Goal:** `plugins/memory/` — the primary value plugin tying capture, storage, and query together.


| #    | Feature              | Description                                                                                                      |
| ---- | -------------------- | ---------------------------------------------------------------------------------------------------------------- |
| E8.1 | OpaxPlugin interface | Define in internal/plugin: Name, Namespace, RegisterViews, RegisterCLI, RegisterMCP. Implement in plugins/memory |
| E8.2 | Session archive ops  | ArchiveSession (full write path), GetSession, ListSessions, SearchSessions (FTS5)                                |
| E8.3 | Save creation        | CreateSave — build save with session attribution (file overlap primary, temporal proximity secondary). Attach `Opax-Save` trailer or note fallback. Called from post-commit hook                           |


---

### Epic 9: CLI Integration

**Goal:** Wire all commands to real implementations. Remove every "not yet implemented" message.


| #    | Feature               | Description                                                                                                                                          |
| ---- | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| E9.1 | `opax init`           | Validate git repo, create .git/opax/ + content/, init SQLite, create orphan branch, configure refspecs (`refs/opax/*`), install hooks (incl. prepare-commit-msg for trailers), generate default config.yaml |
| E9.2 | `opax db rebuild`     | Call StorageBackend.Rebuild(), show progress, output summary                                                                                         |
| E9.3 | `opax storage stats`  | Record counts by type, git tier size, CAS size, DB size. Table + --json                                                                              |
| E9.4 | `opax doctor`         | Health checks: git repo?, branch exists?, DB accessible?, in sync?, hooks installed?, config.yaml?, CAS writable? Pass/warn/fail indicators          |
| E9.5 | `opax search` wiring  | Ensure lazy sync fires before search if dirty. Handle empty DB gracefully                                                                            |
| E9.6 | `opax session` wiring | Ensure JSON output matches MCP tool format for consistency                                                                                           |


---

### Epic 10: MCP Server

**Goal:** `internal/mcp/` — stdio MCP server with session query tools for web-only platforms.


| #     | Feature              | Description                                                                      |
| ----- | -------------------- | -------------------------------------------------------------------------------- |
| E10.1 | Server scaffolding   | MCP SDK or thin custom JSON-RPC. stdio transport. Init/handle/shutdown lifecycle |
| E10.2 | search_sessions tool | Search query + filters → SearchSessions → ranked results with snippets           |
| E10.3 | list_sessions tool   | Optional filters → ListSessions → session list                                   |
| E10.4 | get_session tool     | session_id → GetSession → full metadata + summary                                |
| E10.5 | `opax mcp` command   | CLI command that starts MCP server (for MCP settings: `"command": "opax mcp"`)   |


---

### Epic 11: Hooks & Init Lifecycle

**Goal:** Post-commit capture flow that makes passive capture automatic.


| #     | Feature                      | Description                                                                                                                                                      |
| ----- | ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E11.1 | Hook wrapper scripts         | post-commit wrapper: backup pre-existing hook as .pre-opax, run original first, then `opax capture --post-commit` async (fire-and-forget). Detect husky/lefthook |
| E11.2 | post-merge dirty flag        | Install post-merge hook that touches .git/opax/dirty                                                                                                             |
| E11.3 | `opax capture --post-commit` | Hidden command invoked by hook. Detect sources → read sessions → scrub → write → create save. Trailers already attached by prepare-commit-msg; note fallback if --no-trailers |
| E11.4 | --no-hooks flag              | Skip hook installation on init. Fallback to explicit MCP calls                                                                                                   |


---

### Epic 12: Polish & Validation

**Goal:** Meet all Phase 0 exit criteria. Integration tests. Error handling.


| #     | Feature              | Description                                                                                                              |
| ----- | -------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| E12.1 | E2E integration test | Full exit criteria scenario: init → simulate session → commit → verify save → search → verify results → verify scrubbing |
| E12.2 | Basic compaction     | `opax storage compact` — run git gc on opax data, report before/after. No tiered archival yet                            |
| E12.3 | Error handling       | Clear messages for: not a git repo, not initialized, empty DB, no results. Consistent formatting                         |
| E12.4 | Setup guides         | README quickstart, Claude Code integration guide, Codex integration guide                                                |


---

## Phase 0 Parallelization Opportunities

- **E2 (CAS) ∥ E3 (Hygiene)** — independent, both depend only on E0
- **E7 (Capture readers) ∥ E1-E5** — capture readers can be built/tested against sample files while storage layer is built
- **E10 (MCP) ∥ E11 (Hooks)** — independent, both depend on E8

---

## Phase 0 Risk Register


| Risk                                            | Impact     | Mitigation                                                                 |
| ----------------------------------------------- | ---------- | -------------------------------------------------------------------------- |
| go-git tree manipulation complexity (E1.3)      | High       | Prototype early. Fallback: shell out to git plumbing commands              |
| modernc.org/sqlite FTS5 support (E5.3)          | Medium     | Verify early. Fallback: crawshaw.io/sqlite with CGo                        |
| Claude Code JSONL format changes (E7.3)         | Medium     | Version-detect format, build with flexibility                              |
| Codex session format undocumented (E7.4)        | Medium     | Accept lower fidelity initially                                            |
| MCP Go SDK maturity (E10.1)                     | Low-Medium | Protocol is simple JSON-RPC; thin custom impl is feasible                  |
| Orphan branch tree rebuild perf at scale (E1.3) | Medium     | Benchmark at 1000+ records. Sharded dirs help. Cache tree object if needed |


---

## Phase 1: Workflows + Evals + Encryption + Basic Compliance


| Epic                    | Scope                                                                                                                                          |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| P1.1 Workflows Plugin   | YAML workflow format, state machine (pending→running→completed/failed), git-event triggers, stage dispatch, human approval gates, CLI commands |
| P1.2 Evals Plugin       | Eval note format/schema, LLM-as-judge framework (thin), git note attachment, CLI commands                                                      |
| P1.3 Executor Plugins   | Local process executor, Docker executor, executor interface for Phase 2 remote                                                                 |
| P1.4 Encryption at Rest | age library, per-tier recipient keys, file-level encryption (content only, metadata plaintext), transparent decryption in read path            |
| P1.5 Basic Compliance   | Article 12 evidence packages, session counts, agent summaries, human oversight records, `opax compliance report`                               |
| P1.6 Additional Capture | Cursor session reader, Gemini CLI session reader                                                                                               |


---

## Phase 2: Remote Execution + Web Control Plane


| Epic                  | Scope                                                                                         |
| --------------------- | --------------------------------------------------------------------------------------------- |
| P2.1 Remote Executors | E2B sandbox executor, GitHub Actions executor, result collection                              |
| P2.2 Studio Local     | `opax studio` temp local server, reads SQLite, session timeline + browser + search UI |
| P2.3 Postgres Backend | StorageBackend Postgres adapter, tsvector/tsquery FTS, JSONB + GIN indexes                    |
| P2.4 Studio Hosted    | Always-on dashboard, cross-repo views, SSO/RBAC, webhook notifications                        |
| P2.5 First Adapters   | LangGraph adapter, GitHub Actions adapter                                                     |


---

## Phase 3: Ecosystem + Compliance + Polish


| Epic                     | Scope                                                                        |
| ------------------------ | ---------------------------------------------------------------------------- |
| P3.1 Git Data Spec v2.0  | Extension guidelines, plugin registry/discovery                              |
| P3.2 Full Compliance     | EU AI Act full evidence, NIST AI RMF, ISO 42001, compliance-as-code policies |
| P3.3 Semantic Search     | Local embeddings, SemanticStrategy, hybrid search                            |
| P3.4 Additional Adapters | Temporal, Braintrust, Langfuse adapters                                      |
| P3.5 Team Features       | Shared workflow configs, notification channels, cross-team dashboards        |


---

## Key Files to Modify


| File                                        | Epic | Role                                        |
| ------------------------------------------- | ---- | ------------------------------------------- |
| `cmd/opax/main.go`                          | E9   | CLI entry point, all command wiring         |
| `internal/git/git.go`                       | E1   | Orphan branch + notes + refs (hardest code) |
| `internal/store/store.go`                   | E5   | SQLite materialization + StorageBackend     |
| `internal/cas/cas.go`                       | E2   | Content-addressed storage                   |
| `internal/capture/capture.go`               | E7   | Capture coordinator                         |
| `internal/capture/claudecode/claudecode.go` | E7   | Claude Code JSONL reader                    |
| `internal/capture/codex/codex.go`           | E7   | Codex session reader                        |
| `internal/hygiene/hygiene.go`               | E3   | Secret scrubbing pipeline                   |
| `internal/plugin/plugin.go`                 | E8   | Plugin interface + loading                  |
| `internal/mcp/mcp.go`                       | E10  | MCP server                                  |
| `plugins/memory/memory.go`                  | E8   | Memory plugin (primary value)               |


**New files to create:**

- `internal/types/types.go` (E0.2)
- `internal/config/config.go` (E0.3)
- `internal/lock/lock.go` (E0.4)

---

## Verification

Phase 0 is verified by running the E2E integration test (E12.1):

1. `opax init` in a test repo
2. Simulate Claude Code session (write sample JSONL)
3. `git commit` — post-commit hook fires, creates save + session archive
4. `opax search "auth"` — returns matching sessions with snippet
5. Simulate Codex session, same repo — `opax search` returns same results
6. `opax storage stats` — shows record counts and sizes
7. Verify content containing `AKIA...` test key shows `[REDACTED:aws_key]`

