# Opax — Git Data Spec

**Version:** 2.0.0-draft
**Date:** March 17, 2026
**Companion to:** Opax PRD v2.0.0

---

## Overview

This specification defines how Opax stores structured agent activity data as standard git objects. It is the foundation everything else builds on — the SDK implements it, plugins extend it, and third-party tools can read/write it directly without Opax's involvement.

The spec uses five git primitives: orphan branches, commit trailers, git notes, custom refs, and annotated tags. All Opax data lives under the `opax/` namespace to avoid collision with user branches, refs, and tags.

---

## 1. Namespace Convention

```
Orphan branch:      opax/v1

Git notes:          refs/opax/notes/{namespace}

Custom refs:        refs/opax/{purpose}

Annotated tags:     opax/{plugin-defined}

Commit trailers:    Opax-Session: {session-id}
                    Opax-Agent: {agent-identifier}
                    Opax-Duration: {seconds}
```

The `opax/` prefix is configurable but defaults to `opax/`. All data is stored on the single `opax/v1` branch with a sharded directory structure. Third-party plugins register their own shard prefix under the same branch via `opax/v1/ext-{name}/`. Extension plugins choose their own ID prefixes (avoiding first-party prefixes) and follow the same sharding convention.

### ID Format

All record IDs use the pattern `{type_prefix}_{ULID}`. ULIDs are lexicographically sortable and contain an embedded timestamp.


| Type            | Prefix | Example                       |
| --------------- | ------ | ----------------------------- |
| Session archive | `ses_` | `ses_01JQXYZ1234567890ABCDEF` |
| Save            | `sav_` | `sav_01JQXYZ1234567890ABCDEF` |


Plugins define their own ID prefixes (e.g., `wrk_`, `act_`) and register them to avoid collisions.

---

## 2. Single Consolidated Orphan Branch

All Opax data lives on a single orphan branch: `opax/v1`. This branch has no common ancestor with the main codebase. Records are organized in a sharded directory structure using the first two hex characters of the SHA-256 hash of the record ID.

### 2.1 Branch Structure

```
opax/v1/
├── sessions/
│   ├── a3/                          # sha256("ses_01JQXYZ...")[:2]
│   │   └── ses_01JQXYZ.../
│   │       ├── metadata.json
│   │       └── summary.md
│   └── ...
├── saves/
│   ├── 7f/                          # sha256("sav_01JQXYZ...")[:2]
│   │   └── sav_01JQXYZ.../
│   │       └── metadata.json
│   └── ...
└── ext-{plugin}/                    # Plugin-owned directories (e.g., ext-workflows/)
    └── ...
```

**Why single branch:** Per-record orphan branches don't scale (4,500+ branches/month for sessions alone). A single branch lets git share tree objects between commits, enables delta compression across full history, and avoids ref enumeration costs.

**Sharding:** The shard directory is the first two hex characters of `sha256(record_id)`. This gives 256 uniformly distributed buckets regardless of ID prefix or creation time, keeping append cost constant at ~60ms even at 100k+ records. See `docs/misc/sharding-research.md` for benchmarks.

**Write mechanics:** Adding a record uses git plumbing commands (`hash-object`, `mktree`, `commit-tree`, `update-ref`) or a git library. The working tree is never checked out. Writes are serialized via `.git/opax.lock`.

**Read mechanics:** The SQLite index maps record IDs to `(commit, path)` tuples. Reads go through SQLite, not branch enumeration.

### 2.2 Session Archives

**Path:** `opax/v1/sessions/{shard}/{id}/`

Complete records of agent sessions: what was asked, what the agent did, what code changed, how long it took.

```
sessions/a3/ses_01JQXYZ.../
├── metadata.json
└── summary.md
```

**metadata.json:**

