# Git Tree Sharding Research

**Date:** March 17, 2026
**Context:** Evaluating whether Opax needs directory sharding on `opax/v1`, and if so, what scheme to use.

---

## Question

Opax stores records on a single orphan branch (`opax/v1`) with type directories (`sessions/`, `saves/`, etc.). Each new record adds an entry to a type directory. Should those directories be flat, or sharded into subdirectories?

---

## 1. Git Tree Object Internals

Each tree entry is stored as: `<mode> SP <name> NUL <20-byte-SHA1>` — about 28 + filename length bytes per entry.

Tree objects are **immutable**. Adding one entry to a directory with N existing entries requires:

1. Read the existing tree via `git ls-tree` — serializes all N entries as text
2. Append the new entry to the text stream
3. Write the new tree via `git mktree` — parses all N+1 entries, sorts them, writes binary tree object
4. The old tree object remains (git is append-only)

This is **O(N) per append** — the entire directory is rewritten every time.

---

## 2. Benchmarks: Flat vs Sharded Append

The critical operation for Opax is "add one record to a type directory." Measured on macOS with git plumbing commands:

### Flat directory (all entries in one tree)

| Entries (N) | mktree time | ls-tree time | ls-tree single path | diff-tree | commit-tree | Tree obj size |
|------------|------------|-------------|---------------------|-----------|-------------|---------------|
| 100 | 0.012s | 0.036s | 0.042s | — | 0.034s | 4.3 KB |
| 1,000 | 0.034s | 0.034s | 0.034s | 0.033s | 0.033s | 43 KB |
| 10,000 | 0.274s | 0.035s | 0.032s | 0.034s | 0.032s | 430 KB |
| 50,000 | 1.33s | 0.050s | 0.038s | 0.045s | 0.032s | 2.1 MB |
| 100,000 | 2.36s | 0.072s | 0.045s | 0.041s | 0.031s | 4.3 MB |

Key observation: **mktree scales linearly** (it must parse/sort/write all entries). **commit-tree is O(1)** (it just stores the tree's hash). **ls-tree and diff-tree are fast** even at scale.

### Append-one-entry cost: flat vs sharded (256 buckets)

| Total records | Flat append | Sharded append | Speedup |
|-------------|------------|----------------|---------|
| 1,000 | 42ms | 53ms | 0.8x (sharded slower — overhead) |
| 5,000 | 170ms | 48ms | **3.5x** |
| 10,000 | 276ms | 59ms | **4.7x** |
| 50,000 | 1.20s | 57ms | **21x** |
| 100,000 | 2.47s | 65ms | **38x** |

**Crossover point: ~2,000-3,000 entries.** Below that, the overhead of two-level tree manipulation makes sharding slightly slower. Above that, sharding wins increasingly because:

- **Flat append:** read all N entries + write N+1 entries = O(N)
- **Sharded append:** read root (256 entries) + read 1 shard (~N/256 entries) + write 1 shard + write root = O(N/256 + 256)

The sharded append cost is **essentially constant** at ~55-65ms regardless of total entry count.

---

## 3. Packfile Delta Compression

Git's packfile format aggressively delta-compresses similar tree objects:

- 100 sequential commits on a 10k-entry tree where each commit changes 1 file: **99% of tree objects are delta-compressed**
- A 430 KB tree object (10k entries) that changes one entry compresses to a delta of ~50-80 bytes in the packfile
- Both flat and sharded layouts compress well, but sharded has smaller individual tree objects that delta-compress more efficiently

**Storage cost is negligible** after `git gc` regardless of layout.

---

## 4. Never-Checked-Out Branch

When the branch is never checked out (plumbing-only access):

- **No index file overhead.** `.git/index` is never created or updated
- **No working tree operations.** No checkout, read-tree, or stat() calls
- **Sparse reachable set algorithm** (default since Git 2.27) helps during push — only walks changed trees, not all trees. Pushing one new commit that changes one shard avoids walking unchanged subtrees

---

## 5. How Entire.io Does It

**Source:** `entireio/cli` on GitHub, third-party parsers (`Signet-AI/signetai`, `specstoryai/getspecstory`).

Entire uses **2-character hex prefix sharding** on their checkpoints branch:

```
entire/checkpoints/v1/
  <id[:2]>/<id[2:]>/
    metadata.json
    0/
      metadata.json
      full.jsonl
      prompt.txt
      context.md
      content_hash.txt
    1/
      ...
```

A 12-hex checkpoint ID like `a3b2c4d5e6f7` maps to path `a3/b2c4d5e6f7/`. This gives **256 shards**.

They also use:
- Commit trailers: `Entire-Checkpoint: a3b2c4d5e6f7`
- Shadow branches during active sessions: `entire/<base[:7]>-<worktreeHash[:6]>`
- Plumbing-only reads: `git ls-tree -r`, `git show <branch>:<path>`
- Written in Go, using git hooks (prepare-commit-msg, commit-msg, post-commit, pre-push)

---

## 6. Opax Capacity Context

At projected volumes (5-developer team):

| Record type | Monthly volume | Time to reach 2k-3k crossover |
|------------|---------------|-------------------------------|
| Sessions | 4,500 | < 1 month |
| Saves | 4,500 | < 1 month |
| Context artifacts | 1,200 | 2 months |
| Workflows | ~200 | 10+ months |
| Actions | ~900 | 2-3 months |

Sessions and saves cross into painful territory within the first month. Flat directories are not viable for the primary record types.

---

## 7. Sharding Scheme Options

### Option A: Hash-based (recommended)

Shard on first 2 hex chars of SHA-256 of the full record ID.

- `ctx_01JQXYZ...` → `sha256("ctx_01JQXYZ...")[:2]` → e.g. `a3/`
- **256 buckets**, uniformly distributed regardless of creation time
- Matches Entire.io's approach
- Deterministic: same ID always maps to same shard
- Loses human-readability (can't eyeball which shard a record is in)

### Option B: ULID-prefix-based

Shard on first 2 chars of the ULID portion (after type prefix + underscore).

- `ctx_01JQXYZ...` → `01/`
- **1,024 buckets** (32² Crockford base32 characters)
- Distribution is time-correlated — ULID first chars rotate slowly, so recent records cluster
- Human-readable: you can roughly tell when a shard's records were created
- Worse distribution than hash-based under bursty workloads

### Option C: No sharding (flat)

- Simplest implementation
- Works fine for months 1-2 at small team sizes
- Degrades linearly and becomes a problem quickly for sessions/saves
- Would require retrofitting sharding later (migration pain)

---

## 8. Recommendation

**Option A (hash-based, 256 buckets).** It matches Entire.io's proven production pattern, gives uniform distribution independent of creation time, and keeps append cost constant at ~60ms regardless of scale. The loss of human-readability doesn't matter because records are always accessed via SQLite lookup, not by browsing the tree.
