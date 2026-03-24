# Opax Implementation Roadmap

**Version:** 1.0.0-draft
**Date:** March 17, 2026
**Companion to:** Opax PRD v2.0.0

---

## Context

This roadmap defines the build order from current state through Phase 0 (MVP) and outlines Phases 1-5 at epic level. It will be broken into epics and features for execution.

### Vision Roadmap

The full product arc, from CLI to platform. See `overview.md` for detailed rationale.

```
Phase 0: CLI + Passive Capture + Memory          ← distribution (free, open-source)
Phase 1: Workflows + Evals + Executors            ← orchestration foundation
Phase 2: Studio + Remote Execution + Postgres     ← first revenue (team subscriptions)
Phase 3: Ecosystem + Compliance + Adapters        ← ecosystem + enterprise
Phase 4: Intelligence Layer                       ← moat (cross-repo memory, quality scoring)
Phase 5: Ecosystem & Generalization               ← platform (marketplace, multi-language SDKs)
```

Memory and orchestration are inseparable — neither is useful alone at scale. Phase 0 ships memory. Phase 1 adds orchestration. Together they form the core value prop: agents that coordinate AND learn from each other's sessions.

Git is the coordination substrate throughout: branches are work units, commits are stage gates, hooks are transitions, PRs are review gates. Execution is pluggable from Phase 1 onward — agents can run in Docker, CI, cloud sandboxes, serverless, etc. via thin executor drivers.

### Current implementation snapshot (repo)

- Implemented foundations: dependencies, `internal/types`, `internal/config`, `internal/lock`, and related tests.
- Package scaffolds exist for `git`, `store`, `cas`, `capture`, `hygiene`, `mcp`, `plugin`, and `plugins/memory`.
- CLI surface exists, but non-version commands are currently stubs.
- Current command namespace for session-oriented commands is `opax session ...`.

### Task Tracking

Status legend used throughout this roadmap:

- `completed` — implemented and verified in repo
- `in_progress` — actively being built
- `planned` — scoped but not started
- `blocked` — cannot proceed due to dependency/risk

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

### EPIC-0000: Project Foundation

**Status:** completed

**Goal:** Types, config, dependencies. Nothing user-visible but everything downstream needs this.


| Feature ID | Feature              | Status    | Description                                                                                                                                                                                                                                                                                                 |
| ---------- | -------------------- | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0001  | Add dependencies     | completed | go-git, modernc.org/sqlite, oklog/ulid, yaml.v3, MCP Go SDK                                                                                                                                                                                                                                                 |
| FEAT-0002  | Core domain types    | completed | `internal/types/` — record ID types (ses_, sav_), Hygiene metadata on Session/Save, SessionMetadata (includes files_touched), SaveMetadata (sessions array with attribution), NoteContent, enums (ScrubMode, AttrReason), ULID generation helper. Plugin ID prefixes (wrk_, act_) registered at plugin load |
| FEAT-0003  | Configuration system | completed | `internal/config/` — OpaxConfig struct (hygiene, storage, capture, trailers), single `config.yaml` with hierarchy (SDK defaults → team `.opax/config.yaml` → personal `~/.config/opax/config.yaml`), strict validation                                                                                      |
| FEAT-0004  | File lock utility    | completed | `internal/lock/` — .git/opax.lock for write serialization, advisory locking with timeout, deferred cleanup                                                                                                                                                                                                  |


---

### EPIC-0001: Git Plumbing Layer

**Status:** planned

**Goal:** Read/write to `opax/v1` orphan branch and git notes via go-git plumbing. Never touch working tree. **Riskiest epic** — tree manipulation is the hardest code.


