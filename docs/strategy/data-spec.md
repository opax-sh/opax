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
Orphan branch:      opax/data/v1

Git notes:          refs/notes/opax-{namespace}

Custom refs:        refs/opax/{purpose}

Annotated tags:     opax/milestone/{description}

Commit trailers:    OA-Session: {session-id}
                    OA-Agent: {agent-identifier}
                    OA-Stage: {workflow-name}/{stage-name}
                    OA-Workflow: {workflow-instance-id}
                    OA-Duration: {seconds}
```

The `opax/` prefix is configurable but defaults to `opax/`. All data is stored on the single `opax/data/v1` branch with a sharded directory structure. Third-party plugins register their own shard prefix under the same branch via `opax/data/v1/ext-{name}/`.

### ID Format

All record IDs use the pattern `{type_prefix}_{ULID}`. ULIDs are lexicographically sortable and contain an embedded timestamp.

| Type | Prefix | Example |
|---|---|---|
| Context artifact | `ctx_` | `ctx_01JQXYZ1234567890ABCDEF` |
| Session archive | `ses_` | `ses_01JQXYZ1234567890ABCDEF` |
| Checkpoint | `chk_` | `chk_01JQXYZ1234567890ABCDEF` |
| Workflow instance | `wf_` | `wf_01JQXYZ1234567890ABCDEF` |
| Action execution | `act_` | `act_01JQXYZ1234567890ABCDEF` |

---

## 2. Single Consolidated Orphan Branch

All Opax data lives on a single orphan branch: `opax/data/v1`. This branch has no common ancestor with the main codebase. Records are organized in a sharded directory structure using the first two characters of the record ID.

### 2.1 Branch Structure

```
opax/data/v1/
├── contexts/
│   ├── ct/
│   │   └── ctx_01JQXYZ.../
│   │       ├── metadata.json
│   │       └── content.md
│   └── ...
├── sessions/
│   ├── se/
│   │   └── ses_01JQXYZ.../
│   │       ├── metadata.json
│   │       └── summary.md
│   └── ...
├── checkpoints/
│   ├── ch/
│   │   └── chk_01JQXYZ.../
│   │       └── metadata.json
│   └── ...
├── workflows/
│   ├── wf/
│   │   └── wf_01JQXYZ.../
│   │       ├── manifest.json
│   │       ├── stages/
│   │       └── events.jsonl
│   └── ...
└── actions/
    ├── ac/
    │   └── act_01JQXYZ.../
    │       ├── metadata.json
    │       └── artifacts/
    └── ...
```

**Why single branch:** Per-record orphan branches don't scale (4,500+ branches/month for sessions alone). A single branch lets git share tree objects between commits, enables delta compression across full history, and avoids ref enumeration costs. Adopted from Entire.io's `entire/checkpoints/v1` pattern.

**Sharding:** The first two characters of the record ID determine the shard directory. This prevents any single directory from accumulating too many entries, which would degrade git tree performance.

**Write mechanics:** Adding a record uses git plumbing commands (`hash-object`, `mktree`, `commit-tree`, `update-ref`) or a git library. The working tree is never checked out. Writes are serialized via `.git/opax.lock`.

**Read mechanics:** The SQLite index maps record IDs to `(commit, path)` tuples. Reads go through SQLite, not branch enumeration.

### 2.2 Memory Plugin: Context Artifacts

**Path:** `opax/data/v1/contexts/{shard}/{id}/`

Cross-platform agent context: conversations, decisions, architecture notes, implementation plans, research, handover documents.

```
contexts/ct/ctx_01JQXYZ.../
├── metadata.json
└── content.md
```

**metadata.json:**

```json
{
  "id": "ctx_01JQXYZ...",
  "version": 1,
  "type": "conversation | decision | architecture | implementation_plan | review | bug_report | research | handover | note",
  "title": "Auth architecture discussion",
  "source": {
    "platform": "claude-web | claude-code | codex | aider | goose | chatgpt | manual",
    "session_id": "optional-platform-session-id",
    "model": "claude-sonnet-4-20250514"
  },
  "tags": ["auth", "oauth", "architecture"],
  "related_paths": ["src/auth/**", "src/middleware/auth.ts"],
  "content_hash": "sha256:a1b2c3d4...",
  "privacy": {
    "tier": "public | team | private",
    "scrubbed": true,
    "scrub_version": "1.0.0",
    "encrypted": false
  },
  "created_at": "2026-03-13T10:30:00Z",
  "updated_at": "2026-03-13T10:30:00Z"
}
```

The `content_hash` field references bulk content in content-addressed storage (see Section 8). When content is small enough to inline (< 4 KB), it may be stored directly as `content.md` on the branch instead.

Context artifacts are append-only. Updates create new commits on the branch, preserving full history.

### 2.3 Memory Plugin: Session Archives

**Path:** `opax/data/v1/sessions/{shard}/{id}/`

Complete records of agent sessions: what was asked, what the agent did, what code changed, how long it took.

```
sessions/se/ses_01JQXYZ.../
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
  "content_hash": "sha256:e5f6g7h8...",
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

