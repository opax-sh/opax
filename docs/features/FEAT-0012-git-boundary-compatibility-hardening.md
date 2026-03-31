# FEAT-0012 - Native Git Backend Adapter Migration

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** In Progress
**Last synced:** 2026-03-31
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management), FEAT-0007 (Write records), FEAT-0008 (Read records), FEAT-0009 (Git notes), FEAT-0010 (Trailer support)
**Dependents:** E4 integrated write path, E5 rebuild/read consumers

---

## Problem

`go-git` repository open behavior is not a reliable production source of truth for extension-enabled linked worktrees (`extensions.worktreeConfig=true`).

The previous compatibility framing focused on path-specific opens and fallback elimination, but the root problem is backend ownership: `internal/git` needs one production transport with Git-native repository semantics while preserving Opax-owned typed contracts.

---

## Design

### Boundary reset at discovery

- `DiscoverRepo` derives `RepoContext` from native Git `rev-parse` facts.
- Discovery resolves and validates:
  - worktree root
  - git dir
  - common git dir
  - bare/non-bare state
  - linked-worktree status
- Worktree-scoped execution is the default context for commands that must honor worktree-local config.

### Single typed native backend in `internal/git`

- One unexported native backend owns:
  - command execution
  - stdout/stderr capture
  - exit-code handling
  - Git-version checks
  - stdout parsing for refs, commits, trees, blobs, and trailers
- Feature code calls typed helpers, not raw command strings.
- Primary plumbing commands:
  - `rev-parse`
  - `for-each-ref`
  - `cat-file`
  - `ls-tree`
  - `hash-object`
  - `mktree`
  - `commit-tree`
  - `update-ref`
  - `interpret-trailers`

### Contract preservation above backend

- Exported `internal/git` APIs remain unchanged.
- Typed error contracts stay stable:
  - `ErrTipChanged`
  - `ErrRecordExists`
  - `ErrRecordNotFound`
  - `ErrFileNotFound`
  - `ErrMalformedTree`
  - note not-found/conflict/malformed surfaces
- Opax validation remains in Go:
  - collection and record ID validation
  - path traversal rejection
  - deterministic shard/path derivation
  - notes namespace and payload validation
  - `Opax-Save` value validation
  - `opax/v1` sentinel validation and malformed-tree detection

### Batch-read expectation

- Read paths avoid per-object process churn in hot loops.
- Recursive tree traversals and blob batch reads are handled through typed backend helpers.
- "One subprocess per blob/tree" is treated as a backend design bug, not an acceptable steady state.

### Migration order

1. discovery and repo context
2. ref resolution + `opax/v1` bootstrap/validation
3. object read path for commits/trees/blobs
4. record reads and tree walks
5. record writes and notes publication
6. trailer parsing from committed commits
7. remove remaining production `go-git` transport usage from `internal/git`

---

## Runtime Contract

- Single binary distribution remains unchanged.
- Standard Git environment is required at runtime.
- Minimum supported Git version is explicit and enforced: `2.30.0`.

---

## Acceptance Criteria

- `DiscoverRepo` is native-Git derived and linked-worktree safe.
- `EnsureOpaxBranch`, `GetOpaxBranchTip`, `ValidateOpaxBranch` run through the native backend.
- `WriteRecord`, `ReadRecord`, `ReadFileAtPath`, `WalkRecords` run through the native backend.
- `WriteNote`, `ReadNote`, `ListNotes`, `ListNoteNamespaces` run through the native backend.
- `ParseSaveTrailerFromCommit` reads committed messages via the native backend and preserves Opax validation policy.
- Production `internal/git` transport does not depend on `go-git` repository open/read/write flows.

---

## Test Plan

- Backend parity coverage for:
  - `DiscoverRepo`
  - `EnsureOpaxBranch`, `GetOpaxBranchTip`, `ValidateOpaxBranch`
  - `WriteRecord`, `ReadRecord`, `ReadFileAtPath`, `WalkRecords`
  - `WriteNote`, `ReadNote`, `ListNotes`, `ListNoteNamespaces`
  - trailer parsing/mutation behavior
  - CAS conflict/retry behavior
  - malformed ref/tree/blob/note cases
- Linked-worktree tests remain first-class, with native backend behavior as the subject under test.
- CI defines a supported Git-version matrix with a pinned minimum version.

Verification commands:

- `go test ./internal/git/...`
- `make test`

---

## Notes

- Detailed rollout sequencing lives in [`docs/tasks/FEAT-0012-native-git-backend-adapter-migration.md`](../tasks/FEAT-0012-native-git-backend-adapter-migration.md).
- `go-git` may remain in tests as temporary fixture/scaffolding support, but not as the production semantics oracle.
- This feature is a backend migration wave, not a per-feature shell-out fallback strategy.
