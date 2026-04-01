# FEAT-0010 - Commit Trailer Support

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-03-30
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0002 (ID types)
**Dependents:** E4 commit linkage, E8 save creation, E11 hook lifecycle

---

## Problem

The product docs want `Opax-Save` trailers to be the default immutable link between a source commit and its save, but the timeline only works if the save ID exists before the commit is created. That timing detail is easy to miss and was inconsistent in the roadmap.

This feature resolves it by making trailer handling a dedicated preallocation and parsing layer:

- allocate a fresh `sav_` ID during `prepare-commit-msg`
- insert or replace exactly one `Opax-Save` trailer in the commit message
- parse the committed trailer later from `HEAD`

This feature does **not** create the save record itself.

---

## Design

### Scope

`internal/git/` owns save-ID allocation and committed-trailer validation policy. Hook installation, wrapper scripts, and post-commit orchestration belong to E11.

`UpsertSaveTrailer` is a hook-time helper, not a general-purpose pure-Go commit-message normalizer. Trailer mutation and trailer recognition follow native Git semantics so repo config, comment-char handling, and linked worktree behavior stay aligned with Git itself. Opax keeps committed-object reads and save-ID validation policy in Go.

### Public API

```go
func AllocateSaveID() types.SaveID
func UpsertSaveTrailer(ctx *RepoContext, message []byte) ([]byte, types.SaveID, error)
func ParseSaveTrailer(message []byte) (types.SaveID, bool, error)
func ParseSaveTrailerFromCommit(ctx *RepoContext, commitHash string) (types.SaveID, bool, error)
```

### Fixed Trailer Key

Phase 0 uses one fixed canonical trailer key:

`Opax-Save`

`trailers.prefix` remains a configuration field for future extensibility, but FEAT-0010 does not honor custom trailer naming in Phase 0. Hook logic in E11 may still use `trailers.enabled` to disable trailer insertion entirely.

---

## Specification

### Preallocation Flow

`UpsertSaveTrailer` performs these steps:

1. generate a new `sav_` ID via `AllocateSaveID`
2. invoke native Git trailer rewriting in the discovered repository/worktree context from `ctx`
3. replace any existing save trailer with a new canonical `Opax-Save: <new-id>` value
4. rely on Git's own handling for trailer placement, comment/template blocks, `core.commentChar`, and linked-worktree config
5. return the updated message plus the allocated save ID

### Placement Rules

The helper should behave like a normal Git trailer writer for Opax-managed hook flows:

- keep the main body intact
- preserve existing non-Opax trailer ordering
- place the trailer according to native Git trailer semantics
- honor repository/worktree config, including custom comment characters and linked-worktree config
- treat ambiguous commit-message layouts as outside the broad pure-Go parity contract; Git owns hook-time message semantics

### Regeneration Rules

The helper always generates a fresh save ID when invoked for a commit that will create a new commit object, including:

- normal commits
- `--amend`
- merge commits
- squash/rebase commit creation
- cherry-pick commit creation

Reusing an old `Opax-Save` would point a new commit at the wrong future save, so replace semantics are required.

### Parse Rules

`ParseSaveTrailer` and `ParseSaveTrailerFromCommit` must:

- return `(id, true, nil)` when exactly one valid trailer exists
- return `(_, false, nil)` when the trailer is absent
- match `Opax-Save` case-insensitively for detection, replacement, and duplicate validation
- emit canonical spelling `Opax-Save` when inserting a trailer
- validate the value with `types.SaveID.Validate()`
- return an error when multiple matching save trailers exist or the value is not a valid `sav_` ID

### Runtime Boundary

Phase 0 ownership model at this feature boundary:

- native Git owns trailer mutation and trailer recognition semantics
- Go plus the typed `internal/git` native adapter own committed-object reads and save-trailer validation policy
- this feature does not introduce extra ad hoc shell-out boundaries beyond the shared adapter

CI pins a minimum Git version (`2.30.0`) for trailer integration suites to reduce environment drift. Locally, the Git-backed suites skip only when Git is unavailable.

### Aborted Commits

Aborted commits may orphan preallocated `sav_` IDs. This is acceptable. No cleanup or reuse mechanism is required.

---