### 2.4 Checkpoints (Commit-Anchored)

**Path:** `opax/data/v1/checkpoints/{shard}/{id}/`

Checkpoints anchor session data to specific commits. The primary question is "what context produced this commit?" — checkpoints are created on commit.

```
checkpoints/ch/chk_01JQXYZ.../
└── metadata.json
```

**metadata.json:**

```json
{
  "id": "chk_01JQXYZ...",
  "version": 1,
  "commit": "abc1234def5678...",
  "session_id": "ses_01JQXYZ...",
  "agent": "claude-code",
  "branch": "feature/auth-implementation",
  "created_at": "2026-03-13T11:15:00Z",
  "files_in_commit": ["src/auth/pkce.ts", "src/auth/oauth.ts"],
  "content_hash": "sha256:i9j0k1l2..."
}
```

### 2.5 Workflows Plugin: Execution Logs

**Path:** `opax/data/v1/workflows/{shard}/{id}/`

Execution history of a workflow instance: which stages ran, in what order, what triggered them, outcomes.

```
workflows/wf/wf_01JQXYZ.../
├── manifest.json
├── stages/
│   ├── implement.json
│   ├── review.json
│   ├── test.json
│   └── merge.json
└── events.jsonl
```

**Stage record:**

```json
{
  "name": "implement",
  "status": "pending | running | completed | failed | skipped | waiting_gate",
  "trigger": {
    "event": "manual | commit | merge | ref-update | gate-passed",
    "received_at": "2026-03-13T10:30:00Z"
  },
  "executor": {
    "type": "local-process | docker | e2b | github-actions"
  },
  "started_at": "2026-03-13T10:30:00Z",
  "ended_at": "2026-03-13T11:15:00Z",
  "outcome": {
    "exit_code": 0,
    "session_id": "ses_01JQXYZ...",
    "commits": ["abc1234"],
    "branch": "agent/implement/auth-123"
  },
  "gate": {
    "type": "human-approval",
    "status": "approved",
    "approved_by": "developer",
    "approved_at": "2026-03-13T11:20:00Z"
  }
}
```

### 2.6 Action Execution Logs

**Path:** `opax/data/v1/actions/{shard}/{id}/`

Logs from sandboxed action executions (tests, lints, evals). A single workflow stage may trigger multiple action executions.

```
actions/ac/act_01JQXYZ.../
├── metadata.json
└── artifacts/
    └── results.json
```

Bulk output (stdout, stderr) stored in content-addressed storage, referenced by `content_hash` in metadata.

---

## 3. Git Notes

Notes are annotations attached to existing commits without modifying the commit hash. Each namespace stores a different category of annotation. Notes are the **default mechanism** for attaching Opax metadata to commits (preferred over trailers).

### 3.1 First-Party Namespaces

| Namespace | Purpose | Typical Writer |
|---|---|---|
| `refs/notes/opax-sessions` | Links commits to session archives | post-commit hook / passive capture |
| `refs/notes/opax-reviews` | Code review assessments | Review workflow stage |
| `refs/notes/opax-tests` | Test results | Test action / CI |
| `refs/notes/opax-evals` | Eval scores | Eval action |
| `refs/notes/opax-gates` | Approval records | Workflow gate resolution |
| `refs/notes/opax-ci` | General CI results | Lint/build actions |

### 3.2 Note Content Format

All notes are JSON objects with a `version` field.

**Session link note (`opax-sessions`):**

```json
{
  "version": 1,
  "session_id": "ses_01JQXYZ...",
  "checkpoint_id": "chk_01JQXYZ...",
  "agent": "claude-code",
  "stage": "feature-development/implement",
  "workflow_id": "wf_01JQXYZ...",
  "duration_seconds": 2700
}
```

**Review note (`opax-reviews`):**

