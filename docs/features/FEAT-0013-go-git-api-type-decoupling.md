# FEAT-0013 - go-git API and Type Decoupling

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-04-15
**Dependencies:** FEAT-0012 (Native backend adapter migration)
**Dependents:** Future `internal/git` callers, module dependency cleanup

---

## Problem

FEAT-0012 completed the production transport migration to native Git, but it intentionally froze a compatibility surface around `github.com/go-git/go-git/v5/plumbing` so the backend cutover could land without widening the API change set.

That leaves three pieces of follow-up work:

- exported `internal/git` contracts still expose `go-git/plumbing` types
- non-test production code still carries the frozen `go-git/plumbing` compatibility surface
- tests and smoke coverage still keep the module dependency alive in `go.mod`

Until that follow-up lands, Opax is native Git in production behavior but not fully decoupled from `go-git`.

---

## Scope

### In Scope

- Replace exported `go-git/plumbing` contract exposure with Opax-owned string-based contracts while preserving runtime behavior.
- Remove `go-git/plumbing` from every non-test `internal/git` file once the new public boundary is in place.
- Rewrite test and smoke coverage away from `go-git`, then drop the `github.com/go-git/go-git/v5` module dependency.
- Keep native Git as the only production transport for `internal/git`.
- Update adjacent feature docs and `docs/index.md` so the new staged split and repo-private API break are explicit.

### Out of Scope

- Reopening FEAT-0012 production transport decisions.
- Adding dual backends or fallback runtime modes.
- Changing persisted/domain commit-hash fields in `internal/types` or serialized JSON shapes.
- Introducing compatibility adapters that accept both old `plumbing` types and new strings.
- Adding broader result-shape or error-taxonomy cleanups outside the contract changes listed below.

---

## Contracts

- FEAT-0013 starts only after FEAT-0012 is complete and docs are synced to the native-backend production boundary.
- FEAT-0013 is an allowed repo-private API break for `internal/git`; docs must describe the caller migration explicitly instead of treating it as silent compatibility churn.
- Stage 1 is a complete production-decoupling checkpoint:
  - public `internal/git` hash-bearing APIs return canonical lowercase 40-character strings
  - no exported hash type survives anywhere in the public `internal/git` API
  - `WriteRequest.ExpectedTip` becomes `*string`
  - `WriteResult` keeps `BranchTip` and `RecordRoot` and removes `CommitHash`
  - `ReadResult.BranchTip` and `RecordLocator.BranchTip` keep their names and switch only in representation
  - `EnsureOpaxBranch` remains the create-or-validate bootstrap API
  - other branch-dependent public APIs use `ErrOpaxBranchNotFound` for missing `opax/v1`
  - new typed input errors are limited to `ErrInvalidHash`, `ErrCommitNotFound`, and `ErrOpaxBranchNotFound`
  - existing sentinels `ErrRecordNotFound`, `ErrFileNotFound`, `ErrMalformedTree`, `ErrNoteNotFound`, `ErrMalformedNote`, `ErrNoteConflict`, and `ErrTipChanged` remain stable
  - no non-test `internal/git` file imports `go-git`
- Stage 2 starts only after Stage 1 lands green and docs are synced:
  - remove remaining `go-git` usage from tests and smoke coverage
  - replace `go-git`-based test fixtures with native Git helpers
  - replace the `go-git` smoke test with native Git runtime smoke coverage
  - drop the `go-git` module dependency from `go.mod` and `go.sum`
- Native Git remains the only production transport throughout both stages.
- Persisted/domain commit-hash fields such as `types.Note.CommitHash` and `types.Save.CommitHash` remain unchanged strings throughout both stages.
- Public hash inputs reject abbreviated hashes. Mixed-case and whitespace-padded inputs are accepted only through normalization to canonical lowercase 40-character strings.

---

## Acceptance Criteria

- [x] Stage 1 lands as a separately mergeable checkpoint with synced docs and green proof gates.
- [x] Stage 1 removes every exported `go-git/plumbing` type from `internal/git` and leaves no exported hash type behind.
- [x] Stage 1 updates the public `internal/git` boundary to canonical lowercase 40-character strings, removes `WriteResult.CommitHash`, and standardizes missing-branch behavior on `ErrOpaxBranchNotFound`.
- [x] Stage 1 removes all non-test `go-git` imports from `internal/git`.
- [x] Stage 1 updates tests away from `plumbing.ErrReferenceNotFound` assertions and stale `WriteResult.CommitHash` expectations.
- [x] Stage 2 lands as a separately mergeable checkpoint with synced docs and green proof gates.
- [x] Stage 2 removes remaining `go-git` usage from tests and smoke coverage, replaces it with native Git helpers/runtime smoke coverage, and deletes the module dependency from `go.mod` and `go.sum`.
- [x] `go test ./internal/git/...` and `make test` stay green throughout both stages.

---

## Closeout Note (2026-04-15)

- Stage 1 completed the caller-surface contract migration to canonical string hashes and removed the frozen non-test `go-git` production imports.
- Stage 2 replaced the remaining `go-git` test/smoke helpers with native Git CLI helpers and removed `github.com/go-git/go-git/v5` from the module graph.
- Closeout verification commands:
  - `go test ./internal/git/...`
  - `go test ./...`
  - `make test`

---

## Test Plan

- Keep the existing FEAT-0012 native-backend proof gates green while decoupling the API surface.
- Tighten the existing production import guard in Stage 1 so non-test `internal/git` files cannot import `go-git`.
- Add explicit regression coverage for:
  - branch missing
  - invalid hash
  - missing commit
  - normalization of uppercase or whitespace-padded inputs
  - `ErrTipChanged`
  - successful non-empty canonical string outputs
- Cover the updated string-based contract on:
  - `EnsureOpaxBranch`
  - `GetOpaxBranchTip`
  - `WriteRecord`
  - `ReadRecord`
  - `WalkRecords`
  - `WriteNote`
  - `ReadNote`
  - `ParseSaveTrailerFromCommit`
- Keep `ReadNote` and `ParseSaveTrailerFromCommit` on the narrow typed input-error set:
  - malformed hash -> `ErrInvalidHash`
  - valid canonical hash with no commit -> `ErrCommitNotFound`
  - other runtime/malformed-layout failures stay on existing sentinels or wrapped contextual errors
- Replace `go-git`-driven tests in Stage 2 with native Git helper-backed fixtures before removing the module dependency.
- Replace `internal/deps_smoke_test.go`'s `go-git` smoke coverage with native Git runtime smoke coverage before deleting the module dependency.

---

## Notes

- FEAT-0013 follows FEAT-0012 closeout and completes the repo-private `internal/git` API break plus full production decoupling.
- Stage 1 and Stage 2 both landed as independently mergeable checkpoints.
- `ListNotes` remains unchanged at the public contract level.