| Feature ID | Feature                  | Status  | Description                                                                                                                                                                                                                                                                    |
| ---------- | ------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| FEAT-0005  | Repo discovery           | planned | Open git repo via go-git, validate, locate .git/, create .git/opax/                                                                                                                                                                                                            |
| FEAT-0006  | Orphan branch mgmt       | planned | Create `opax/v1` if absent (first commit with version marker), read current tip, idempotent                                                                                                                                                                                    |
| FEAT-0007  | Write records to branch  | planned | **Hardest task.** hash-object → mktree → commit-tree → update-ref. Shard directory (first 2 hex chars of sha256(record_id), 256 buckets). Build full tree from current tip + new subtree. Acquire .git/opax.lock. Fallback: shell out to git plumbing if go-git is too awkward |
| FEAT-0008  | Read records from branch | planned | Navigate tree at branch tip to shard/id path, read blob contents                                                                                                                                                                                                               |
| FEAT-0009  | Git notes operations     | planned | Write/read JSON notes under namespaces (refs/opax/notes/sessions, etc.), handle missing notes ref                                                                                                                                                                              |
| FEAT-0010  | Commit trailer support   | planned | Preallocate save IDs and upsert `Opax-Save` via `prepare-commit-msg`; parse committed trailers later. Default session linkage mechanism                                                                                                                                        |
| FEAT-0011  | Refspec configuration    | planned | Generate conservative `.git/config` updates — exclude `opax/v1` from default fetch and store explicit Opax fetch/push refspecs separately                                                                                                                                     |


---

### EPIC-0002: Content-Addressed Storage

**Status:** planned

**Goal:** `internal/cas/` — store/retrieve bulk content by SHA-256 hash at `.git/opax/content/`.


| Feature ID | Feature              | Status  | Description                                                                             |
| ---------- | -------------------- | ------- | --------------------------------------------------------------------------------------- |
| FEAT-0012  | CAS write            | planned | SHA-256 hash, shard to .git/opax/content/{hash[0:2]}/{hash[2:]}, skip if exists (dedup) |
| FEAT-0013  | CAS read             | planned | Retrieve by hash, optional integrity verification (recompute + compare)                 |
| FEAT-0014  | Directory management | planned | Ensure dirs exist, create shards on demand, stats (count, total size)                   |
| FEAT-0015  | Size threshold logic | planned | 4 KB threshold utility: inline on branch (< 4KB) or CAS (>= 4KB)                        |


---

### EPIC-0003: Hygiene Pipeline

**Status:** planned

**Goal:** `internal/hygiene/` — secret detection and scrubbing on all content before storage.


| Feature ID | Feature                | Status  | Description                                                                                                                       |
| ---------- | ---------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0016  | Detector interface     | planned | Detector interface (Detect → []Finding), registry, config-driven loading                                                          |
| FEAT-0017  | Built-in detectors (6) | planned | AWS keys, GitHub tokens, JWTs, PEM private keys, connection strings, generic API keys                                             |
| FEAT-0018  | Entropy detection      | planned | Shannon entropy calculator, configurable threshold (default 4.5), min length 20                                                   |
| FEAT-0019  | Source file scanning   | planned | Read .env/.env.local, extract key-value pairs, flag exact matches in content                                                      |
| FEAT-0020  | Allowlist filtering    | planned | Exact strings + regex patterns, applied after detection before scrubbing                                                          |
| FEAT-0021  | Scrubbing action       | planned | Redact (default: `[REDACTED:{detector}]`), reject (error), warn (pass through + log). Returns scrubbed content + hygiene metadata |
| FEAT-0022  | Pipeline orchestrator  | planned | `Scrub(content, config) → (scrubbed, Hygiene, error)`. Order: source scan → pattern match → entropy → allowlist → scrub           |


---

### EPIC-0004: Integrated Write Path

**Status:** planned

**Goal:** Compose git + CAS + hygiene into a single write operation. The actual Opax storage pipeline.