## Edge Cases

- **Existing `Opax-Save` trailer present** - remove and replace it with a new value
- **Existing `opax-save` trailer present** - treat it as the same token and replace it with canonical `Opax-Save`
- **Commented commit template** - preserve comments and insert the trailer above them
- **Non-default `core.commentChar`** - honor Git's configured comment prefix, including multi-character values such as `//`
- **`core.commentChar=auto`** - follow native Git behavior instead of Opax-owned heuristics
- **Linked worktree config** - honor worktree-local config by running trailer mutation in the discovered worktree context
- **Commit message with other trailers** - preserve them; only rewrite `Opax-Save`
- **Body text ending with `token: value` lines but no blank separator** - treat those lines as body text, not as an existing trailer block
- **Malformed existing save ID** - replace during prepare phase; error during parse phase if reading an already committed message
- **Multiple `Opax-Save` trailers** - error on parse; normalize to one on upsert

---

## Acceptance Criteria

- `AllocateSaveID` returns valid `sav_` IDs
- `UpsertSaveTrailer` inserts exactly one `Opax-Save` trailer into a plain commit message
- `UpsertSaveTrailer` uses native Git trailer semantics in the discovered repository/worktree context
- `UpsertSaveTrailer` preserves native Git trailer visibility and comment/template placement
- `UpsertSaveTrailer` honors linked-worktree config for hook-time mutation
- `UpsertSaveTrailer` replaces any existing save trailer with a new ID using case-insensitive token matching
- `ParseSaveTrailer` parses exactly one valid trailer and rejects malformed or duplicate values
- `ParseSaveTrailerFromCommit` reads the committed trailer from an existing commit object
- Aborted commits do not require save-ID cleanup or reuse

---

## Test Plan

### Git-backed integration layer

- `TestTrailerParityUpsertSaveTrailer`: native-Git rewrite parity for subject-only, subject+body, existing non-Opax trailers, mixed-case `opax-save` replacement, blank-line recognition, default/custom comment blocks, and scissor placement
- `TestTrailerParityParseSaveTrailer`: parse parity for absent trailer, proper blank-separated trailer block, unseparated body text, parse-output preservation of other trailers, and trailer recognition with retained template comment lines
- `TestTrailerPolicyUpsertSaveTrailerUsesLinkedWorktreeConfig`: hook-time mutation honors worktree-local config instead of shared config only
- each rewrite test reuses the Opax-generated `sav_` in the Git invocation and compares full message bytes

### Drift Detector Layer (selected upstream-inspired cases)

- `TestTrailerOracleDriftDetectors`: bodiless/no-trailing-newline, bodied/no-trailing-newline, replace-existing-while-preserving-others, and comment-template placement checks
- keeps the list intentionally short/high-signal while catching semantic drift early

### Opax Policy Layer

- `TestTrailerPolicyAllocateSaveID`: generated IDs are valid `sav_` values
- `TestTrailerPolicyParseSaveTrailerValidation`: malformed and duplicate save trailer errors are Opax-enforced
- `TestTrailerPolicyCanonicalTokenSpellingOnInsert`: insertion always emits canonical `Opax-Save`
- `TestTrailerPolicyParseSaveTrailerFromCommit` + `TestTrailerPolicyParseSaveTrailerFromCommitAppliesValidation`: read committed message and apply Opax validation
- `TestTrailerPolicyParseSaveTrailerFromCommitWithRetainedTemplateComments`: parse committed save trailer when retained comment/template lines follow the trailer
- `TestTrailerPolicyParseSaveTrailerFromCommitHonorsCommentCharInLinkedWorktreeContext`: parse committed save trailer in linked-worktree context with configured non-default comment-char semantics
- `TestTrailerPolicyAutoCommentCharFailClosedHeuristics`: body content remains intact on representative `core.commentChar=auto` inputs now that mutation follows native Git behavior

### Upstream References

- Git documentation: [`git interpret-trailers`](https://raw.githubusercontent.com/git/git/master/Documentation/git-interpret-trailers.adoc)
- Upstream source of ported cases: [`t/t7513-interpret-trailers.sh`](https://raw.githubusercontent.com/git/git/master/t/t7513-interpret-trailers.sh)
