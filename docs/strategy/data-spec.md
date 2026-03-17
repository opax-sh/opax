# Opax — Git Data Spec

**Version:** 1.0.0-draft
**Date:** March 16, 2026
**Companion to:** Opax PRD v4

---

## Overview

This specification defines how Opax stores structured agent activity data as standard git objects. It is the foundation everything else builds on — the SDK implements it, plugins extend it, and third-party tools can read/write it directly without Opax's involvement.

The spec uses five git primitives: orphan branches, commit trailers, git notes, custom refs, and annotated tags. All Opax data lives under the `oa/` namespace to avoid collision with user branches, refs, and tags.

---

## 1. Namespace Convention

```
Orphan branches:    oa/{plugin}/{type}/{id}
                    oa/core/index

Git notes:          refs/notes/oa-{namespace}

Custom refs:        refs/oa/{purpose}

Annotated tags:     oa/milestone/{description}

Commit trailers:    OA-Session: {session-id}
                    OA-Agent: {agent-identifier}
                    OA-Stage: {workflow-name}/{stage-name}
                    OA-Workflow: {workflow-instance-id}
                    OA-Duration: {seconds}
```

The `oa/` prefix is configurable but defaults to `oa/`. All first-party plugins use `oa/{plugin-name}/` as their branch prefix. Third-party plugins register their own namespace under `oa/ext-{name}/`.

### ID Format

All record IDs use the pattern `{type_prefix}_{ULID}`. ULIDs are lexicographically sortable and contain an embedded timestamp.

| Type | Prefix | Example |
|---|---|---|
| Context artifact | `ctx_` | `ctx_01JQXYZ1234567890ABCDEF` |
| Session archive | `ses_` | `ses_01JQXYZ1234567890ABCDEF` |
| Workflow instance | `wf_` | `wf_01JQXYZ1234567890ABCDEF` |
| Action execution | `act_` | `act_01JQXYZ1234567890ABCDEF` |

---

## 2. Orphan Branches

Orphan branches are git branches with no common ancestor to the main codebase. They store structured data as files in commit trees without polluting the project's history. Each orphan branch is an independent data stream.

### 2.1 Memory Plugin: Context Artifacts

**Branch:** `oa/memory/context/{id}`

Cross-platform agent context: conversations, decisions, architecture notes, implementation plans, research, handover documents.

```
oa/memory/context/ctx_01JQXYZ.../
├── metadata.json
├── content.md
└── related/
    └── refs.json
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

Context artifacts are append-only. Updates create new commits on the same orphan branch, preserving full history. The latest commit is the current version.

### 2.2 Memory Plugin: Session Archives

**Branch:** `oa/memory/sessions/{id}`

Complete records of agent sessions: what was asked, what the agent did, what code changed, how long it took.

```
oa/memory/sessions/ses_01JQXYZ.../
├── metadata.json
├── transcript.md
├── diff.patch
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
  "privacy": {
    "tier": "team",
    "scrubbed": true,
    "scrub_version": "1.0.0",
    "encrypted": false
  },
  "tags": ["auth", "feature"]
}
```

### 2.3 Workflows Plugin: Execution Logs

**Branch:** `oa/workflows/{workflow-name}/{instance-id}`

Execution history of a workflow instance: which stages ran, in what order, what triggered them, outcomes.

```
oa/workflows/feature-development/wf_01JQXYZ.../
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

### 2.4 Action Execution Logs

**Branch:** `oa/actions/{execution-id}`

Logs from sandboxed action executions (tests, lints, evals). A single workflow stage may trigger multiple action executions.

```
oa/actions/act_01JQXYZ.../
├── metadata.json
├── stdout.log
├── stderr.log
└── artifacts/
    └── results.json
```

### 2.5 Core Index

**Branch:** `oa/core/index`

A keyword search index over all Opax data. Stored as a single orphan branch. This is a portable fallback for tools reading git directly — the SDK's read path uses FTS5 via SQLite, not this index.

```
oa/core/index/
└── index.json
```

The index is always rebuildable from the underlying branches.

---

## 3. Git Notes

