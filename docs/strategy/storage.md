# Opax — Storage & Scaling Spec

**Version:** 1.0.0-draft
**Date:** March 16, 2026
**Companion to:** Opax PRD v4

---

## Overview

Git was designed for source code, not high-frequency structured data from multiple concurrent agents. This spec addresses the gap between git's design constraints and Opax's storage requirements through capacity planning, branch consolidation strategies, tiered retention, archive repositories, and the database abstraction layer.

---

## 1. Capacity Math

### Per-Session Storage

| Component | Size | Notes |
|---|---|---|
| metadata.json | ~2 KB | Structured JSON |
| transcript.md | ~200 KB | Full conversation (varies widely) |
| diff.patch | ~50 KB | Unified diff of changes |
| summary.md | ~10 KB | Auto-generated summary |
| Git overhead | ~3 KB | Object headers, tree entries |
| **Total** | **~265 KB** | Per session archive |

### Per-Developer Daily Volume

Assuming a heavily agent-assisted developer (30 sessions/day across multiple agents):

| Data Type | Volume | Calculation |
|---|---|---|
| Session archives | ~7.9 MB | 30 × 265 KB |
| Context artifacts | ~0.4 MB | 8 × 50 KB |
| Workflow + action logs | ~9 MB | 30 × 300 KB (3-4 stages per session) |
| Git notes, index overhead | ~1 MB | Estimates |
| **Total** | **~18-20 MB/day** | |

### Team Scaling (5 developers)

| Timeframe | Raw Data | With Git Overhead (~15%) | With Encryption (~3-5x on content) |
|---|---|---|---|
| Daily | ~100 MB | ~115 MB | ~250-400 MB |
| Monthly | ~3 GB | ~3.5 GB | ~7.5-12 GB |
| Yearly | ~36 GB | ~41 GB | ~90-180 GB |
| 5 years | ~180 GB | ~207 GB | ~450-900 GB |

These numbers vastly exceed the PRD's 2 GB soft cap and GitHub's recommended 5 GB repo size limit. Without mitigation, performance degrades: clone times spike, fetch operations slow, ref enumeration becomes costly.

### Branch Counting

If every record gets its own orphan branch:
- 5 developers × 30 sessions/day × 30 days = **4,500 branches/month** just for sessions
- Add context artifacts, workflows, actions: easily **6,000-8,000 branches/month**
- GitHub soft limit: ~10,000 branches

At this rate, the branch limit is hit in under 2 months. Per-record branches do not scale for teams.

---

## 2. Branch Consolidation Strategy

### The Decision

Per-record orphan branches (conceptually clean, one branch = one record) don't scale. The alternative is consolidated branches per data type, where multiple records share a branch as files in a directory structure.

Since SQLite is the read path (not branch enumeration), and the SDK abstracts git operations, the consolidation complexity is hidden inside the SDK. Users and plugins interact with record IDs; the SDK maps IDs to their git storage location.

### Consolidated Branch Model

Instead of `oa/memory/context/{id}` as a separate orphan branch per artifact, use a smaller number of time-bucketed branches:

```
oa/memory/context/2026-03/      # all context artifacts from March 2026
├── ctx_01JQXYZ.../
│   ├── metadata.json
│   └── content.md
├── ctx_01JQABC.../
│   ├── metadata.json
│   └── content.md
└── ...
```

**Bucketing:** Monthly by default. Each month gets one orphan branch per data type. A commit is added to the branch for each new record (or batch of records).

**Branch count math (consolidated):**
- Data types: ~4 (contexts, sessions, workflows, actions)
- Buckets per year: 12
- **Total branches per year: ~48** (vs. ~72,000 with per-record model)

This stays well under any platform limit indefinitely.

**Write mechanics:** Adding a record means:
1. Check out the current month's branch for that data type
2. Add the record's files to the tree
3. Commit
4. Update SQLite index

This is more complex than creating a new orphan branch, but the SDK handles it transparently. Concurrent writes to the same branch are serialized via `.git/opax.lock`.

**Read mechanics:** Unchanged. The SQLite index maps record IDs to `(branch, commit, path)` tuples. Reads go through SQLite, not branch enumeration.

