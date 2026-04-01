# FEAT-0012 - Native Git Backend Adapter Migration

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-04-01
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Orphan branch management), FEAT-0007 (Write records), FEAT-0008 (Read records), FEAT-0009 (Git notes), FEAT-0010 (Trailer support)
**Dependents:** E4 integrated write path, E5 rebuild/read consumers

---

## Problem

`go-git` open/read behavior is not a reliable production source of truth for extension-enabled linked worktrees (`extensions.worktreeConfig=true`).

FEAT-0012 resolves this as a backend-ownership problem, not a per-feature fallback problem: `internal/git` moves to one native-Git production transport while preserving Opax-owned typed contracts.

---

## Scope

### In Scope

- Migrate production `internal/git` transport to one unexported typed native backend.
- Keep exported `internal/git` API signatures and typed error surfaces stable in this feature.
- Preserve linked-worktree correctness, branch identity/sentinel validation, note namespace policy, and trailer validation policy.
- Add CI-enforced checkpoint delivery gates for the migration slices.

### Out of Scope

- Exported type/API decoupling from `go-git` (deferred to FEAT-0013).
- Product-facing runtime knobs for backend binary selection.
- Dual production backends or fallback mode.

---

## Design Contracts

### Discovery Contract

- `DiscoverRepo` derives `RepoContext` from native Git `rev-parse` facts.
- Discovery validates worktree root, git dir, common git dir, bare/non-bare state, and linked-worktree status.
- Worktree-scoped execution is default for commands that must honor worktree-local config.
- Discovery inconsistencies fail closed.

### Backend Boundary Contract

- One unexported native backend in `internal/git` owns command execution, stdout/stderr capture, parsing, and exit handling.
- Feature code calls typed helpers, never raw command strings.
- Primary plumbing commands: `rev-parse`, `for-each-ref`, `cat-file`, `ls-tree`, `hash-object`, `mktree`, `commit-tree`, `update-ref`, `interpret-trailers`.

### Runtime Policy

- Enforce a shared fail-fast Git version gate (`>=2.30.0`) at backend init.
- Cache both successful and failed gate outcomes.
- Allow gate-cache reset only for test/CI harnesses via guarded internal test/CI wiring.
- Force Git subprocess locale to `LC_ALL=C` and `LANG=C`.
- Support private test/CI git binary override `OPAX_GIT_BIN`; do not accept it in normal runtime paths.
- Include sanitized stderr context in wrapped errors:
  - scrub absolute filesystem paths
  - keep refs/object IDs visible
  - enforce one global cap (target default: 512 bytes, implementation detail)

### API and Validation Contract

- Exported `internal/git` APIs remain unchanged for FEAT-0012.
- Exported `go-git` surface types are frozen for FEAT-0012 compatibility.
- Opax validation remains in Go:
  - collection and record ID validation
  - path traversal rejection
  - deterministic shard/path derivation
  - notes namespace and payload validation
  - `Opax-Save` value validation
  - `opax/v1` sentinel validation and malformed-tree detection
- Typed errors remain stable, including:
  - `ErrTipChanged`
  - `ErrRecordExists`
  - `ErrRecordNotFound`
  - `ErrFileNotFound`
  - `ErrMalformedTree`
  - note not-found/conflict/malformed surfaces

### Error Translation Contract

- Error mapping is driven by typed post-conditions in Go, not stderr text.
- Structured CAS probing is primary for `update-ref` conflict detection.
- Stderr matching is minimal fallback for ambiguous failures.
- Ambiguous CAS outcomes map to an internal unknown-outcome error, not `ErrTipChanged`.
- Unknown-outcome CAS errors stay internal in FEAT-0012.
- Backend classification stays fail-closed on malformed or ambiguous Git output.
- Ambiguous internal CAS outcomes remain debug-level diagnostics, not user-facing CLI output.

### Read-Path Performance Contract

- Recursive tree traversals and blob reads use batch-friendly backend helpers.
- Subprocess-per-object loops are regressions.
- Hot read paths are protected by hard call-count ceilings.

---

## Ordered Implementation Plan (A -> F)

### Preconditions

- FEAT-0010 is already landed independently.
- No production dual-backend mode.
- `go-git` may remain only on the frozen FEAT-0012 compatibility surface (`go-git/plumbing`) and in narrowly scoped tests; it is no longer the production transport.

### Checkpoints

| Checkpoint | Scope | Required migration work | Required proof gate |
| ---------- | ----- | ----------------------- | ------------------- |
| A | Discovery and backend gate | Native discovery from `rev-parse`; shared runtime gate policy; locale and binary-resolution policy wiring | Linked-worktree discovery parity; gate-policy tests |
| B | Ref primitives and branch lifecycle | Migrate `EnsureOpaxBranch`, `GetOpaxBranchTip`, `ValidateOpaxBranch`; structured CAS conflict probing | Branch bootstrap/validation/CAS conflict parity |
| C | Object reads and batch behavior | Migrate commit/tree/blob reads to backend helpers | Malformed object parity; call-count ceilings for hot reads |
| D | Record reads and walks | Migrate `ReadRecord`, `ReadFileAtPath`, `WalkRecords` | Record read/walk parity; call-count ceilings |
| E | Record writes and notes | Migrate write flows and note read/write/list operations | Write/notes parity; CAS retry and namespace validation parity |
| F | Trailers and cleanup | Migrate committed trailer reads (`ParseSaveTrailerFromCommit`); preserve malformed `Opax-Save` hard errors; remove remaining production `go-git` transport usage | Trailer policy parity; production transport cleanup complete |