```json
{
  "id": "ses_01JQXYZ...",
  "version": 1,
  "agent": "claude-code | aider | codex | goose | unknown",
  "model": "claude-sonnet-4-20250514",
  "branch": "feature/auth-implementation",
  "started_at": "2026-03-13T10:30:00Z",
  "ended_at": "2026-03-13T11:15:00Z",
  "duration_seconds": 2700,
  "exit_code": 0,
  "commits": ["abc1234", "def5678"],
  "files_changed": 12,
  "lines_added": 340,
  "lines_removed": 45,
  "content_hash": "e5f6g7h8...",
  "privacy": {
    "tier": "team",
    "scrubbed": true,
    "scrub_version": "1.0.0",
    "encrypted": false
  },
  "tags": ["auth", "feature"]
}
```

Bulk content (transcript, diff) is stored in content-addressed storage, referenced by `content_hash`. The `summary.md` stays on the branch (small, useful for quick inspection).

### 2.4 Saves (Commit-Anchored)

**Path:** `opax/v1/saves/{shard}/{id}/`

Saves anchor session data to specific commits. The primary question is "what context produced this commit?" — saves are created on commit.

```
saves/7f/sav_01JQXYZ.../
└── metadata.json
```

**metadata.json:**

```json
{
  "id": "sav_01JQXYZ...",
  "version": 1,
  "commit_hash": "abc1234def5678...",
  "session_id": "ses_01JQXYZ...",
  "agent": "claude-code",
  "branch": "feature/auth-implementation",
  "created_at": "2026-03-13T11:15:00Z",
  "files_in_commit": ["src/auth/pkce.ts", "src/auth/oauth.ts"],
  "content_hash": "i9j0k1l2...",
  "privacy": {
    "tier": "team",
    "scrubbed": true,
    "scrub_version": "1.0.0",
    "encrypted": false
  }
}
```

---

## 3. Git Notes

Notes are annotations attached to existing commits without modifying the commit hash. Each namespace stores a different category of annotation. Notes are used for metadata that arrives **after** the commit (test results, review verdicts, eval scores) — data that cannot be embedded as a trailer at commit time.

**Important:** Notes are mutable. Anyone with push access to a notes ref can silently rewrite or delete notes. For commit-time metadata (session linkage, agent identity), use trailers instead — they are cryptographically bound to the commit hash and cannot be altered.

### 3.1 First-Party Namespaces


| Namespace                  | Purpose                           | Typical Writer                     |
| -------------------------- | --------------------------------- | ---------------------------------- |
| `refs/opax/notes/sessions` | Session linkage (fallback when trailers disabled) | post-commit hook |

Plugins register their own note namespaces under `refs/opax/notes/` (e.g., `refs/opax/notes/reviews`, `refs/opax/notes/tests`). Notes are the natural fit for plugin data since it arrives after the commit.

### 3.2 Note Content Format

All notes are JSON objects with a `version` field.

**Session link note (`opax-sessions`) — used when trailers are disabled:**

```json
{
  "version": 1,
  "session_id": "ses_01JQXYZ...",
  "save_id": "sav_01JQXYZ...",
  "agent": "claude-code",
  "duration_seconds": 2700
}
```

### 3.3 Extension Namespaces

First-party namespaces live directly under `refs/opax/notes/`. Community/third-party namespaces use `refs/opax/notes/ext-{name}`. Third-party tools define their own schemas. The SDK provides generic read/write methods for any namespace.

### 3.4 Notes Distribution

`git push` does not push notes by default. The SDK configures a single push refspec for `refs/opax/*` during `opax init`, which covers notes, config, and all plugin refs. Auto-push on commit is configurable but off by default — explicit `opax push` or `git push` with configured refspecs.

---

## 4. Commit Trailers

Trailers are structured key-value pairs appended to commit messages. They are the **default mechanism** for linking commits to session archives. A `prepare-commit-msg` hook appends trailers before the commit is created, so they are part of the commit hash — immutable and tamper-evident.

Trailers are added automatically by the post-install hook. Can be disabled via `opax init --no-trailers` for teams that object to modified commit messages.