| Feature ID | Feature               | Status  | Description                                                                                                                                                               |
| ---------- | --------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0023  | Write orchestrator    | planned | Accept record + content → scrub → size threshold → CAS or inline → set content_hash + hygiene metadata → serialize → write to orphan branch. All under .git/opax.lock     |
| FEAT-0024  | Session archive write | planned | sessions/{shard}/{id}/ with metadata.json + summary.md. Transcript → CAS (always large). Generate ses_ ULID                                                               |
| FEAT-0025  | save write            | planned | saves/{shard}/{id}/ with metadata.json. Link to commit hash + session IDs. Finalize preallocated `sav_` ID from trailer                                                    |
| FEAT-0026  | Commit linkage        | planned | Use the preallocated `Opax-Save` trailer as the default immutable linkage, or write a session-link note when `--no-trailers` is enabled. Save fans out to sessions via many-to-many linkage |


---

### EPIC-0005: SQLite Materialization

**Status:** planned

**Goal:** `internal/store/` — SQLite at `.git/opax/opax.db` as materialized view of git data. FTS5 search.


| Feature ID | Feature                  | Status  | Description                                                                                                                                                                              |
| ---------- | ------------------------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0027  | DB initialization        | planned | Open/create at .git/opax/opax.db, WAL mode, foreign keys, page size                                                                                                                      |
| FEAT-0028  | Core schema              | planned | Tables: opax_sessions, opax_session_tags, opax_saves, opax_notes, opax_materializer_state. All indexes. Idempotent (IF NOT EXISTS). Plugins create views over opax_notes, not new tables |
| FEAT-0029  | FTS5 setup               | planned | Virtual table opax_sessions_fts. AFTER INSERT/DELETE triggers. **Verify FTS5 works with modernc.org/sqlite early**                                                                       |
| FEAT-0030  | StorageBackend interface | planned | InitSchema, Query, QueryOne, Execute, Search, Sync, Rebuild, Transaction. SearchOptions + SearchResult types                                                                             |
| FEAT-0031  | SQLite adapter           | planned | Implement StorageBackend against modernc.org/sqlite. FTS5 MATCH, json_extract, transactions                                                                                              |
| FEAT-0032  | Full rebuild             | planned | Walk all commits on opax/v1, parse metadata.json files, insert into tables, walk notes refs, update materializer_state. The "always rebuildable from git" guarantee                      |
| FEAT-0033  | Incremental sync         | planned | Compare current HEAD vs stored git_head, walk only new commits, materialize new records                                                                                                  |
| FEAT-0034  | Dirty flag mechanism     | planned | Write: touch .git/opax/dirty. Read: check flag → incremental sync → remove flag. No daemon needed                                                                                        |


---

### EPIC-0006: Search & Query

**Status:** planned

**Goal:** Make `opax search` and `opax session list/get` work. **First demo milestone.**


| Feature ID | Feature                  | Status  | Description                                                                                                              |
| ---------- | ------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------ |
| FEAT-0035  | SearchStrategy interface | planned | search_mode field (keyword/semantic/hybrid). Phase 0: FTS5Strategy only                                                  |
| FEAT-0036  | FTS5 search              | planned | Query opax_sessions_fts. Ranked results with snippets. Filters: provider, branch, tags, date range, --limit              |
| FEAT-0037  | `opax search` command    | planned | Wire CLI stub → FTS5 search. Text output (default) + JSON (--json). Show id, provider, branch, tags, created_at, snippet |
| FEAT-0038  | `opax session list`      | planned | Query opax_sessions with filters, pagination. Table or JSON output                                                       |
| FEAT-0039  | `opax session get`       | planned | Query by ID, fetch summary + transcript from CAS if content_hash set. Metadata + content output                          |


---

### EPIC-0007: Passive Capture Engine

**Status:** planned

**Goal:** `internal/capture/` — read agent session files after agent writes them, normalize to common format.