### Delivery and Merge Policy

- Use one stacked branch per slice.
- Every slice PR must update FEAT checkpoint state and `docs/index.md` in the same PR.
- Every slice PR must include canonical-fixture compatibility check evidence.
- Call-count ceiling checks are required proof gates for C, D, and any later slice touching read paths.
- No special rollback playbook; standard PR/revert discipline is intentional.
- PR titles must include checkpoint label (`A` through `F`).
- Each slice PR must include a FEAT decision-delta note for checkpoint-level policy changes.

### Checkpoint Tracking Rules

Status enum: `Planned`, `In Progress`, `Blocked`, `Done`.

- Set `Done` when required migration work, required proof gates, and FEAT/index tracker updates are complete.
- `Blocked` status must include a one-line blocker reason in the `Status` cell.
- `PR` values must be full PR URLs once a PR exists.

### Checkpoint Status Tracker

| Checkpoint | Scope | Status | PR |
| ---------- | ----- | ------ | -- |
| A | Discovery and backend gate | Done | TBD |
| B | Ref primitives and branch lifecycle | Done | TBD |
| C | Object reads and batch behavior | Done | TBD |
| D | Record reads and walks | Done | TBD |
| E | Record writes and notes | Done | TBD |
| F | Trailers and cleanup | Done | TBD |

### Checkpoint A Decision Delta (2026-04-01)

- Introduce one shared command-runtime policy for discovery and backend helper execution.
- Restrict `OPAX_GIT_BIN` to test execution and fail closed in normal runtime paths.
- Cache both pass/fail Git version gate results by resolved binary path.
- Force subprocess locale (`LC_ALL=C`, `LANG=C`) and sanitize/cap stderr context globally.

### Checkpoint B Decision Delta (2026-04-01)

- Move `update-ref` CAS conflict classification to structured post-condition probing in the shared native backend helper.
- Classify CAS outcomes by ref post-condition:
  - live ref equals requested new hash -> applied
  - live ref disproves expected-old CAS precondition -> conflict
  - otherwise -> unknown internal outcome
- Keep stderr conflict text matching as fallback only when post-condition probing is unavailable or inconclusive.
- Keep unknown CAS outcomes internal and do not map them to exported conflict surfaces (`ErrTipChanged`, `ErrNoteConflict`).

### Checkpoint C Decision Delta (2026-04-01)

- Keep checkpoint C scoped to object-read helper hardening and proof gates; no read traversal redesign in this slice.
- Map `ReadRecord` batch blob-read failures to `ErrMalformedTree` to preserve typed malformed-object behavior.
- Enforce checkpoint C hot-read call-count gates:
  - `ReadRecord` measured operation: total git calls `<= 15`, exactly one `cat-file --batch`, and zero hash-form `cat-file blob <40-hex>` loops.
  - `ReadFileAtPath` measured operation: total git calls `<= 13`.
- Warm the shared Git version gate before call-count measurement and clear instrumentation logs so thresholds apply only to measured reads.

### Checkpoint D Decision Delta (2026-04-01)

- Replace `WalkRecords` per-shard subtree reads with one recursive tree read per collection (`ls-tree -r -t`) while keeping typed `ErrMalformedTree` fail-closed behavior.
- Preserve shard and record-root structural validation semantics during recursive traversal:
  - depth-1 shard entries must be trees and valid shard names
  - depth-2 record roots must be trees, pass `validateRecordID`, and match deterministic `deriveRecordRoot`
- Emit each record locator exactly once, in deterministic traversal order, and fail closed on duplicate record roots.
- Enforce checkpoint D hot-read call-count gate:
  - `WalkRecords` measured operation: total git calls `<= 12` for the canonical multi-collection fixture and exactly one recursive `ls-tree` call per discovered collection.

### Checkpoint E Decision Delta (2026-04-01)

- Keep checkpoint E scoped to explicit native-backend write/notes proof gates; no exported `internal/git` API or runtime-policy changes land in this slice.
- Add deterministic CAS-retry checkpoint coverage by forcing one real competing `update-ref` state change on:
  - `refs/heads/opax/v1` record publication
  - `refs/notes/opax/{namespace}` note publication
- Keep note validation and malformed-state policy unchanged while making the checkpoint gate explicit:
  - invalid namespaces still fail before Git execution
  - nested note refs still map to `ErrMalformedNote`
  - malformed note payloads and note refs that do not point to commits still map to `ErrMalformedNote`
- Keep linked-worktree checkpoint coverage explicit for write paths:
  - `WriteRecord` preserves working-tree/index state while publishing through the shared native backend
  - `WriteNote`, `ReadNote`, `ListNotes`, and `ListNoteNamespaces` remain linked-worktree safe on the canonical `extensions.worktreeConfig=true` fixture

