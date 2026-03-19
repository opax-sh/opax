# FEAT-0007 - Write Records To Branch

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management), FEAT-0004 (File lock utility)
**Dependents:** E4 write orchestrator, E5 rebuild inputs, E8 memory plugin, E9 init/storage stats

---

## Problem

This is the hardest write path in Phase 0. Opax needs to append structured records to a single orphan branch without checking it out, without clobbering concurrent writes, and without trusting user-supplied paths. The roadmap describes the desired plumbing flow (`hash-object`, `mktree`, `commit-tree`, `update-ref`), but the dangerous parts are in the gaps:

- stale branch tips can silently overwrite history if ref updates are not conditional
- user-controlled paths can escape intended directories
- duplicate record IDs can create conflicting logical state
- partial failures can leave unreachable objects behind

This feature must make the branch write contract explicit before higher-level session/save logic depends on it.

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

### Locking

The full write runs under the shared Phase 0 lock:

`filepath.Join(ctx.CommonGitDir, "opax.lock")`

The feature must acquire the lock before reading the current branch tip.

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
2. Acquire `.git/opax.lock`
3. Validate `opax/v1`
4. Read current branch tip
5. If `ExpectedTip` is set and differs from the current tip, return `ErrTipChanged`
6. Derive the record root path from `Collection` and `RecordID`
7. Check whether that record root already exists in the tip tree
8. If it exists, return `ErrRecordExists`
9. Write blobs for all supplied files
10. Build the new trees needed to add the record directory while preserving the rest of the branch tree
11. Create a new commit whose parent is the current branch tip
12. Update `refs/heads/opax/v1` with a compare-and-swap old-tip check
13. Return the new branch tip and commit hash

### Compare-And-Swap Requirement

The ref update must use the previously read tip as the expected old value. If that check fails, return `ErrTipChanged`.

This is non-negotiable. Silent overwrite would violate the append-only branch model.

### Failure Behavior

If blob or tree objects are written but the final ref update fails, the operation returns an error and leaves unreachable objects behind. That is acceptable. Unreachable objects are cheaper than history corruption.

### Duplicate Record IDs

Record IDs are immutable keys. If a record root already exists, the write fails. This feature does not support in-place record mutation.

### Fallback Policy

Primary implementation uses go-git plumbing. A narrow internal fallback to shell plumbing is allowed **only** if:

- it remains behind the same `WriteRecord` API
- it preserves identical lock/ref-update semantics
- it is covered by the same tests

The fallback is a mitigation, not a second design.

---

## Failure Matrix

| Condition | Expected behavior |
|---|---|
| Invalid collection | Validation error before lock or object writes |
| Invalid file path | Validation error before lock or object writes |
| Lock timeout or stale lock | Bubble lock package error |
| Invalid `opax/v1` branch | Validation error, no write attempted |
| `ExpectedTip` mismatch | `ErrTipChanged`, no branch update |
| Existing record root | `ErrRecordExists`, no branch update |
| Ref update old-tip mismatch | `ErrTipChanged`, branch unchanged, unreachable objects allowed |

---

## Edge Cases

- **Linked worktree caller** - write uses `CommonGitDir`, never the worktree-private gitdir
- **Empty file set** - reject; a record directory without files is meaningless and git cannot represent empty dirs
- **File path collision after cleaning** - reject before writing any objects
- **Existing branch tip missing sentinel** - treat as invalid branch rather than trying to repair inline
- **Plugin collection typo** - reject if the collection is not `ext-{name}` with a valid name
- **Record root exists but is malformed** - still reject as existing logical ID; later repair or rebuild is a different concern

---

## Acceptance Criteria

- `WriteRecord` writes a new record directory at the deterministic sharded path for sessions, saves, and extension collections
- `WriteRecord` acquires `.git/opax.lock` before reading the branch tip
- `WriteRecord` rejects invalid collections and invalid relative file paths
- `WriteRecord` rejects duplicate record IDs without mutating the branch
- `WriteRecord` creates a new commit on `refs/heads/opax/v1` without touching the working tree
- Ref updates use compare-and-swap old-tip checks and return `ErrTipChanged` on mismatch
- Internal branch commits use the fixed `Opax <opax@local>` identity
- Partial failures may leave unreachable objects but never corrupt `opax/v1`

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestWriteRecordSessionPath` | Session path derivation | Files land under `sessions/{shard}/{id}/` |
| `TestWriteRecordSavePath` | Save path derivation | Files land under `saves/{shard}/{id}/` |
| `TestWriteRecordExtensionPath` | Extension path derivation | Files land under `ext-{name}/{shard}/{id}/` |
| `TestWriteRecordRejectsAbsolutePath` | Path traversal safety | Validation error |
| `TestWriteRecordRejectsParentTraversal` | Path traversal safety | Validation error |
| `TestWriteRecordRejectsDuplicateRecord` | Immutable ID enforcement | `ErrRecordExists` |
| `TestWriteRecordTipMismatch` | CAS ref update | `ErrTipChanged` when expected tip changed |
| `TestWriteRecordUsesLock` | Write serialization | Competing writer waits or times out on lock |
| `TestWriteRecordPreservesSentinel` | Branch identity preservation | `meta/version.json` remains present after write |
| `TestWriteRecordNoWorkingTreeChanges` | Plumbing-only guarantee | Worktree remains untouched |
