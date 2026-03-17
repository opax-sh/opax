# Opax — Storage & Scaling Spec

**Version:** 2.0.0-draft
**Date:** March 17, 2026
**Companion to:** Opax PRD v2.0.0

---

## Overview

Git was designed for source code, not high-frequency structured data from multiple concurrent agents. Opax addresses this with a two-tier storage model (metadata in git, bulk content in content-addressed storage), a single consolidated branch, four-tier retention, and a database abstraction layer.

---

## 1. Two-Tier Storage Model

### Principle

Metadata and references live in git. Bulk content (transcripts, diffs, action logs) lives in content-addressed storage (CAS) at `.git/opax/content/`, referenced by SHA-256 hash from git metadata.

### Why Two Tiers

Git metadata benefits from:
- Integrity (cryptographic linking)
- Distribution (`git push/pull`)
- Delta compression (similar JSON files compress well)
- Immutability (append-only history)

Bulk content does NOT benefit from git storage:
- Transcripts are large (~200 KB each) and unique (poor delta compression)
- High volume (~30 sessions/day per developer)
- Encryption eliminates compression (3-5x size multiplier)

---

## 2. Capacity Math

### Git Tier (Metadata Only)

| Component | Size | Notes |
|---|---|---|
| metadata.json per session | ~2 KB | Structured JSON |
| summary.md per session | ~10 KB | Auto-generated |
| save metadata | ~1 KB | Commit anchor |
| Git overhead per record | ~1 KB | Object headers, tree entries |
| **Total per session** | **~14 KB** | Metadata only |

### Per-Developer Daily Volume (Git)

| Data Type | Volume | Calculation |
|---|---|---|
| Session metadata | ~420 KB | 30 × 14 KB |
| Context metadata | ~80 KB | 8 × 10 KB |
| Workflow + action metadata | ~60 KB | 30 × 2 KB |
| **Total** | **~560 KB/day** | |

### Team Scaling — Git Tier (5 developers)

| Timeframe | Git Data | With Encryption (~3-5x on metadata) |
|---|---|---|
| Daily | ~2.8 MB | ~8-14 MB |
| Monthly | ~84 MB | ~252-420 MB |
| Yearly | ~1 GB | ~3-5 GB |

This is within comfortable limits for any git hosting platform.

### CAS Tier (Bulk Content)

| Component | Size | Notes |
|---|---|---|
| transcript.md | ~200 KB | Full conversation (varies widely) |
| diff.patch | ~50 KB | Unified diff of changes |
| stdout/stderr | ~20 KB | Action output |
| **Total per session** | **~270 KB** | |

### Team Scaling — CAS Tier (5 developers)

| Timeframe | CAS Data |
|---|---|
| Daily | ~40 MB |
| Monthly | ~1.2 GB |
| Yearly | ~15 GB |

CAS data lives on the local filesystem and does not affect git operations (clone, fetch, push). It can be selectively archived, compressed, or moved to object storage.

---

## 3. Single Consolidated Branch

### The Decision

All Opax data lives on a single orphan branch: `opax/data/v1`. Records are organized in a sharded directory structure using the first two hex characters of `sha256(record_id)`, giving 256 uniformly distributed buckets. Adopted from Entire.io's `entire/checkpoints/v1` pattern. See `docs/misc/sharding-research.md` for benchmarks.

### Why Single Branch

Per-record orphan branches don't scale:
- 5 developers × 30 sessions/day × 30 days = **4,500 branches/month** just for sessions
- Add context artifacts, workflows, actions: easily **6,000-8,000 branches/month**
- GitHub soft limit: ~10,000 branches

A single branch:
- **Branch count: 1.** Always. Regardless of data volume.
- Git shares tree objects between commits (directory structure is mostly stable)
- Delta compression works across full history
- Ref enumeration stays fast

### Write Mechanics

Adding a record uses git plumbing commands (never checkout):

1. `git hash-object -w` — write content files as git blobs
2. `git mktree` — create tree objects for the directory structure
3. `git commit-tree` — create commit pointing to new tree, with parent as current branch tip
4. `git update-ref` — update `opax/data/v1` to point to new commit

Concurrent writes serialized via `.git/opax.lock`.

Alternatively, a git library (go-git) performs these operations without shelling out.

