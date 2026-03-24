# FEAT-0007 - Write Records To Branch

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management)
**Dependents:** E4 write orchestrator, E5 rebuild inputs, E8 memory plugin, E9 init/storage stats

---

## Problem

This is the hardest write path in Phase 0. Opax needs to append structured records to a single orphan branch without checking it out, without clobbering concurrent writes, and without trusting user-supplied paths.

The Phase 0 concurrency contract is optimistic:

- immutable object creation is concurrent
- mutable ref publication is compare-and-swap (CAS) on `refs/heads/opax/v1`
- retries are bounded and internal
- repo-wide `.git/opax.lock` is not part of normal record appends

This feature must make that contract explicit before higher-level session/save logic depends on it.

---

## Design

### Scope

This feature writes a **generic record directory** to `opax/v1`. It does not know what a session or save means. Higher layers provide the record type, record ID, and file set.

### Deterministic Path Derivation

The record root path is derived entirely from collection name and record ID:

```text
sessions/{sha256(id)[:2]}/{id}/
saves/{sha256(id)[:2]}/{id}/
ext-{plugin}/{sha256(id)[:2]}/{id}/
```

The caller cannot override the root path.

### Public API

```go
type RecordFile struct {
    Path    string
    Content []byte
}

type WriteRequest struct {
    Collection  string
    RecordID    string
    Files       []RecordFile
    ExpectedTip *plumbing.Hash
}

type WriteResult struct {
    BranchTip   plumbing.Hash
    CommitHash  plumbing.Hash
    RecordRoot  string
}

func WriteRecord(ctx *RepoContext, req WriteRequest) (*WriteResult, error)
```

### Ref Publication Contract

`WriteRecord` uses optimistic per-ref CAS against `refs/heads/opax/v1`:

- every attempt re-reads the current branch tip
- every attempt rebuilds tree/commit objects against that tip
- publish uses strict expected-tip CAS:
  - existing ref tip: `Storer.CheckAndSetReference(newRef, oldRef)`
  - missing ref tip: create-if-absent (no blind `CheckAndSetReference(newRef, nil)`)
- on `storage.ErrReferenceHasChanged`, retry with bounded backoff

Bounded retry policy in Phase 0:

- `maxRefPublishAttempts = 8`
- exponential backoff starts at `10ms`
- backoff cap is `100ms`
- no user-facing knobs in Phase 0

### Expected Tip Semantics

`ExpectedTip` is a strict concurrency fence:

- if `ExpectedTip` is set and the live tip differs, return `ErrTipChanged`
- if `ExpectedTip` is set, do not auto-retry past a tip change
- if `ExpectedTip` is nil, auto-retry CAS conflicts until success or retry exhaustion

### Commit Identity

Each internal branch write uses:

- Author: `Opax <opax@local>`
- Committer: `Opax <opax@local>`

Commit message format:

`opax: write <collection> <record-id>`

---

## Specification

### Valid Collections

`Collection` must be one of:

- `sessions`
- `saves`
- `ext-{name}` where `{name}` matches lowercase `[a-z0-9-]+`

Anything else is rejected.

### Valid Record Files

Each `RecordFile.Path` is relative to the record root and must satisfy:

- not empty
- not absolute
- no `..` segments
- clean normalized slash-separated path
- no duplicate file paths after cleaning

This lets plugins add nested files later without letting callers escape the record directory.

### Write Algorithm

1. Validate `Collection`, `RecordID`, and all file paths
2. Validate `opax/v1`
3. For `attempt := 1..maxRefPublishAttempts`:
4. Read current branch tip
5. If `ExpectedTip` is set and differs, return `ErrTipChanged`
6. Derive record root from `Collection` and `RecordID`
7. Check whether that record root exists in the current tip tree
8. If it exists, return `ErrRecordExists`
9. Write blobs for supplied files
10. Build minimal tree delta preserving unrelated tree content
11. Create a new commit with parent = current tip
12. Publish `refs/heads/opax/v1` with strict CAS (existing-tip compare-and-set or missing-tip create-if-absent)
13. If publish succeeds, return new tip and commit hash
14. If publish returns `ErrReferenceHasChanged`:
15. If `ExpectedTip` is set, return `ErrTipChanged`
16. Otherwise retry with bounded exponential backoff
17. On retry exhaustion, return a conflict error (`ErrTipChanged` wrapped with retry context)