```
feat: implement OAuth2 PKCE flow

Implements the authorization code flow with PKCE.

Opax-Session: ses_01JQXYZ...
Opax-Agent: claude-code
Opax-Duration: 2700
```


| Trailer         | Value             | Purpose                                |
| --------------- | ----------------- | -------------------------------------- |
| `Opax-Session`  | Session ID        | Links commit to session archive        |
| `Opax-Agent`    | Agent identifier  | Which agent produced this commit       |
| `Opax-Duration` | Seconds (integer) | Time from session start to this commit |


Plugins may define additional trailers (e.g., `Opax-Stage`, `Opax-Workflow`).

Trailers are queryable via `git log --format="%(trailers)"`. When trailers are disabled (`--no-trailers`), session linkage falls back to git notes via `refs/opax/notes/sessions` — functional but mutable.

---

## 5. Custom Refs

Refs are lightweight pointers to git objects. Opax uses custom refs under `refs/opax/` for application state that needs to survive process restarts.


| Ref                | Points to | Purpose                             |
| ------------------ | --------- | ----------------------------------- |
| `refs/opax/config` | Blob      | Repository-level Opax configuration |


Updated atomically via `git update-ref`. Plugins may register additional custom refs under `refs/opax/`.

---

## 6. Annotated Tags

The `opax/` tag namespace is reserved. Core does not define any tags. Plugins may create annotated tags under `opax/` (e.g., `opax/milestone/{description}`) for audit trails and milestone markers.

---

## 7. SQLite Materialization

The SQLite database at `.git/opax/opax.db` is a materialized view of all Opax git data, optimized for queries. It is always rebuildable from git via `opax db rebuild`.

### 7.1 Core Schema

```sql
-- Session archives
CREATE TABLE opax_sessions (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  agent TEXT NOT NULL,
  model TEXT,
  branch TEXT,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  duration_seconds INTEGER,
  exit_code INTEGER,
  commits TEXT,  -- JSON array
  files_changed INTEGER,
  lines_added INTEGER,
  lines_removed INTEGER,
  tags TEXT,  -- JSON array
  summary TEXT,  -- session summary, materialized from summary.md
  content_hash TEXT,  -- SHA-256 hash referencing CAS
  privacy_tier TEXT DEFAULT 'team',
  privacy_scrubbed BOOLEAN DEFAULT FALSE,
  privacy_scrub_version TEXT,
  privacy_encrypted BOOLEAN DEFAULT FALSE,
  git_branch TEXT NOT NULL DEFAULT 'opax/v1',
  git_commit TEXT NOT NULL,
  archive_location TEXT  -- NULL = hot, remote URL = warm/cold
);

CREATE TABLE opax_session_tags (
  session_id TEXT NOT NULL REFERENCES opax_sessions(id),
  tag TEXT NOT NULL,
  PRIMARY KEY (session_id, tag)
);
CREATE INDEX idx_session_tags_tag ON opax_session_tags(tag);

-- Saves (commit-anchored)
CREATE TABLE opax_saves (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  commit_hash TEXT NOT NULL,
  session_id TEXT REFERENCES opax_sessions(id),
  agent TEXT,
  branch TEXT,
  content_hash TEXT,
  privacy_tier TEXT DEFAULT 'team',
  privacy_scrubbed BOOLEAN DEFAULT FALSE,
  privacy_scrub_version TEXT,
  privacy_encrypted BOOLEAN DEFAULT FALSE,
  git_commit TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_saves_commit ON opax_saves(commit_hash);
CREATE INDEX idx_saves_session ON opax_saves(session_id);

-- Git notes (all namespaces)
CREATE TABLE opax_notes (
  commit_hash TEXT NOT NULL,
  namespace TEXT NOT NULL,
  content TEXT NOT NULL,  -- JSON
  created_at TEXT,  -- derived from git commit timestamp
  PRIMARY KEY (commit_hash, namespace)
);
CREATE INDEX idx_notes_namespace ON opax_notes(namespace);

-- FTS5 full-text search
CREATE VIRTUAL TABLE opax_sessions_fts USING fts5(
  id, agent, branch, tags, summary,
  content=opax_sessions,
  content_rowid=rowid
);

-- Materializer state tracking
CREATE TABLE opax_materializer_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- Tracks git_head so SDK detects when external git changes need materializing
```

