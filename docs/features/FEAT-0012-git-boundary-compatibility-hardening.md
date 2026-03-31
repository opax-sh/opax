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

### Implementation Plan

Defaults:

- FEAT-0010 lands first as a clean trailer-only feature branch.
- FEAT-0012 work continues on `FEAT-0012-native-git-backend-adapter`.
- Rebase FEAT-0012 onto `main` only after FEAT-0010 lands.
- No production dual-backend mode.
- `go-git` may remain in tests temporarily, but not as the production semantics oracle.

Execution order:

1. discovery and repo context
2. ref resolution + `opax/v1` bootstrap/validation
3. object read path for commits/trees/blobs
4. record reads and tree walks
5. record writes and notes publication
6. trailer parsing from committed commits
7. remove remaining production `go-git` transport usage from `internal/git`

Phase gates:

- Discovery and runtime gate:
  - keep `DiscoverRepo` native-Git derived
  - derive `RepoContext` from `rev-parse` facts
  - enforce minimum supported Git version in one shared backend gate
  - preserve linked-worktree, common-git-dir, and bare-repo behavior contracts
- Ref and branch primitives:
  - route ref reads and CAS updates through the typed backend
  - migrate `EnsureOpaxBranch`, `GetOpaxBranchTip`, and `ValidateOpaxBranch`
  - preserve sentinel validation, symbolic-ref rejection, and typed conflict behavior
- Object read path and batch semantics:
  - migrate commit/tree/blob reads to backend helpers
  - keep batch-friendly tree walk/blob read behavior for hot paths
  - treat subprocess-per-object loops as regressions
- Record reads and walks:
  - migrate `ReadRecord`, `ReadFileAtPath`, and `WalkRecords`
  - preserve deterministic path derivation, typed not-found behavior, and malformed-tree errors
- Record writes and notes:
  - migrate write-tree/write-commit/write-ref flows behind the backend
  - migrate notes read/write/list operations
  - preserve CAS retry behavior, note conflict semantics, and namespace validation
- Trailer commit reads and final cleanup:
  - keep FEAT-0010 hook-time trailer mutation scoped to trailer semantics
  - migrate committed trailer reads needed by FEAT-0012 onto the backend
  - remove remaining production `go-git` transport usage from `internal/git`
  - keep exported signatures unchanged

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

- `go-git` may remain in tests as temporary fixture/scaffolding support, but not as the production semantics oracle.
- This feature is a backend migration wave, not a per-feature shell-out fallback strategy.

## Exit Criteria

- production `internal/git` paths no longer depend on `go-git` repository open/read/write transport
- FEAT-0010 trailer scope remains independent and already landed
- FEAT-0012 docs, epic docs, ADRs, and `docs/index.md` stay synchronized with the current migration state