### Migration Path

Phase 0 can start with either model. If starting with per-record branches (simpler to implement), migration to consolidated branches is a one-time operation: read all records from individual branches, write them to consolidated branches, delete individual branches, rebuild SQLite index. This can be shipped as `opax storage migrate` when scaling becomes an issue.

---

## 3. Tiered Retention

### Retention Tiers

| Tier | Age | Storage Form | Queryable Via |
|---|---|---|---|
| Hot | 0-30 days | Individual records on consolidated branches | SQLite (full content) |
| Warm | 30-90 days | Compacted daily summaries | SQLite (metadata + summary, full content on demand from git) |
| Cold | 90+ days | Moved to archive repo | SQLite (metadata only, content from archive repo on demand) |

### Compaction Process

**Hot → Warm (30 days):**
- Individual session archives older than 30 days are merged into daily summary records
- Each daily summary contains: session count, total duration, agents used, branches touched, aggregated file change stats, list of session IDs
- Individual session transcripts and diffs are preserved in git history (accessible via `git log` on the consolidated branch) but not stored as separate files
- Context artifacts are NOT compacted — they're retained at full fidelity longer than sessions

**Warm → Cold (90 days):**
- Daily summaries and their underlying git history are moved to the archive repo
- The primary repo's consolidated branches are pruned to remove old commits
- SQLite retains metadata-only records pointing to the archive repo
- `git gc` runs after pruning to reclaim space

### Compliance Override

When compliance mode is enabled (see *Compliance Framework*), the retention floor overrides compaction:

```yaml
storage:
  retention:
    individual: 30d
    compaction: 90d
    context: 365d
    compliance_floor: 3y  # overrides all above; data moved to archive, never deleted
```

---

## 4. Archive Repositories

### Purpose

Archive repos store Opax data that's been retired from the primary repository. They're standard git repos containing only Opax orphan branches. This keeps the primary repo performant while preserving historical data for compliance, analytics, and forensics.

### Architecture

```
Primary Repo (.git/)
├── oa/memory/context/2026-03/    (hot + warm)
├── oa/memory/sessions/2026-03/   (hot + warm)
└── .git/opax/opax.db             (indexes everything)

Archive Repo (.git/)
├── oa/memory/context/2025-12/    (cold)
├── oa/memory/sessions/2025-12/   (cold)
└── (no SQLite — indexed by primary repo's SQLite)
```

### Operations

**Archive:** `opax storage archive` moves branches older than the compaction threshold to the archive repo. Updates SQLite to reference the archive.

**Query spanning:** The SDK checks the archive repo when a record isn't found in the primary repo. This is transparent to callers — the same `opax.memory.context.get(id)` call works regardless of where the data lives.

**Archive repo location:** Configurable. Defaults to a sibling directory (`../repo-opax-archive/`). Can also be a remote URL (the SDK clones/fetches on demand).

### Hosted Tier

In the hosted control plane (Phase 2+), archive repos are managed automatically. The Postgres-backed materialized view indexes all repos (primary + archives) in a single query surface. No manual archive management needed.

---

## 5. StorageBackend Interface

### Purpose

Abstract database dialect differences so the SDK's public API is unchanged whether SQLite or Postgres is underneath. The local SDK always uses SQLite. The hosted control plane uses Postgres.

### Interface

```typescript
interface StorageBackend {
  // Schema management
  initSchema(): Promise<void>;
  migrateSchema(fromVersion: number, toVersion: number): Promise<void>;

  // Read operations
  query<T>(sql: string, params?: unknown[]): Promise<T[]>;
  queryOne<T>(sql: string, params?: unknown[]): Promise<T | null>;

  // Write operations
  execute(sql: string, params?: unknown[]): Promise<{ changes: number }>;
  executeMany(statements: Array<{ sql: string; params?: unknown[] }>): Promise<void>;

  // Full-text search (dialect-specific)
  search(table: string, query: string, options?: SearchOptions): Promise<SearchResult[]>;

  // Sync
  sync(gitHead: string): Promise<SyncResult>;
  rebuild(): Promise<void>;

  // Transaction support
  transaction<T>(fn: (tx: StorageBackend) => Promise<T>): Promise<T>;
}

interface SearchOptions {
  limit?: number;
  offset?: number;
  filters?: Record<string, unknown>;
}
```