### Read Mechanics

SQLite maps record IDs to `(commit, path)` tuples. Reads go through SQLite, not branch enumeration.

---

## 4. Content-Addressed Storage (CAS)

### Layout

```
.git/opax/content/
├── a1/
│   └── b2c3d4e5f6...
├── e5/
│   └── f6g7h8i9j0...
└── ...
```

Files sharded by first two characters of SHA-256 hash, mirroring git's object storage layout.

### Operations

**Write:** SHA-256 hash computed → file written to `.git/opax/content/{hash[0:2]}/{hash[2:]}` → hash recorded in metadata on branch.

**Read:** SQLite lookup → `content_hash` → read file from CAS → optional integrity verification via `sha256sum`.

**Dedup:** Content-addressing provides natural deduplication. Same content = same hash = one copy.

### What Goes Where

| Content Type | Storage | Rationale |
|---|---|---|
| `metadata.json` | Git (on branch) | Small, structured, benefits from git delta compression |
| `summary.md` | Git (on branch) | Small, useful for quick inspection |
| `content.md` (< 4 KB) | Git (on branch) | Small enough to inline |
| `content.md` (>= 4 KB) | CAS | Too large for efficient git storage |
| `transcript.md` | CAS | Large, high-volume |
| `diff.patch` | CAS | Large, high-volume |
| `stdout.log` / `stderr.log` | CAS | Large, high-volume |

### Compaction

CAS files older than the hot tier threshold can be:
1. Compressed (gzip) in place
2. Moved to warm/cold storage
3. Bundled for archive

The `content_hash` in git metadata is immutable — it always points to the original content, regardless of where it physically lives.

---

## 5. Archive Tiers

### Four-Tier Retention

| Tier | Age | Storage | Query Surface |
|---|---|---|---|
| **Hot** | 0-30 days | Same repo (`opax/data/v1` branch) + local CAS | SQLite (full content via CAS) |
| **Warm** | 30-90 days | Git remote (archive repo) | SQLite (metadata only, content fetched on demand from archive remote) |
| **Cold** | 90+ days | Git bundles on object storage | SQLite (metadata only, content from downloaded bundle) |
| **Hosted** | All | Git alternates (shared object pool) | Postgres (full cross-repo query surface) |

### Hot Tier (0-30 days)

Default state. All metadata on the `opax/data/v1` branch. All bulk content in local CAS. Full query access via SQLite FTS5.

### Warm Tier (30-90 days)

**Archive operation:** `opax storage archive --warm`

1. Old metadata commits are pushed to a configured archive remote
2. Corresponding CAS files are transferred to the archive remote's CAS
3. Local copies are removed (metadata pruned from branch, CAS files deleted)
4. SQLite retains metadata-only records with `archive_location` pointing to the remote

**Query:** Metadata queries work normally. Bulk content requires `opax storage fetch <id>` to pull from the archive remote on demand.

### Cold Tier (90+ days)

**Archive operation:** `opax storage archive --cold`

1. Warm data is bundled into git bundles (`.bundle` files)
2. Corresponding CAS files are tarred and compressed
3. Both are uploaded to configured object storage (S3, GCS, R2)
4. Archive remote copies are cleaned up
5. SQLite retains metadata-only records with `archive_location` pointing to object storage

**Query:** Metadata queries work normally. Restoring content requires downloading the bundle + CAS archive, then `git fetch` from the bundle.

### Hosted Tier

For teams using the hosted control plane:

- Git alternates provide a shared object pool across repos
- Postgres materializes all data (hot through cold) in a single query surface
- No manual archive management needed
- CAS distribution handled automatically

### Compliance Override

When compliance mode is enabled (see *Compliance Framework*), the retention floor overrides archival:

```yaml
storage:
  retention:
    hot: 30d
    warm: 90d
    context: 365d
    compliance_floor: 3y  # data archived, never deleted
```

---

## 6. StorageBackend Interface

### Purpose

Abstract database dialect differences so the SDK's public API is unchanged whether SQLite or Postgres is underneath. The local SDK always uses SQLite. The hosted control plane uses Postgres.

### Interface

