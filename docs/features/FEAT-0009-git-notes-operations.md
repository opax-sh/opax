# FEAT-0009 - Git Notes Operations

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-03-30
**Dependencies:** FEAT-0005 (Repo discovery)
**Dependents:** E4 commit linkage fallback, E5 rebuild, E8 memory plugin, future eval/review plugins

---

## Problem

Git notes are the mutable metadata layer in the Opax model. They are required for:

- session linkage fallback when trailers are disabled
- future plugin annotations like evals, reviews, and test results
- rebuild flows that enumerate namespace refs and note entries

The API contract must preserve concurrent note writes in a namespace without falling back to a repo-wide lock.

---

## Design

### Namespace Mapping

A logical namespace maps to one notes ref:

`refs/notes/opax/{namespace}`

Examples:

- `sessions` -> `refs/notes/opax/sessions`
- `ext-reviews` -> `refs/notes/opax/ext-reviews`

Each namespace ref is its own concurrency domain.
Opax-managed notes must be directly usable through native Git notes tooling:

- `git notes --ref=opax/sessions show <commit>`
- `git notes --ref=opax/ext-reviews add -m '<json>' <commit>`

### Public API

```go
func WriteNote(ctx *RepoContext, note types.Note) error
func ReadNote(ctx *RepoContext, namespace, commitHash string) (*types.Note, error)
func ListNotes(ctx *RepoContext, namespace string) ([]types.Note, error)
func ListNoteNamespaces(ctx *RepoContext) ([]string, error)
```

### Payload Contract

The stored git-note blob is one JSON object per target commit:

- top-level JSON object
- numeric top-level `version` field
- all remaining top-level fields are plugin-owned payload fields

The Go API still uses shared `types.Note`:

- `Version` is the extracted top-level `version`
- `Content` is the remaining JSON object after `version` is split out
- `Content` must not itself contain a top-level `version` key on write

### Ref Publication Contract

`WriteNote` publishes each notes ref with optimistic CAS:

- read current notes tip for namespace
- rebuild notes commit against that tip
- strict expected-tip publish on that namespace ref:
  - existing ref tip: `CheckAndSetReference(newRef, oldRef)`
  - missing ref tip: create-if-absent (no blind `CheckAndSetReference(newRef, nil)`)
- retry only on `storage.ErrReferenceHasChanged`

Bounded retry policy in Phase 0:

- `maxRefPublishAttempts = 8`
- exponential backoff starts at `10ms`
- backoff cap is `100ms`
- no user-facing configuration

Repo-wide `.git/opax.lock` is not part of steady-state note writes.

---

## Specification

### Namespace Validation

Valid namespace format:

- lowercase letters, numbers, and dashes only
- no slashes
- no `..`
- first-party names may be plain names like `sessions`
- extension names use `ext-{name}`

Invalid namespaces are rejected before git I/O.

### `WriteNote`

Rules:

1. validate namespace format
2. validate that `CommitHash` resolves to a commit
3. validate `types.Note.Version > 0`
4. validate `Content` is a JSON object without a top-level `version` field
5. merge `version` plus content fields into one stored git-note payload
6. for `attempt := 1..maxRefPublishAttempts`:
7. read current namespace ref tip (or treat missing ref as empty tree baseline)
8. rebuild notes commit for this `(namespace, commit)` write
9. publish namespace ref via CAS
10. on `ErrReferenceHasChanged`, retry with bounded backoff
11. on exhaustion, return `ErrNoteConflict`

Overwrite semantics:

- writing the same `(namespace, commit)` is intentional last-writer-wins
- concurrent writes to distinct commits in the same namespace must both survive

### `ReadNote`

Returns the note for `(namespace, commit)` if present; otherwise `ErrNoteNotFound`.

### `ListNotes`

Returns all notes for a namespace; used for rebuild and sync. The implementation must read both native git-notes flat entries and fanout entries, because Git may emit either layout.

### `ListNoteNamespaces`

Enumerates refs directly under `refs/notes/opax/`.

---

## Edge Cases

- **Target commit missing** - reject the write
- **Existing note JSON malformed** - surface `ErrMalformedNote`
- **Missing notes ref** - `ReadNote`/`ListNotes` treat as empty; `WriteNote` bootstraps with create-if-absent CAS and retries on first-writer conflict
- **Concurrent same-target writes** - intentional last-writer-wins
- **Concurrent different-target writes** - both writes preserved via CAS retry
- **Namespace with slash** - reject to avoid ref/path ambiguity
- **Nested note refs under `refs/notes/opax/`** - reject as malformed namespace state

---

## Acceptance Criteria

- `WriteNote` writes valid JSON payloads under `refs/notes/opax/{namespace}`
- `WriteNote` rejects invalid namespaces and non-existent target commits
- `WriteNote` bootstraps a missing namespace ref lazily
- `WriteNote` uses per-namespace CAS publish with bounded retry
- Opax-written notes are readable with `git notes --ref=opax/<namespace> show <commit>`
- Git-written notes under `refs/notes/opax/{namespace}` are readable through `ReadNote`
- `ReadNote` reads by namespace and commit hash
- `ListNotes` enumerates all notes in a namespace
- `ListNoteNamespaces` returns first-party and extension namespaces
- Same-target overwrite remains last-writer-wins
- Distinct concurrent writes in one namespace are both preserved

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestWriteNoteSessionsNamespace` | First-party notes ref mapping | Note stored under `refs/notes/opax/sessions` |
| `TestWriteNoteExtensionNamespace` | Extension namespace mapping | Note stored under `refs/notes/opax/ext-reviews` |
| `TestWriteNoteRejectsBadNamespace` | Namespace validation | Validation error |
| `TestWriteNoteRejectsMissingCommit` | Target existence check | Validation error |
| `TestWriteNoteBootstrapsRef` | Missing-ref creation | First write succeeds without prior ref |
| `TestWriteNoteConcurrentDistinctTargets` | Namespace CAS concurrency | Concurrent writes to distinct commits are both preserved |
| `TestWriteNoteConcurrentOverwrite` | Same-target overwrite semantics | Last writer wins without ref corruption |
| `TestReadNoteGitNotesInterop` | Native Git read path | Git-written note is readable through `ReadNote` |
| `TestWriteNoteGitNotesInterop` | Native Git show path | `git notes --ref=opax/<namespace> show` reads Opax-written note |
| `TestListNotes` | Namespace enumeration | Returns all notes in namespace |
| `TestListNoteNamespaces` | Namespace discovery | Returns first-party and extension refs |
