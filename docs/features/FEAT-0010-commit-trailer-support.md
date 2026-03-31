# FEAT-0010 - Commit Trailer Support

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-03-31
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
2. resolve the repository's Git comment character from `ctx`, defaulting to `#` when unset
3. parse the existing commit message
4. remove any existing save trailers using case-insensitive token matching
5. insert exactly one canonical `Opax-Save: <new-id>` trailer
6. preserve all non-Opax trailers, body text, and comment lines
7. return the updated message plus the allocated save ID

### Placement Rules

The helper should behave like a normal Git trailer writer for supported Phase 0 cases:

- keep the main body intact
- maintain one blank line between body and trailer block when needed
- preserve existing non-Opax trailer ordering
- place the trailer after the body / existing trailer block and before the trailing commented template block
- treat scissor/template lines that begin with the active comment char as part of the preserved comment block

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

### Aborted Commits

Aborted commits may orphan preallocated `sav_` IDs. This is acceptable. No cleanup or reuse mechanism is required.

---

## Edge Cases

- **Existing `Opax-Save` trailer present** - remove and replace it with a new value
- **Existing `opax-save` trailer present** - treat it as the same token and replace it with canonical `Opax-Save`
- **Commented commit template** - preserve comments and insert the trailer above them
- **Non-default `core.commentChar`** - preserve the comment block using the configured comment character
- **Commit message with other trailers** - preserve them; only rewrite `Opax-Save`
- **Malformed existing save ID** - replace during prepare phase; error during parse phase if reading an already committed message
- **Multiple `Opax-Save` trailers** - error on parse; normalize to one on upsert

---

## Acceptance Criteria

- `AllocateSaveID` returns valid `sav_` IDs
- `UpsertSaveTrailer` inserts exactly one `Opax-Save` trailer into a plain commit message
- `UpsertSaveTrailer` reads Git comment formatting rules from the repository context
- `UpsertSaveTrailer` preserves non-Opax trailers and comment blocks
- `UpsertSaveTrailer` replaces any existing save trailer with a new ID using case-insensitive token matching
- `ParseSaveTrailer` parses exactly one valid trailer and rejects malformed or duplicate values
- `ParseSaveTrailerFromCommit` reads the committed trailer from an existing commit object
- Aborted commits do not require save-ID cleanup or reuse

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestAllocateSaveID` | Save ID generation | Returns valid `sav_` ID |
| `TestUpsertSaveTrailerPlainMessage` | Basic trailer insertion | Exactly one `Opax-Save` trailer added |
| `TestUpsertSaveTrailerReplacesExistingMixedCase` | Replace semantics | Mixed-case old trailer removed, canonical new one inserted |
| `TestUpsertSaveTrailerPreservesOtherTrailers` | Trailer coexistence | Other trailers preserved verbatim |
| `TestUpsertSaveTrailerPreservesCommentBlockDefaultChar` | Default comment char handling | `#` comments preserved below trailer block |
| `TestUpsertSaveTrailerPreservesCommentBlockCustomChar` | Custom comment char handling | Non-default comment block preserved below trailer block |
| `TestUpsertSaveTrailerHandlesScissors` | Scissor/template handling | Trailer inserted above preserved scissor block |
| `TestParseSaveTrailerAbsent` | Missing trailer | Returns `(_, false, nil)` |
| `TestParseSaveTrailerMalformedValue` | Invalid trailer payload | Error |
| `TestParseSaveTrailerDuplicateMixedCase` | Duplicate trailer detection | Error even with mixed token casing |
| `TestParseSaveTrailerFromCommit` | Commit object parsing | Trailer read correctly from commit |
