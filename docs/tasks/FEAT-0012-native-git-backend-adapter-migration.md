# FEAT-0012 Task Breakdown - Native Git Backend Adapter Migration

**Parent Feature:** [FEAT-0012 - Native Git Backend Adapter Migration](../features/FEAT-0012-git-boundary-compatibility-hardening.md)
**Status:** In Progress
**Last synced:** 2026-03-31

---

## Goal

Execute FEAT-0012 as a backend replacement wave inside `internal/git`, while preserving exported APIs and typed Opax contracts.

This task doc is the implementation breakdown. The feature doc owns the end-state contract; this task doc owns the migration sequence and verification gates.

---

## Defaults

- FEAT-0010 lands first as a clean trailer-only feature branch.
- FEAT-0012 work continues on `FEAT-0012-native-git-backend-adapter`.
- Rebase FEAT-0012 onto `main` only after FEAT-0010 lands.
- No production dual-backend mode.
- `go-git` may remain in tests temporarily, but not as the production semantics oracle.

---

## Execution Order

### 1. Discovery and Runtime Gate

- keep `DiscoverRepo` native-Git derived
- derive `RepoContext` from `rev-parse` facts
- enforce minimum supported Git version in one shared backend gate
- preserve linked-worktree, common-git-dir, and bare-repo behavior contracts

Verification:

- `go test ./internal/git/...`
- linked-worktree discovery parity tests

### 2. Ref and Branch Primitives

- route ref reads and CAS updates through the typed backend
- migrate `EnsureOpaxBranch`, `GetOpaxBranchTip`, and `ValidateOpaxBranch`
- preserve sentinel validation, symbolic-ref rejection, and typed conflict behavior

Verification:

- `go test ./internal/git/...`
- malformed ref and malformed branch tests

### 3. Object Read Path and Batch Semantics

- migrate commit/tree/blob reads to backend helpers
- keep batch-friendly tree walk/blob read behavior for hot paths
- treat subprocess-per-object loops as regressions

Verification:

- `go test ./internal/git/...`
- tree/blob malformed-object tests

### 4. Record Reads and Walks

- migrate `ReadRecord`, `ReadFileAtPath`, and `WalkRecords`
- preserve deterministic path derivation, typed not-found behavior, and malformed-tree errors

Verification:

- `go test ./internal/git/...`
- read-path parity and traversal rejection tests

### 5. Record Writes and Notes

- migrate write-tree/write-commit/write-ref flows behind the backend
- migrate notes read/write/list operations
- preserve CAS retry behavior, note conflict semantics, and namespace validation

Verification:

- `go test ./internal/git/...`
- CAS conflict/retry and note malformed-state tests

### 6. Trailer Commit Reads and Final Cleanup

- keep FEAT-0010 hook-time trailer mutation scoped to trailer semantics
- migrate committed trailer reads needed by FEAT-0012 onto the backend
- remove remaining production `go-git` transport usage from `internal/git`
- keep exported signatures unchanged

Verification:

- `go test ./internal/git/...`
- `PATH="/opt/homebrew/bin:$PATH" make test`

---

## Exit Criteria

- production `internal/git` paths no longer depend on `go-git` repository open/read/write transport
- FEAT-0010 trailer scope remains independent and already landed
- FEAT-0012 docs, epic docs, ADRs, and `docs/index.md` stay synchronized with the current migration state