```json
{
  "version": 1,
  "reviewer": "claude-code",
  "session_id": "ses_01JQXYZ...",
  "verdict": "approve | request_changes | comment",
  "summary": "Implementation looks solid.",
  "issues": [
    {
      "severity": "suggestion | warning | error",
      "file": "src/auth/pkce.ts",
      "line": 42,
      "message": "Consider renaming for clarity"
    }
  ],
  "reviewed_at": "2026-03-13T11:20:00Z"
}
```

**Test result note (`opax-tests`):**

```json
{
  "version": 1,
  "runner": "opax-action",
  "execution_id": "act_01JQXYZ...",
  "passed": 42,
  "failed": 0,
  "skipped": 3,
  "duration_ms": 12450,
  "framework": "vitest",
  "ran_at": "2026-03-13T11:25:00Z"
}
```

**Eval note (`opax-evals`):**

```json
{
  "version": 1,
  "evaluator": "opax-eval",
  "execution_id": "act_01JQXYZ...",
  "scores": {
    "correctness": 0.92,
    "code_quality": 0.85,
    "test_coverage": 0.78
  },
  "model": "claude-sonnet-4-20250514",
  "criteria_version": "1.0",
  "evaluated_at": "2026-03-13T11:30:00Z"
}
```

### 3.3 Extension Namespaces

The `opax-` prefix is reserved for first-party namespaces. Community/third-party namespaces use `opax-ext-{name}`. Third-party tools define their own schemas. The SDK provides generic read/write methods for any namespace.

### 3.4 Notes Distribution

`git push` does not push notes by default. The SDK configures push refspecs for `refs/notes/opax-*` during `opax init`. Auto-push notes to remote on commit is configurable but off by default — explicit `opax push` or `git push` with configured refspecs.

---

## 4. Commit Trailers

Trailers are structured key-value pairs appended to commit messages. They are **opt-in** (not default) because they modify the commit hash, which risks developer backlash.

When enabled, a `prepare-commit-msg` hook appends trailers to commits made in Opax-managed worktrees.

```
feat: implement OAuth2 PKCE flow

Implements the authorization code flow with PKCE.

OA-Session: ses_01JQXYZ...
OA-Agent: claude-code
OA-Stage: feature-development/implement
OA-Workflow: wf_01JQXYZ...
OA-Duration: 2700
```

| Trailer | Value | Purpose |
|---|---|---|
| `OA-Session` | Session ID | Links commit to session archive |
| `OA-Agent` | Agent identifier | Which agent produced this commit |
| `OA-Stage` | `{workflow}/{stage}` | Workflow stage this commit belongs to |
| `OA-Workflow` | Workflow instance ID | Groups commits across stages |
| `OA-Duration` | Seconds (integer) | Time from session start to this commit |

Trailers are queryable via `git log --format="%(trailers)"`. When trailers are disabled (default), the same metadata is stored as git notes via `refs/notes/opax-sessions`.

---

## 5. Custom Refs

Refs are lightweight pointers to git objects. Opax uses custom refs under `refs/opax/` for application state that needs to survive process restarts.

| Ref | Points to | Purpose |
|---|---|---|
| `refs/opax/workflows/active` | Blob | JSON listing active workflow instances |
| `refs/opax/config` | Blob | Repository-level Opax configuration |

Updated atomically via `git update-ref`. Inspectable: `git show refs/opax/workflows/active`.

---

## 6. Annotated Tags

Tags mark significant workflow milestones. Annotated tags store tagger, timestamp, and message — suitable for audit trails.

```
opax/milestone/{workflow-name}/{description}
```

```bash
git tag -a "opax/milestone/feature-development/auth-complete" \
  -m '{"workflow": "wf_01JQXYZ...", "stage": "merge", "result": "success"}' \
  abc1234
```

Visible in GitHub/GitLab UI. Provide human-readable markers in the project timeline.

---

## 7. SQLite Materialization

The SQLite database at `.git/opax/opax.db` is a materialized view of all Opax git data, optimized for queries. It is always rebuildable from git via `opax db rebuild`.

### 7.1 Core Schema