Notes are annotations attached to existing commits without modifying the commit hash. Each namespace stores a different category of annotation. Notes are the **default mechanism** for attaching Opax metadata to commits (preferred over trailers).

### 3.1 First-Party Namespaces

| Namespace | Purpose | Typical Writer |
|---|---|---|
| `refs/notes/oa-sessions` | Links commits to session archives | post-commit hook |
| `refs/notes/oa-reviews` | Code review assessments | Review workflow stage |
| `refs/notes/oa-tests` | Test results | Test action / CI |
| `refs/notes/oa-evals` | Eval scores | Eval action |
| `refs/notes/oa-gates` | Approval records | Workflow gate resolution |
| `refs/notes/oa-ci` | General CI results | Lint/build actions |

### 3.2 Note Content Format

All notes are JSON objects with a `version` field.

**Session link note (`oa-sessions`):**

```json
{
  "version": 1,
  "session_id": "ses_01JQXYZ...",
  "agent": "claude-code",
  "stage": "feature-development/implement",
  "workflow_id": "wf_01JQXYZ...",
  "duration_seconds": 2700
}
```

**Review note (`oa-reviews`):**

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

**Test result note (`oa-tests`):**

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

**Eval note (`oa-evals`):**

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

The `oa-` prefix is reserved for first-party namespaces. Community/third-party namespaces use `oa-ext-{name}`. Third-party tools define their own schemas. The SDK provides generic read/write methods for any namespace.

### 3.4 Notes Distribution

`git push` does not push notes by default. The SDK configures push refspecs for `refs/notes/oa-*` during `opax init`. Auto-push notes to remote on commit is configurable but off by default — explicit `opax push` or `git push` with configured refspecs.

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

Trailers are queryable via `git log --format="%(trailers)"`. When trailers are disabled (default), the same metadata is stored as git notes via `refs/notes/oa-sessions`.

---

## 5. Custom Refs

Refs are lightweight pointers to git objects. Opax uses custom refs under `refs/oa/` for application state that needs to survive process restarts.

| Ref | Points to | Purpose |
|---|---|---|
| `refs/oa/workflows/active` | Blob | JSON listing active workflow instances |
| `refs/oa/config` | Blob | Repository-level Opax configuration |

Updated atomically via `git update-ref`. Inspectable: `git show refs/oa/workflows/active`.

---

## 6. Annotated Tags

Tags mark significant workflow milestones. Annotated tags store tagger, timestamp, and message — suitable for audit trails.

```
oa/milestone/{workflow-name}/{description}
```