| Feature ID | Feature                  | Status  | Description                                                                                                                                             |
| ---------- | ------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0040  | Common transcript format | planned | NormalizedSession struct: agent, model, timestamps, duration, messages, files_changed, commits. NormalizedMessage: role, content, timestamp, tool_calls |
| FEAT-0041  | CaptureSource interface  | planned | Detect() bool, ReadSessions(since time.Time) → []NormalizedSession                                                                                      |
| FEAT-0042  | Claude Code reader       | planned | Read JSONL from ~/.claude/projects/{hash}/. Parse messages, extract model/timestamps/tool usage. Handle partial sessions                                |
| FEAT-0043  | Codex reader             | planned | Read Codex session logs. Lower fidelity initially until format is better understood                                                                     |
| FEAT-0044  | Capture coordinator      | planned | Enumerate sources, call Detect/ReadSessions, track last capture timestamp per source                                                                    |
| FEAT-0045  | Transcript summarization | planned | Simple extraction: first user message as title, key stats, files changed. No LLM in Phase 0                                                             |


---

### EPIC-0008: Memory Plugin

**Status:** planned

**Goal:** `plugins/memory/` — the primary value plugin tying capture, storage, and query together.


| Feature ID | Feature              | Status  | Description                                                                                                                                                                      |
| ---------- | -------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0046  | OpaxPlugin interface | planned | Define in internal/plugin: Name, Namespace, RegisterViews, RegisterCLI, RegisterMCP. Implement in plugins/memory                                                                 |
| FEAT-0047  | Session archive ops  | planned | ArchiveSession (full write path), GetSession, ListSessions, SearchSessions (FTS5)                                                                                                |
| FEAT-0048  | Save creation        | planned | CreateSave — build save with session attribution (file overlap primary, temporal proximity secondary). Finalize the preallocated `sav_` ID from `Opax-Save`, or write a note fallback. Called from post-commit hook |


---

### EPIC-0009: CLI Integration

**Status:** planned

**Goal:** Wire all commands to real implementations. Remove every "not yet implemented" message.


| Feature ID | Feature               | Status  | Description                                                                                                                                                                                                 |
| ---------- | --------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0049  | `opax init`           | planned | Validate git repo, create .git/opax/ + content/, init SQLite, create orphan branch, configure default-fetch exclusion plus explicit Opax refspecs, install hooks (incl. `prepare-commit-msg` for preallocated trailers), generate default config.yaml |
| FEAT-0050  | `opax db rebuild`     | planned | Call StorageBackend.Rebuild(), show progress, output summary                                                                                                                                                |
| FEAT-0051  | `opax storage stats`  | planned | Record counts by type, git tier size, CAS size, DB size. Table + --json                                                                                                                                     |
| FEAT-0052  | `opax doctor`         | planned | Health checks: git repo?, branch exists?, DB accessible?, in sync?, hooks installed?, config.yaml?, CAS writable? Pass/warn/fail indicators                                                                 |
| FEAT-0053  | `opax search` wiring  | planned | Ensure lazy sync fires before search if dirty. Handle empty DB gracefully                                                                                                                                   |
| FEAT-0054  | `opax session` wiring | planned | Ensure JSON output matches MCP tool format for consistency                                                                                                                                                  |


---

### EPIC-0010: MCP Server

**Status:** planned

**Goal:** `internal/mcp/` — stdio MCP server with session query tools for web-only platforms.


| Feature ID | Feature              | Status  | Description                                                                      |
| ---------- | -------------------- | ------- | -------------------------------------------------------------------------------- |
| FEAT-0055  | Server scaffolding   | planned | MCP SDK or thin custom JSON-RPC. stdio transport. Init/handle/shutdown lifecycle |
| FEAT-0056  | search_sessions tool | planned | Search query + filters → SearchSessions → ranked results with snippets           |
| FEAT-0057  | list_sessions tool   | planned | Optional filters → ListSessions → session list                                   |
| FEAT-0058  | get_session tool     | planned | session_id → GetSession → full metadata + summary                                |
| FEAT-0059  | `opax mcp` command   | planned | CLI command that starts MCP server (for MCP settings: `"command": "opax mcp"`)   |