```sql
-- Context artifacts
CREATE TABLE opax_contexts (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  content TEXT,
  source_platform TEXT,
  source_session_id TEXT,
  source_model TEXT,
  tags TEXT,  -- JSON array, denormalized below
  related_paths TEXT,  -- JSON array
  content_hash TEXT,  -- SHA-256 hash referencing CAS
  privacy_tier TEXT DEFAULT 'public',
  privacy_scrubbed BOOLEAN DEFAULT FALSE,
  privacy_encrypted BOOLEAN DEFAULT FALSE,
  git_branch TEXT NOT NULL DEFAULT 'opax/data/v1',
  git_commit TEXT NOT NULL,
  archive_location TEXT,  -- NULL = hot, remote URL = warm/cold
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Tag junction table for indexed lookups
CREATE TABLE opax_context_tags (
  context_id TEXT NOT NULL REFERENCES opax_contexts(id),
  tag TEXT NOT NULL,
  PRIMARY KEY (context_id, tag)
);
CREATE INDEX idx_context_tags_tag ON opax_context_tags(tag);

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
  content_hash TEXT,  -- SHA-256 hash referencing CAS
  privacy_tier TEXT DEFAULT 'team',
  git_branch TEXT NOT NULL DEFAULT 'opax/data/v1',
  git_commit TEXT NOT NULL,
  archive_location TEXT  -- NULL = hot, remote URL = warm/cold
);

CREATE TABLE opax_session_tags (
  session_id TEXT NOT NULL REFERENCES opax_sessions(id),
  tag TEXT NOT NULL,
  PRIMARY KEY (session_id, tag)
);
CREATE INDEX idx_session_tags_tag ON opax_session_tags(tag);

-- Checkpoints (commit-anchored)
CREATE TABLE opax_checkpoints (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  commit_hash TEXT NOT NULL,
  session_id TEXT REFERENCES opax_sessions(id),
  agent TEXT,
  branch TEXT,
  content_hash TEXT,
  git_commit TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_checkpoints_commit ON opax_checkpoints(commit_hash);
CREATE INDEX idx_checkpoints_session ON opax_checkpoints(session_id);

-- Git notes (all namespaces)
CREATE TABLE opax_notes (
  commit_hash TEXT NOT NULL,
  namespace TEXT NOT NULL,
  content TEXT NOT NULL,  -- JSON
  created_at TEXT,
  PRIMARY KEY (commit_hash, namespace)
);
CREATE INDEX idx_notes_namespace ON opax_notes(namespace);

-- Typed views over notes
CREATE VIEW opax_reviews AS
  SELECT commit_hash,
    json_extract(content, '$.reviewer') AS reviewer,
    json_extract(content, '$.verdict') AS verdict,
    json_extract(content, '$.summary') AS summary,
    json_extract(content, '$.reviewed_at') AS reviewed_at,
    content AS raw
  FROM opax_notes WHERE namespace = 'opax-reviews';

CREATE VIEW opax_test_results AS
  SELECT commit_hash,
    json_extract(content, '$.passed') AS passed,
    json_extract(content, '$.failed') AS failed,
    json_extract(content, '$.skipped') AS skipped,
    json_extract(content, '$.duration_ms') AS duration_ms,
    json_extract(content, '$.framework') AS framework,
    json_extract(content, '$.ran_at') AS ran_at,
    content AS raw
  FROM opax_notes WHERE namespace = 'opax-tests';

CREATE VIEW opax_eval_scores AS
  SELECT commit_hash,
    json_extract(content, '$.scores') AS scores,
    json_extract(content, '$.model') AS model,
    json_extract(content, '$.evaluated_at') AS evaluated_at,
    content AS raw
  FROM opax_notes WHERE namespace = 'opax-evals';

-- Workflow instances
CREATE TABLE opax_workflows (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,  -- active | completed | failed | cancelled
  manifest TEXT,  -- JSON
  git_branch TEXT NOT NULL DEFAULT 'opax/data/v1',
  git_commit TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE opax_workflow_stages (
  workflow_id TEXT NOT NULL REFERENCES opax_workflows(id),
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  trigger_event TEXT,
  executor_type TEXT,
  started_at TEXT,
  ended_at TEXT,
  outcome TEXT,  -- JSON
  gate_type TEXT,
  gate_status TEXT,
  PRIMARY KEY (workflow_id, name)
);

-- FTS5 full-text search
CREATE VIRTUAL TABLE opax_contexts_fts USING fts5(
  id, title, content, tags,
  content=opax_contexts,
  content_rowid=rowid
);

CREATE VIRTUAL TABLE opax_sessions_fts USING fts5(
  id, agent, branch, tags,
  content=opax_sessions,
  content_rowid=rowid
);

-- Triggers to keep FTS in sync
CREATE TRIGGER opax_contexts_ai AFTER INSERT ON opax_contexts BEGIN
  INSERT INTO opax_contexts_fts(rowid, id, title, content, tags)
  VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;

CREATE TRIGGER opax_contexts_ad AFTER DELETE ON opax_contexts BEGIN
  INSERT INTO opax_contexts_fts(opax_contexts_fts, rowid, id, title, content, tags)
  VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
END;

-- Materializer state tracking
CREATE TABLE opax_materializer_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- Tracks git_head so SDK detects when external git changes need materializing
```