### SQLite Adapter

- Uses `better-sqlite3` (synchronous, fastest Node SQLite binding)
- FTS5 for full-text search
- `json_extract()` for JSON column queries
- WAL mode for concurrent reads
- Database at `.git/opax/opax.db`

### Postgres Adapter (Phase 2)

- Uses `pg` with connection pooling
- `tsvector` / `to_tsquery` for full-text search
- `->>`  / `jsonb_extract_path_text` for JSON queries
- JSONB columns with GIN indexes for efficient filtering
- `pgvector` extension for future semantic search
- `LISTEN`/`NOTIFY` for real-time change notifications (hosted Studio)

### Upgrade Path

Upgrading from local to hosted is configuration, not migration:

1. Configure `StorageBackend` to use Postgres
2. Run `opax db rebuild` (reads all data from git, writes to Postgres)
3. No data migration — the database is always derived from git

---

## 6. Git Operations Performance

### Refspec Configuration

The SDK configures refspecs during `opax init` to prevent Opax data from inflating normal git operations:

```gitconfig
[remote "origin"]
  # Default fetch only pulls code branches
  fetch = +refs/heads/*:refs/remotes/origin/*

  # Opax data fetched on demand
  fetch = +refs/notes/oa-*:refs/notes/oa-*

  # Opax branches NOT fetched by default
  # Use: opax pull (fetches oa/* branches)

[push]
  # Notes pushed explicitly
  # Use: opax push (pushes oa/* branches + notes)
```

### Object Packing

`opax storage compact` runs `git gc` after compaction to repack objects. For repos with heavy Opax usage, aggressive packing settings help:

```gitconfig
[pack]
  windowMemory = 256m
  threads = 4
```

### Clone Performance

A fresh clone of a repo with Opax data should NOT clone all Opax branches by default. The `opax init` refspec configuration ensures this. After cloning:

1. `opax init` sets up refspecs and creates the local SQLite database
2. `opax pull` fetches Opax branches (can be scoped: `opax pull --since 30d`)
3. SQLite materializes fetched data

This means `git clone` remains fast regardless of Opax data volume. Opax data is fetched incrementally on demand.

---

## 7. Size Monitoring

### `opax storage stats`

Reports current storage usage:

```
Opax Storage Report
───────────────────
Repository:     /path/to/repo
Data branches:  48 (consolidated)
Archive repo:   ../repo-opax-archive/ (23 branches)

Data Type        Count    Size       Oldest
─────────────── ─────── ─────────── ──────────
Context          234     12.3 MB     2026-01-15
Sessions         1,847   489.2 MB    2026-01-15
Workflows        67      8.4 MB      2026-02-01
Actions          312     156.7 MB    2026-02-01
Notes            2,190   4.2 MB      2026-01-15
Index            1       0.8 MB      —

Total (primary): 671.6 MB
Total (archive): 2.3 GB
SQLite DB:       45.2 MB

Recommendations:
  ⚠ 234 sessions older than 30d — run `opax storage compact`
  ✓ Branch count within limits (48 < 5000)
  ✓ Primary repo under 2 GB cap
```

### Alerts

`opax doctor` includes storage health checks:

- Primary repo size approaching 2 GB cap
- Branch count approaching configured limit
- Compaction overdue (last run > configured interval)
- Archive repo unreachable
- SQLite database out of sync with git

---

## 8. Scaling Recommendations

| Team Size | Daily Volume | Strategy |
|---|---|---|
| Solo developer | ~20 MB | Default settings. Per-record branches fine initially. |
| 2-5 developers | ~100 MB | Consolidated branches. Monthly compaction. Archive repo for >90d data. |
| 5-15 developers | ~300 MB | Consolidated branches. Weekly compaction. Archive repo. Consider hosted tier. |
| 15+ developers | ~1+ GB | Hosted tier recommended. Postgres materialization. Managed archive storage. |

For all team sizes, encrypted repos (~3-5x multiplier on content) should use aggressive compaction and archive repos from day one.