### Checkpoint F Decision Delta (2026-04-01)

- Keep `ParseSaveTrailerFromCommit` on the existing native backend path via `openRepoFromContext()` plus shared commit-read helpers; no new production transport refactor lands in this slice.
- Close trailer proof gates with committed-message validation parity:
  - malformed committed `Opax-Save` values still fail hard
  - duplicate or mixed-case committed `Opax-Save` trailers still fail hard with the same Go-owned validation contract as `ParseSaveTrailer`
- Add a repo-enforced import guard so non-test `internal/git` production files may keep only the frozen FEAT-0012 compatibility import surface `github.com/go-git/go-git/v5/plumbing`.
- Treat any new non-test `internal/git` import of top-level `github.com/go-git/go-git/v5` or other `go-git` transport/open-read-write packages as a checkpoint-F failure; broader type/API decoupling remains deferred to FEAT-0013.

### Closeout Note (2026-04-01)

- FEAT-0012 is complete once docs match the shipped boundary and the current local proof gates stay green.
- FEAT-0012 completes the production transport migration to native Git for `internal/git`; it does not remove the frozen FEAT-0012 compatibility surface (`go-git/plumbing`) or the module dependency.
- Cross-platform and Git-version matrix automation remain useful hardening work, but they are not a blocker for FEAT-0012 closeout because this repository does not currently define that CI infrastructure.

---

## Runtime Contract

- Single binary distribution remains unchanged.
- Standard Git environment is required at runtime.
- Minimum supported Git version: `2.30.0`.

---

## Acceptance Criteria

- `DiscoverRepo` is native-Git derived and linked-worktree safe.
- `EnsureOpaxBranch`, `GetOpaxBranchTip`, `ValidateOpaxBranch` run through the native backend.
- `WriteRecord`, `ReadRecord`, `ReadFileAtPath`, `WalkRecords` run through the native backend.
- `WriteNote`, `ReadNote`, `ListNotes`, `ListNoteNamespaces` run through the native backend.
- `ParseSaveTrailerFromCommit` reads committed messages via native backend and preserves Opax validation policy.
- Production `internal/git` transport does not depend on `go-git` repository open/read/write flows.

---

## Test Plan

- Keep one canonical linked-worktree fixture (`extensions.worktreeConfig=true`) across all slices.
- Require one fixture-driven test per exported API surface touched by a slice.
- Maintain parity coverage for:
  - discovery
  - branch primitives
  - record read/write APIs
  - notes APIs
  - trailer parse/mutation behavior
  - CAS conflict/retry behavior
  - malformed ref/tree/blob/note behavior
- Enforce non-mutation invariants with one shared before/after harness (working tree and index).
- Enforce hard call-count ceilings for hot read paths.
- Run locale-hardening tests under non-`C` process locale and verify forced `LC_ALL=C`/`LANG=C` behavior.
- Keep call-count thresholds defined directly in Go tests.
- Keep compatibility fixtures deterministic (no random data in checkpoint compatibility tests).
- Keep performance contracts scoped to call-count invariants (no wall-clock gates).
- Keep `go-git` out of new tests unless explicitly tagged temporary oracle coverage.
- Keep the repo-enforced import guard that fails when production `internal/git` paths import `go-git` transport/open-read-write dependencies outside the frozen `go-git/plumbing` compatibility surface.
- Treat broader Linux/macOS and minimum-version/latest-version matrix automation as follow-up CI hardening, not a FEAT-0012 closeout gate.

Verification commands:

- `go test ./internal/git/...`
- `make test`

---

## Exit Criteria

- Production `internal/git` paths no longer depend on `go-git` repository open/read/write transport.
- FEAT-0010 trailer scope remains independent and landed.
- FEAT-0012 docs, epic docs, ADRs, and `docs/index.md` are synchronized with migration state.
- Current repo-enforced verification is green:
  - `go test ./internal/git/...`
  - `make test`
  - production `internal/git` import guard stays green under `tools/checks`
- FEAT-0012 completion is explicitly limited to production transport migration:
  - native Git owns production repository/object/ref transport in `internal/git`
  - exported `go-git/plumbing` compatibility types remain frozen in FEAT-0012
  - full `go-git` type/module removal is deferred to FEAT-0013
- All checkpoints (`A` through `F`) are `Done` with no unresolved checkpoint policy decisions.

---

## Follow-up Feature Commitment (FEAT-0013)

- Track follow-up feature [`FEAT-0013 - go-git API and type decoupling`](FEAT-0013-go-git-api-type-decoupling.md) in blocked status.
- FEAT-0013 starts only after FEAT-0012 closeout lands.
- FEAT-0013 executes in two tracked stages:
  - Stage 1: exported contract decoupling from `go-git/plumbing`, with runtime behavior and typed errors preserved.
  - Stage 2: remaining internal `go-git/plumbing` dependency cleanup while keeping Stage 1 API stable.
- FEAT-0013 tracking checklist (initial):
  - [ ] Stage 1 contract changes merged with caller compatibility notes.
  - [ ] Stage 2 internal cleanup merged without API regressions.