---

### EPIC-0011: Hooks & Init Lifecycle

**Status:** planned

**Goal:** Post-commit capture flow that makes passive capture automatic.


| Feature ID | Feature                      | Status  | Description                                                                                                                                                                   |
| ---------- | ---------------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FEAT-0060  | Hook wrapper scripts         | planned | post-commit wrapper: backup pre-existing hook as .pre-opax, run original first, then `opax capture --post-commit` async (fire-and-forget). Detect husky/lefthook              |
| FEAT-0061  | post-merge dirty flag        | planned | Install post-merge hook that touches .git/opax/dirty                                                                                                                          |
| FEAT-0062  | `opax capture --post-commit` | planned | Hidden command invoked by hook. Detect sources → read sessions → scrub → write → finalize save from the preallocated trailer, or write a note fallback if --no-trailers |
| FEAT-0063  | --no-hooks flag              | planned | Skip hook installation on init. Fallback to explicit MCP calls                                                                                                                |


---

### EPIC-0012: Polish & Validation

**Status:** planned

**Goal:** Meet all Phase 0 exit criteria. Integration tests. Error handling.


| Feature ID | Feature              | Status  | Description                                                                                                              |
| ---------- | -------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------ |
| FEAT-0064  | E2E integration test | planned | Full exit criteria scenario: init → simulate session → commit → verify save → search → verify results → verify scrubbing |
| FEAT-0065  | Basic compaction     | planned | `opax storage compact` — run git gc on opax data, report before/after. No tiered archival yet                            |
| FEAT-0066  | Error handling       | planned | Clear messages for: not a git repo, not initialized, empty DB, no results. Consistent formatting                         |
| FEAT-0067  | Setup guides         | planned | README quickstart, Claude Code integration guide, Codex integration guide                                                |


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


| Epic                    | Status  | Scope                                                                                                                                          |
| ----------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| P1.1 Workflows Plugin   | planned | YAML workflow format in `.opax/workflows/`, DAG-based stage sequencing, git-event triggers (push, PR, merge), stage dispatch via executor drivers, human approval gates, CLI commands. Git primitives as orchestration substrate: branches = work units, commits = stage gates, hooks = transitions |
| P1.2 Evals Plugin       | planned | Eval note format/schema, LLM-as-judge framework (thin), git note attachment, CLI commands                                                      |
| P1.3 Executor Drivers   | planned | Executor driver contract: branch + context bundle (Opax memory) + task spec → session + completion signal. Local process driver, Docker driver. Interface designed for Phase 2 remote drivers (CI, cloud sandboxes, serverless) |
| P1.4 Encryption at Rest | planned | age library, per-tier recipient keys, file-level encryption (content only, metadata plaintext), transparent decryption in read path            |
| P1.5 Basic Compliance   | planned | Article 12 evidence packages, session counts, agent summaries, human oversight records, `opax compliance report`                               |
| P1.6 Additional Capture | planned | Cursor session reader, Gemini CLI session reader                                                                                               |


---

## Phase 2: Remote Execution + Web Control Plane


| Epic                        | Status  | Scope                                                                                 |
| --------------------------- | ------- | ------------------------------------------------------------------------------------- |
| P2.1 Remote Executor Drivers | planned | E2B sandbox driver, GitHub Actions driver, Cloud Run driver, result collection         |
| P2.2 Studio Local           | planned | `opax studio` temp local server, reads SQLite, session timeline + browser + search UI, workflow DAG visualizer |
| P2.3 Postgres Backend       | planned | StorageBackend Postgres adapter, tsvector/tsquery FTS, JSONB + GIN indexes            |
| P2.4 Studio Hosted          | planned | Always-on dashboard, cross-repo views, SSO/RBAC, webhook notifications, managed orchestration dispatch, live workflow monitoring |
| P2.5 First Adapters         | planned | LangGraph adapter, GitHub Actions adapter                                             |