### 7.2 Plugin Schema Extensions

Plugins extend the schema by implementing `initSchema()` in the `OpaxPlugin` interface. The plugin declares its tables, views, indexes, and FTS entries. The SDK calls `initSchema()` during `opax init` and on plugin load.

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

Bulk content (transcripts, diffs, action logs) is stored outside of git in a content-addressed file store at `.git/opax/content/`. This dramatically reduces git repository size while preserving tamper-verification via hash comparison.

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
4. The hash is recorded in the metadata file on the `opax/data/v1` branch as `content_hash`

### Read Path

1. Query SQLite for the record → get `content_hash`
2. Read `.git/opax/content/{hash[0:2]}/{hash[2:]}`
3. Optionally verify integrity: `sha256sum` of file matches `content_hash`

### Deduplication

Content-addressing provides natural deduplication. If two sessions produce identical transcripts (unlikely but possible), only one copy is stored. More practically, this benefits context artifacts that may be saved multiple times.

### What Goes Where

| Content Type | Storage | Rationale |
|---|---|---|
| `metadata.json` | Git (on branch) | Small, structured, benefits from git delta compression |
| `summary.md` | Git (on branch) | Small, useful for quick inspection via git tools |
| `content.md` (< 4 KB) | Git (on branch) | Small enough to inline |
| `content.md` (>= 4 KB) | CAS | Too large for efficient git storage |
| `transcript.md` | CAS | Large, high-volume |
| `diff.patch` | CAS | Large, high-volume |
| `stdout.log` / `stderr.log` | CAS | Large, high-volume |

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

## 10. Git Platform Compatibility

| Platform | Orphan branches | Notes | Custom refs | Tags |
|---|---|---|---|---|
| GitHub | ✅ (soft limit ~10k) | ✅ (no web UI) | ✅ (some push rules may reject) | ✅ |
| GitLab | ✅ | ✅ | ✅ | ✅ |
| Bitbucket | ✅ | ✅ (limited) | ⚠️ (test required) | ✅ |

The SDK includes `opax init --test-platform` which verifies remote platform compatibility and configures push/fetch refspecs accordingly.

---

## 11. MCP Tool Schemas

The memory plugin exposes five MCP tools. Schemas below are the MCP `inputSchema` format.

### save (persist_context)

```json
{
  "name": "persist_context",
  "description": "Save a conversation, document, or context artifact to the project's shared agent memory.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "context_type": {
        "type": "string",
        "enum": ["conversation", "decision", "architecture", "implementation_plan", "review", "bug_report", "research", "handover", "note"]
      },
      "title": { "type": "string" },
      "content": { "type": "string", "description": "Full content in markdown format" },
      "source": {
        "type": "object",
        "properties": {
          "platform": { "type": "string" },
          "session_id": { "type": "string" },
          "model": { "type": "string" }
        }
      },
      "tags": { "type": "array", "items": { "type": "string" } },
      "related_paths": { "type": "array", "items": { "type": "string" } }
    },
    "required": ["context_type", "title", "content"]
  }
}
```

### search (query_context)

```json
{
  "name": "query_context",
  "description": "Search the project's shared agent memory for relevant context.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query": { "type": "string" },
      "context_type": { "type": "string" },
      "source_platform": { "type": "string" },
      "related_paths": { "type": "array", "items": { "type": "string" } },
      "tags": { "type": "array", "items": { "type": "string" } },
      "after": { "type": "string", "format": "date-time" },
      "before": { "type": "string", "format": "date-time" },
      "limit": { "type": "number", "default": 5 }
    }
  }
}
```

### list (list_context)

```json
{
  "name": "list_context",
  "description": "List all context artifacts, optionally filtered.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "context_type": { "type": "string" },
      "source_platform": { "type": "string" },
      "tags": { "type": "array", "items": { "type": "string" } },
      "limit": { "type": "number", "default": 20 }
    }
  }
}
```

### get (get_context)

```json
{
  "name": "get_context",
  "description": "Retrieve a specific context artifact by ID.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "context_id": { "type": "string" }
    },
    "required": ["context_id"]
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
      "include_contexts": {
        "type": "array",
        "items": { "type": "string" },
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