### Compare-And-Swap Requirement

Ref updates must always use the previously read tip as the expected old value. Blind replacement is forbidden.

### Failure Behavior

If blob/tree/commit objects are written but final ref publication fails, the operation returns an error and may leave unreachable objects. This is acceptable.

### Duplicate Record IDs

Record IDs are immutable keys. If a record root already exists at the current tip, the write fails. This feature does not support in-place record mutation.

---

## Failure Matrix

| Condition | Expected behavior |
|---|---|
| Invalid collection | Validation error before git object writes |
| Invalid file path | Validation error before git object writes |
| Invalid `opax/v1` branch | Validation error, no write attempted |
| `ExpectedTip` mismatch before publish | `ErrTipChanged`, no hidden retry |
| Existing record root | `ErrRecordExists`, no branch update |
| Concurrent first write on missing ref | One writer wins first-tip creation; loser retries with new tip instead of clobbering |
| CAS publish conflict, `ExpectedTip` set | `ErrTipChanged`, no hidden retry |
| CAS publish conflict, `ExpectedTip` nil | Re-read/rebuild/retry up to bounded limit |
| Retry budget exhausted | Conflict error (`ErrTipChanged` wrapped), branch unchanged |

---

## Edge Cases

- **Linked worktree caller** - write uses `CommonGitDir`, never worktree-private gitdir
- **Empty file set** - reject; git cannot represent empty directories
- **File path collision after cleaning** - reject before writing objects
- **Existing branch tip missing sentinel** - treat as invalid branch
- **Plugin collection typo** - reject if not `ext-{name}` with valid name
- **Duplicate race on same record ID** - one writer wins, retried loser deterministically returns duplicate conflict

---

## Acceptance Criteria

- `WriteRecord` writes new record directories at deterministic sharded paths
- `WriteRecord` rejects invalid collections and invalid relative file paths
- `WriteRecord` rejects duplicate record IDs without mutating `opax/v1`
- `WriteRecord` creates commits on `refs/heads/opax/v1` without touching the working tree
- Ref publication uses per-ref CAS with bounded retry (`8` attempts, `10-100ms` backoff)
- `ExpectedTip` mismatches return `ErrTipChanged` without hidden success via retry
- Distinct concurrent record writes eventually preserve both records
- Internal branch commits use fixed identity `Opax <opax@local>`

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestWriteRecordSessionPath` | Session path derivation | Files land under `sessions/{shard}/{id}/` |
| `TestWriteRecordSavePath` | Save path derivation | Files land under `saves/{shard}/{id}/` |
| `TestWriteRecordExtensionPath` | Extension path derivation | Files land under `ext-{name}/{shard}/{id}/` |
| `TestWriteRecordRejectsAbsolutePath` | Path traversal safety | Validation error |
| `TestWriteRecordRejectsParentTraversal` | Path traversal safety | Validation error |
| `TestWriteRecordConcurrentDistinctIDs` | Optimistic concurrency | Two concurrent writers for distinct IDs both succeed eventually |
| `TestPublishRefWithRetryRetriesWhenRefCreatedConcurrently` | Missing-ref first-write CAS | Concurrent first writers do not silently overwrite; loser retries |
| `TestWriteRecordExpectedTipMismatch` | Explicit tip fence | `ErrTipChanged` without hidden retry success |
| `TestWriteRecordDuplicateRace` | Immutable ID race | One winner and one deterministic duplicate conflict |
| `TestWriteRecordConcurrentLinkedWorktrees` | Shared common git dir | Concurrent writers from separate worktrees preserve both writes |
| `TestWriteRecordPreservesSentinel` | Branch identity preservation | `meta/version.json` remains present after write |
| `TestWriteRecordNoWorkingTreeChanges` | Plumbing-only guarantee | Worktree remains untouched |