```bash
git tag -a "oa/milestone/feature-development/auth-complete" \
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
CREATE TABLE oa_contexts (
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
  privacy_tier TEXT DEFAULT 'public',
  privacy_scrubbed BOOLEAN DEFAULT FALSE,
  privacy_encrypted BOOLEAN DEFAULT FALSE,
  git_branch TEXT NOT NULL,
  git_commit TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Tag junction table for indexed lookups
CREATE TABLE oa_context_tags (
  context_id TEXT NOT NULL REFERENCES oa_contexts(id),
  tag TEXT NOT NULL,
  PRIMARY KEY (context_id, tag)
);
CREATE INDEX idx_context_tags_tag ON oa_context_tags(tag);

-- Session archives
CREATE TABLE oa_sessions (
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
  privacy_tier TEXT DEFAULT 'team',
  git_branch TEXT NOT NULL,
  git_commit TEXT NOT NULL
);

CREATE TABLE oa_session_tags (
  session_id TEXT NOT NULL REFERENCES oa_sessions(id),
  tag TEXT NOT NULL,
  PRIMARY KEY (session_id, tag)
);
CREATE INDEX idx_session_tags_tag ON oa_session_tags(tag);

-- Git notes (all namespaces)
CREATE TABLE oa_notes (
  commit_hash TEXT NOT NULL,
  namespace TEXT NOT NULL,
  content TEXT NOT NULL,  -- JSON
  created_at TEXT,
  PRIMARY KEY (commit_hash, namespace)
);
CREATE INDEX idx_notes_namespace ON oa_notes(namespace);

-- Typed views over notes
CREATE VIEW oa_reviews AS
  SELECT commit_hash,
    json_extract(content, '$.reviewer') AS reviewer,
    json_extract(content, '$.verdict') AS verdict,
    json_extract(content, '$.summary') AS summary,
    json_extract(content, '$.reviewed_at') AS reviewed_at,
    content AS raw
  FROM oa_notes WHERE namespace = 'oa-reviews';

CREATE VIEW oa_test_results AS
  SELECT commit_hash,
    json_extract(content, '$.passed') AS passed,
    json_extract(content, '$.failed') AS failed,
    json_extract(content, '$.skipped') AS skipped,
    json_extract(content, '$.duration_ms') AS duration_ms,
    json_extract(content, '$.framework') AS framework,
    json_extract(content, '$.ran_at') AS ran_at,
    content AS raw
  FROM oa_notes WHERE namespace = 'oa-tests';

CREATE VIEW oa_eval_scores AS
  SELECT commit_hash,
    json_extract(content, '$.scores') AS scores,
    json_extract(content, '$.model') AS model,
    json_extract(content, '$.evaluated_at') AS evaluated_at,
    content AS raw
  FROM oa_notes WHERE namespace = 'oa-evals';

-- Workflow instances
CREATE TABLE oa_workflows (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,  -- active | completed | failed | cancelled
  manifest TEXT,  -- JSON
  git_branch TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE oa_workflow_stages (
  workflow_id TEXT NOT NULL REFERENCES oa_workflows(id),
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
CREATE VIRTUAL TABLE oa_contexts_fts USING fts5(
  id, title, content, tags,
  content=oa_contexts,
  content_rowid=rowid
);

CREATE VIRTUAL TABLE oa_sessions_fts USING fts5(
  id, agent, branch, tags,
  content=oa_sessions,
  content_rowid=rowid
);

-- Triggers to keep FTS in sync
CREATE TRIGGER oa_contexts_ai AFTER INSERT ON oa_contexts BEGIN
  INSERT INTO oa_contexts_fts(rowid, id, title, content, tags)
  VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;

CREATE TRIGGER oa_contexts_ad AFTER DELETE ON oa_contexts BEGIN
  INSERT INTO oa_contexts_fts(oa_contexts_fts, rowid, id, title, content, tags)
  VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
END;

-- Materializer state tracking
CREATE TABLE oa_materializer_state (
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
- `query()` — handles dialect differences (`json_extract` vs `->>`operators, FTS5 vs `tsvector`)
- `sync()` — incremental materialization from git to database
- `rebuild()` — full rebuild from git

The local SDK always uses the SQLite adapter. The hosted control plane uses the Postgres adapter. The SDK's public API is unchanged regardless of backend.

### 7.4 Sync Behavior

The `oa_materializer_state` table tracks a `git_head` value. When the SDK detects that HEAD has changed (e.g., after `git pull`), it runs an incremental sync to materialize new records. Current leaning: lazy sync (on first read after HEAD changes) with a stale-data indicator, not eager background sync.

---

## 8. Storage Constraints

Git was designed for source code, not a general-purpose data platform. The spec defines constraints to keep repositories healthy.

**Data types:** Text and JSON only. No binaries on orphan branches. Reference binary artifacts by URL or path.

**Size budget:** See *Storage & Scaling Spec* for detailed capacity math. The recommended soft cap is 2 GB of total Opax data per repository, with compaction and archive repos for larger volumes.

**Network transfer:** Configure refspecs so `git fetch` only pulls code branches by default. Opax data fetched on demand. The SDK auto-configures this during `opax init`.

---

## 9. Git Platform Compatibility

| Platform | Orphan branches | Notes | Custom refs | Tags |
|---|---|---|---|---|
| GitHub | ✅ (soft limit ~10k) | ✅ (no web UI) | ✅ (some push rules may reject) | ✅ |
| GitLab | ✅ | ✅ | ✅ | ✅ |
| Bitbucket | ✅ | ✅ (limited) | ⚠️ (test required) | ✅ |

The SDK includes `opax init --test-platform` which verifies remote platform compatibility and configures push/fetch refspecs accordingly.

---

## 10. MCP Tool Schemas

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