---

## Phase 3: Ecosystem + Compliance + Polish


| Epic                     | Status  | Scope                                                                        |
| ------------------------ | ------- | ---------------------------------------------------------------------------- |
| P3.1 Git Data Spec v2.0  | planned | Extension guidelines, plugin registry/discovery                              |
| P3.2 Full Compliance     | planned | EU AI Act full evidence, NIST AI RMF, ISO 42001, compliance-as-code policies |
| P3.3 Semantic Search     | planned | Local embeddings, SemanticStrategy, hybrid search                            |
| P3.4 Additional Adapters | planned | Temporal, Braintrust, Langfuse adapters                                      |
| P3.5 Team Features       | planned | Shared workflow configs, notification channels, cross-team dashboards        |


---

## Phase 4: Intelligence Layer


| Epic                       | Status  | Scope                                                                                                  |
| -------------------------- | ------- | ------------------------------------------------------------------------------------------------------ |
| P4.1 Cross-Repo Memory     | planned | Agents on Project A learn from patterns in Project B. Hosted Postgres aggregation across repos          |
| P4.2 Quality Scoring       | planned | Automatically assess agent output quality over time. Feed into search ranking                           |
| P4.3 Workflow Insights      | planned | Recommendations based on aggregate usage ("teams like yours add a security review stage here")          |
| P4.4 Cost Analytics         | planned | Token usage, model costs, execution costs per workflow/stage/team. Dashboard views                      |


---

## Phase 5: Ecosystem & Generalization


| Epic                          | Status  | Scope                                                                                                  |
| ----------------------------- | ------- | ------------------------------------------------------------------------------------------------------ |
| P5.1 Workflow Marketplace      | planned | Third-party workflow templates ("code review pipeline for Rails," "security audit for Go")              |
| P5.2 Multi-Language SDKs       | planned | Python and TypeScript SDKs implementing the git data spec. Agents call `import opax` natively           |
| P5.3 Domain Generalization     | planned | If demand emerges: non-git storage drivers, workflow templates for non-dev domains (legal, finance, etc.). Architectural option, not a commitment — the core primitives (work containers, checkpoints, actor sessions, stage transitions) are already domain-agnostic |


---

## Key Files to Modify


| File                                | Epic | Role                                        |
| ----------------------------------- | ---- | ------------------------------------------- |
| `cmd/opax/main.go`                  | E9   | CLI entry point, all command wiring         |
| `internal/git/git.go`               | E1   | Orphan branch + notes + refs (hardest code) |
| `internal/store/store.go`           | E5   | SQLite materialization + StorageBackend     |
| `internal/cas/cas.go`               | E2   | Content-addressed storage                   |
| `internal/capture/capture.go`       | E7   | Capture coordinator                         |
| `internal/capture/claude/claude.go` | E7   | Claude Code JSONL reader                    |
| `internal/capture/codex/codex.go`   | E7   | Codex session reader                        |
| `internal/hygiene/hygiene.go`       | E3   | Secret scrubbing pipeline                   |
| `internal/plugin/plugin.go`         | E8   | Plugin interface + loading                  |
| `internal/mcp/mcp.go`               | E10  | MCP server                                  |
| `plugins/memory/memory.go`          | E8   | Memory plugin (primary value)               |


**Planned files not yet present in repo:**

- None for E0. `internal/lock/lock.go` is implemented.

---

## Verification

Phase 0 is verified by running the E2E integration test (E12.1):

1. `opax init` in a test repo
2. Simulate Claude Code session (write sample JSONL)
3. `git commit` — `prepare-commit-msg` injects `Opax-Save`, then the post-commit hook finalizes the save + session archive
4. `opax search "auth"` — returns matching sessions with snippet
5. Simulate Codex session, same repo — `opax search` returns same results
6. `opax storage stats` — shows record counts and sizes
7. Verify content containing `AKIA...` test key shows `[REDACTED:aws_key]`
