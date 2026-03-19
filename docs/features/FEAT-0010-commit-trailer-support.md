# FEAT-0010 - Commit Trailer Support

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
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

`internal/git/` owns trailer text manipulation and trailer parsing. Hook installation, wrapper scripts, and post-commit orchestration belong to E11.

### Public API

```go
func AllocateSaveID() types.SaveID
func UpsertSaveTrailer(message []byte) ([]byte, types.SaveID, error)
func ParseSaveTrailer(message []byte) (types.SaveID, bool, error)
func ParseSaveTrailerFromCommit(ctx *RepoContext, commitHash string) (types.SaveID, bool, error)
```

### Fixed Trailer Key

Phase 0 uses a fixed trailer key:

`Opax-Save`

The helper does not support custom keys or prefixes in Phase 0.

---

## Specification

### Preallocation Flow

`UpsertSaveTrailer` performs these steps:

1. generate a new `sav_` ID via `AllocateSaveID`
2. parse the existing commit message
3. remove any existing `Opax-Save` trailers
4. insert exactly one `Opax-Save: <new-id>` trailer
5. preserve all non-Opax trailers, body text, and comment lines
6. return the updated message plus the allocated save ID

### Placement Rules

The helper should behave like a normal Git trailer writer:

- keep the main body intact
- maintain one blank line between body and trailer block when needed
- place the trailer before the commented template block if the message contains comment lines

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
- return an error when multiple `Opax-Save` trailers exist or the value is not a valid `sav_` ID

### Aborted Commits

Aborted commits may orphan preallocated `sav_` IDs. This is acceptable. No cleanup or reuse mechanism is required.

---

## Edge Cases

- **Existing `Opax-Save` trailer present** - remove and replace it with a new value
- **Commented commit template** - preserve comments and insert the trailer above them
- **Commit message with other trailers** - preserve them; only rewrite `Opax-Save`
- **Malformed existing save ID** - replace during prepare phase; error during parse phase if reading an already committed message
- **Multiple `Opax-Save` trailers** - error on parse; normalize to one on upsert

---

## Acceptance Criteria

- `AllocateSaveID` returns valid `sav_` IDs
- `UpsertSaveTrailer` inserts exactly one `Opax-Save` trailer into a plain commit message
- `UpsertSaveTrailer` preserves non-Opax trailers and comment blocks
- `UpsertSaveTrailer` replaces any existing `Opax-Save` trailer with a new ID
- `ParseSaveTrailer` parses exactly one valid trailer and rejects malformed or duplicate values
- `ParseSaveTrailerFromCommit` reads the committed trailer from an existing commit object
- Aborted commits do not require save-ID cleanup or reuse

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestAllocateSaveID` | Save ID generation | Returns valid `sav_` ID |
| `TestUpsertSaveTrailerPlainMessage` | Basic trailer insertion | Exactly one `Opax-Save` trailer added |
| `TestUpsertSaveTrailerReplacesExisting` | Replace semantics | Old trailer removed, new one inserted |
| `TestUpsertSaveTrailerPreservesOtherTrailers` | Trailer coexistence | Other trailers preserved verbatim |
| `TestUpsertSaveTrailerPreservesComments` | Comment block handling | Comments preserved below trailer block |
| `TestParseSaveTrailer` | Raw message parsing | Returns valid `sav_` ID |
| `TestParseSaveTrailerDuplicate` | Duplicate trailer detection | Error |
| `TestParseSaveTrailerFromCommit` | Commit object parsing | Trailer read correctly from commit |