### 7.2 Plugin Data

Plugins store data using two mechanisms the core already materializes:

1. **Branch directories** — plugins write records under `opax/v1/ext-{name}/` using the same sharding convention. The core materializer walks these generically during rebuild.
2. **Git notes** — plugins register their own note namespaces under `refs/opax/notes/` (e.g., `refs/opax/notes/reviews`, `refs/opax/notes/tests`). Notes are materialized into the generic `opax_notes` table.

Plugins that need richer queries create **views** over `opax_notes` using `json_extract`, not new tables. This keeps the materializer simple — it doesn't need to understand plugin schemas — and preserves the "SQLite is a cache" invariant since `opax db rebuild` only needs to know about core tables.

```sql
-- Example: a workflows plugin creates a view, not a table
CREATE VIEW opax_reviews AS
  SELECT commit_hash,
    json_extract(content, '$.reviewer') AS reviewer,
    json_extract(content, '$.verdict') AS verdict,
    json_extract(content, '$.summary') AS summary
  FROM opax_notes WHERE namespace = 'reviews';
```

### 7.3 StorageAdapter Interface

The SDK abstracts database dialect differences behind a `StorageAdapter` interface:

- `initSchema()` — generates DDL for the configured backend (SQLite or Postgres)
- `query()` — handles dialect differences (`json_extract` vs `>>` operators, FTS5 vs `tsvector`)
- `sync()` — incremental materialization from git to database
- `rebuild()` — full rebuild from git

The local SDK always uses the SQLite adapter. The hosted control plane uses the Postgres adapter. The SDK's public API is unchanged regardless of backend.

### 7.4 Sync Behavior

The `opax_materializer_state` table tracks a `git_head` value. When the SDK detects that HEAD has changed (e.g., after `git pull`), it runs an incremental sync to materialize new records. Current leaning: lazy sync (on first read after HEAD changes) with a stale-data indicator, not eager background sync.

---

## 8. Content-Addressed Storage (CAS)

### Purpose

Bulk content (transcripts, diffs) is stored outside of git in a content-addressed file store at `.git/opax/content/`. This dramatically reduces git repository size while preserving tamper-verification via hash comparison.

### Layout

```
.git/opax/content/
├── a1/
│   ├── b2c3d4e5f6...   # SHA-256 hash (remaining chars)
│   └── ...
├── e5/
│   └── f6g7h8i9j0...
└── ...
```

Files are sharded by the first two characters of the SHA-256 hash, mirroring git's object storage layout.

### Write Path

1. Content is scrubbed by the privacy pipeline
2. SHA-256 hash is computed over the scrubbed content
3. Content is written to `.git/opax/content/{hash[0:2]}/{hash[2:]}`
4. The raw hex hash is recorded in the metadata file on the `opax/v1` branch as `content_hash` (no algorithm prefix — always SHA-256)

### Read Path

1. Query SQLite for the record → get `content_hash`
2. Read `.git/opax/content/{hash[0:2]}/{hash[2:]}`
3. Optionally verify integrity: `sha256sum` of file matches `content_hash`

### Deduplication

Content-addressing provides natural deduplication. If two sessions produce identical transcripts (unlikely but possible), only one copy is stored.

### What Goes Where


| Content Type    | Storage         | Rationale                                              |
| --------------- | --------------- | ------------------------------------------------------ |
| `metadata.json` | Git (on branch) | Small, structured, benefits from git delta compression |
| `summary.md`    | Git (on branch) | Small, useful for quick inspection via git tools       |
| `transcript.md` | CAS             | Large, high-volume                                     |
| `diff.patch`    | CAS             | Large, high-volume                                     |