```go
type StorageBackend interface {
    // Schema management
    InitSchema() error
    MigrateSchema(fromVersion, toVersion int) error

    // Read operations
    Query(sql string, params ...any) ([]map[string]any, error)
    QueryOne(sql string, params ...any) (map[string]any, error)

    // Write operations
    Execute(sql string, params ...any) (int64, error)
    ExecuteMany(statements []Statement) error

    // Full-text search (dialect-specific)
    Search(table, query string, opts SearchOptions) ([]SearchResult, error)

    // Sync
    Sync(gitHead string) (SyncResult, error)
    Rebuild() error

    // Transaction support
    Transaction(fn func(tx StorageBackend) error) error
}
```

### SQLite Adapter

- Uses `modernc.org/sqlite` (pure Go, no CGo dependencies)
- FTS5 for full-text search
- `json_extract()` for JSON column queries
- WAL mode for concurrent reads
- Database at `.git/opax/opax.db`

### Postgres Adapter (Phase 2)

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

## 7. Git Operations Performance

### Refspec Configuration

The SDK configures refspecs during `opax init` to prevent Opax data from inflating normal git operations:

```gitconfig
[remote "origin"]
  # Default fetch only pulls code branches
  fetch = +refs/heads/*:refs/remotes/origin/*

  # Opax data fetched on demand
  fetch = +refs/notes/opax-*:refs/notes/opax-*

  # Opax branch NOT fetched by default
  # Use: opax pull (fetches opax/data/v1)

[push]
  # Notes pushed explicitly
  # Use: opax push (pushes opax/data/v1 + notes)
```

### Object Packing

`opax storage compact` runs `git gc` after compaction to repack objects. For repos with heavy Opax usage, aggressive packing settings help:

```gitconfig
[pack]
  windowMemory = 256m
  threads = 4
```

### Clone Performance

A fresh clone should NOT clone the Opax branch by default. The refspec configuration ensures this. After cloning:

1. `opax init` sets up refspecs and creates the local SQLite database
2. `opax pull` fetches the `opax/data/v1` branch (can be scoped: `opax pull --since 30d`)
3. SQLite materializes fetched data
4. CAS files are fetched on demand or in bulk

This means `git clone` remains fast regardless of Opax data volume.

---

## 8. Size Monitoring

### `opax storage stats`

Reports current storage usage:

```
Opax Storage Report
───────────────────
Repository:     /path/to/repo
Data branch:    opax/data/v1 (1 branch)
CAS:            .git/opax/content/ (1,847 files)
Archive:        s3://opax-archive/repo (warm + cold)

Data Type        Count    Git Size    CAS Size    Oldest
─────────────── ─────── ─────────── ─────────── ──────────
Context          234     2.3 MB      8.2 MB      2026-01-15
Sessions         1,847   24.0 MB     489.2 MB    2026-01-15
Saves      1,847   1.8 MB      —           2026-01-15
Workflows        67      0.5 MB      6.4 MB      2026-02-01
Actions          312     0.6 MB      156.7 MB    2026-02-01
Notes            2,190   4.2 MB      —           2026-01-15

Total (git):     33.4 MB
Total (CAS):     660.5 MB
Total (archive): 2.3 GB
SQLite DB:       45.2 MB

Recommendations:
  ⚠ 234 sessions older than 30d — run `opax storage archive --warm`
  ✓ Branch count: 1 (single consolidated)
  ✓ Git tier well under 2 GB cap
```

### Alerts

`opax doctor` includes storage health checks:

- Git tier approaching configured cap
- CAS directory exceeding configured size
- Archive overdue (last run > configured interval)
- Archive remote unreachable
- SQLite database out of sync with git

---

## 9. Scaling Recommendations

| Team Size | Daily Git Volume | Daily CAS Volume | Strategy |
|---|---|---|---|
| Solo developer | ~0.5 MB | ~8 MB | Default settings. Hot tier only for months. |
| 2-5 developers | ~2-3 MB | ~40 MB | Monthly warm archival. Cold after 90d. |
| 5-15 developers | ~6-9 MB | ~120 MB | Weekly warm archival. Cold after 90d. Consider hosted tier. |
| 15+ developers | ~20+ MB | ~400+ MB | Hosted tier recommended. Postgres materialization. Managed archive. |

For all team sizes, encrypted repos (~3-5x multiplier on metadata) should use warm archival from day one.
