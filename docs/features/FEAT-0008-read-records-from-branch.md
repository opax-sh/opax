# FEAT-0008 - Read Records From Branch

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** In Progress
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management)
**Dependents:** E5 rebuild/sync, E9 doctor/init diagnostics, debug tooling

---

## Problem

The product story later leans on SQLite for reads, but the database cannot be built or repaired without a direct branch read primitive. Rebuild, incremental sync, and diagnostics need a way to fetch records from `opax/v1` deterministically.

If this feature is underspecified, the materializer will end up reimplementing tree traversal ad hoc, which defeats the point of having a git plumbing layer.

---

## Design

### Scope

This feature provides internal read plumbing for rebuild/sync/debug. It remains
an internal primitive, not the user-facing query surface, and includes:

- record point reads by `(collection, recordID)`
- exact file reads under `opax/v1`
- generic record enumeration for materializer rebuild/sync without ad hoc tree
  traversal in downstream layers

### Public API

```go
type ReadResult struct {
    BranchTip  plumbing.Hash
    RecordRoot string
    Files      map[string][]byte
}

type RecordLocator struct {
    BranchTip  plumbing.Hash
    Collection string
    RecordID   string
    RecordRoot string
}

func ReadRecord(ctx *RepoContext, collection, recordID string) (*ReadResult, error)
func ReadFileAtPath(ctx *RepoContext, path string) ([]byte, error)
func WalkRecords(ctx *RepoContext, visit func(locator RecordLocator) error) error

var ErrRecordNotFound = errors.New("git: record not found")
var ErrFileNotFound = errors.New("git: file not found")
var ErrMalformedTree = errors.New("git: malformed opax tree state")
```

### Shared Path Logic

`ReadRecord` and `WalkRecords` must reuse the same collection/ID validation and
deterministic path derivation as `WriteRecord`.

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

Behavior rules:

- read is performed against a snapshot of the resolved tip
- missing collection or shard directory is a normal not-found, not corruption
- missing record leaf directory is a normal not-found, not corruption
- any shape mismatch in the derived path chain is `ErrMalformedTree`
  (for example expected tree but found blob)

### `ReadFileAtPath`

`ReadFileAtPath` is a lower-level helper used when callers already know the exact path they need.

Rules:

- reads from the current validated `refs/heads/opax/v1` tip
- path must be normalized and stay inside the branch tree namespace
- directories return an error; this helper reads blobs only
- missing path returns `ErrFileNotFound`

### `WalkRecords`

`WalkRecords` enumerates all record roots under the opax branch layout and calls
`visit` once per record.

Rules:

- validates `opax/v1` and resolves one branch tip snapshot
- skips `meta/`
- includes first-party and extension collections (`ext-*`)
- emits one `RecordLocator` per `{collection}/{shard}/{recordID}` record root
- malformed collection/shard/record layout returns `ErrMalformedTree`
- callback errors are returned unchanged

### Error Behavior

The feature must expose typed error conditions that callers can match with
`errors.Is`:

- branch not initialized
- record not found
- file not found
- malformed tree state (expected blob, found tree or missing subtree)

These are different conditions for rebuild and doctor commands.

---

## Edge Cases

- **Collection/shard exists but record leaf is absent** - normal record miss
- **Derived path chain contains blob where tree is required** - malformed branch data
- **Collection typo** - validation error, not a branch read
- **Tip changed during read** - acceptable; reads are against a snapshot of the resolved tip
- **Large file blobs** - return the blob bytes directly; size-tier logic belongs to CAS and higher-level readers

---

## Acceptance Criteria

- `ReadRecord` reads files from the deterministic record root for sessions, saves, and extension collections
- `ReadRecord` uses the same shard derivation as `WriteRecord`
- `ReadRecord` returns a clear not-found error when the record does not exist
- `ReadFileAtPath` reads a single blob by exact path from `opax/v1`
- `WalkRecords` enumerates all records under first-party and extension collections
- The feature distinguishes not-found from malformed-tree conditions
- `ReadRecord`, `ReadFileAtPath`, and `WalkRecords` expose errors matchable via `errors.Is`
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
| `TestReadFileAtPathNotFound` | Missing file behavior | `ErrFileNotFound` |
| `TestReadRecordMalformedTree` | Corrupt branch state | Malformed-data error |
| `TestReadRecordSharedShardMiss` | Shard-level partial path | Missing record returns not-found, not malformed |
| `TestWalkRecordsSessionsSavesExtensions` | Generic enumeration | Visits all record roots across first-party and extension collections |
| `TestWalkRecordsSkipsMeta` | Layout filtering | Metadata branch paths are not emitted as records |
| `TestReadErrorsAreTyped` | Caller error matching | `errors.Is` works for not-found and malformed conditions |