### Distribution

CAS files are local-only by default. For team sharing:

- `opax push` can optionally bundle CAS files and push to a configured remote
- The `content_hash` in git metadata enables verification after transfer
- Hosted tier manages CAS distribution automatically

---

## 9. Storage Constraints

Git was designed for source code, not a general-purpose data platform. The spec defines constraints to keep repositories healthy.

**Data types:** Text and JSON only on the branch. No binaries. Bulk content goes to CAS. Reference binary artifacts by URL or path.

**Size budget:** With the two-tier model, git footprint is ~2-5 MB/day for a 5-developer team (metadata only). CAS adds ~100 MB/day for bulk content but doesn't affect git operations. See *Storage & Scaling Spec* for detailed capacity math.

**Network transfer:** Configure refspecs so `git fetch` only pulls code branches by default. Opax data fetched on demand. The SDK auto-configures this during `opax init`.

---

## 10. Schema Evolution

All record metadata includes a `version` field (integer, starting at 1). When the schema for a record type changes:

1. **New fields are always optional.** Existing records without the field remain valid. The materializer uses sensible defaults for missing fields.
2. **The `version` field increments** when a record uses the new schema. Old and new versions coexist on the same branch.
3. `**opax db rebuild` handles all known versions.** The materializer maps each version to the current SQLite schema during rebuild. This is the migration path — there are no separate migration scripts.
4. **Breaking changes require a new branch.** If a change cannot be handled by optional fields (e.g., fundamentally restructuring a record type), the branch namespace increments: `opax/data/v2`. The old branch is preserved for read-only access. This should be exceedingly rare.

---

## 11. Git Platform Compatibility


| Platform  | Orphan branches     | Notes         | Custom refs                    | Tags |
| --------- | ------------------- | ------------- | ------------------------------ | ---- |
| GitHub    | ✅ (soft limit ~10k) | ✅ (no web UI) | ✅ (some push rules may reject) | ✅    |
| GitLab    | ✅                   | ✅             | ✅                              | ✅    |
| Bitbucket | ✅                   | ✅ (limited)   | ⚠️ (test required)             | ✅    |


The SDK includes `opax init --test-platform` which verifies remote platform compatibility and configures push/fetch refspecs accordingly.

---

## 11. MCP Tool Schemas

The memory plugin exposes MCP tools for querying session data. Schemas below are the MCP `inputSchema` format.

### search (search_sessions)

```json
{
  "name": "search_sessions",
  "description": "Search agent session history for relevant context.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query": { "type": "string" },
      "agent": { "type": "string" },
      "branch": { "type": "string" },
      "tags": { "type": "array", "items": { "type": "string" } },
      "after": { "type": "string", "format": "date-time" },
      "before": { "type": "string", "format": "date-time" },
      "limit": { "type": "number", "default": 5 }
    }
  }
}
```

### list (list_sessions)

```json
{
  "name": "list_sessions",
  "description": "List agent sessions, optionally filtered.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "agent": { "type": "string" },
      "branch": { "type": "string" },
      "tags": { "type": "array", "items": { "type": "string" } },
      "limit": { "type": "number", "default": 20 }
    }
  }
}
```

### get (get_session)

```json
{
  "name": "get_session",
  "description": "Retrieve a specific session archive by ID.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "session_id": { "type": "string" }
    },
    "required": ["session_id"]
  }
}
```

### handover (create_handover)

```json
{
  "name": "create_handover",
  "description": "Generate a structured handover document for transitioning work to another agent or platform.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "next_task": { "type": "string" },
      "include_sessions": {
        "type": "array",
        "items": { "type": "string" },
        "description": "Session IDs to include. Default: auto-select recent sessions on current branch.",
        "default": ["auto"]
      },
      "detail_level": {
        "type": "string",
        "enum": ["brief", "standard", "comprehensive"],
        "default": "standard"
      }
    },
    "required": ["next_task"]
  }
}
```

