# FEAT-0008 - Read Records From Branch

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management)
**Dependents:** E5 rebuild/sync, E9 doctor/init diagnostics, debug tooling

---

## Problem

The product story later leans on SQLite for reads, but the database cannot be built or repaired without a direct branch read primitive. Rebuild, incremental sync, and diagnostics need a way to fetch records from `opax/v1` deterministically.

If this feature is underspecified, the materializer will end up reimplementing tree traversal ad hoc, which defeats the point of having a git plumbing layer.

---

## Design

### Scope

This feature provides **point reads**, not branch enumeration as the public query surface. It is intentionally low-level and exists for rebuild/sync/debug use cases.

### Public API

```go
type ReadResult struct {
    BranchTip  plumbing.Hash
    RecordRoot string
    Files      map[string][]byte
}

func ReadRecord(ctx *RepoContext, collection, recordID string) (*ReadResult, error)
func ReadFileAtPath(ctx *RepoContext, branchRef, path string) ([]byte, error)
```

### Shared Path Logic

`ReadRecord` must use the same deterministic path derivation as `WriteRecord`.

---

## Specification

### `ReadRecord`

`ReadRecord`:

1. validates the collection and record ID
2. validates `opax/v1`
3. resolves the current branch tip
4. derives the record root path
5. reads every file under that record root
6. returns a `map[path]content` keyed relative to the record root

### `ReadFileAtPath`

`ReadFileAtPath` is a lower-level helper used when callers already know the exact path they need.

Rules:

- `branchRef` defaults to `refs/heads/opax/v1` when callers pass the standard branch name
- path must be normalized and stay inside the branch tree namespace
- directories return an error; this helper reads blobs only

### Error Behavior

The feature should distinguish:

- branch not initialized
- record not found
- file not found
- malformed tree state (expected blob, found tree or missing subtree)

These are different conditions for rebuild and doctor commands.

---

## Edge Cases

- **Record ID path exists partially** - treat as malformed branch data rather than a normal miss
- **Collection typo** - validation error, not a branch read
- **Tip changed during read** - acceptable; reads are against a snapshot of the resolved tip
- **Large file blobs** - return the blob bytes directly; size-tier logic belongs to CAS and higher-level readers

---

## Acceptance Criteria

- `ReadRecord` reads files from the deterministic record root for sessions, saves, and extension collections
- `ReadRecord` uses the same shard derivation as `WriteRecord`
- `ReadRecord` returns a clear not-found error when the record does not exist
- `ReadFileAtPath` reads a single blob by exact path from `opax/v1`
- The feature distinguishes not-found from malformed-tree conditions
- The feature does not claim to be the public search/query surface for Phase 0

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestReadRecordSession` | Session record point read | Returns expected files and bytes |
| `TestReadRecordSave` | Save record point read | Returns expected files and bytes |
| `TestReadRecordExtension` | Extension record point read | Returns expected files and bytes |
| `TestReadRecordNotFound` | Missing record behavior | Not-found error |
| `TestReadFileAtPath` | Exact blob read | Returns exact file bytes |
| `TestReadFileAtPathDirectory` | Blob-only enforcement | Error when path resolves to tree |
| `TestReadRecordMalformedTree` | Corrupt branch state | Malformed-data error |
