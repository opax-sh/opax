# FEAT-0009 - Git Notes Operations

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
**Dependencies:** FEAT-0005 (Repo discovery)
**Dependents:** E4 commit linkage fallback, E5 rebuild, E8 memory plugin, future eval/review plugins

---

## Problem

Git notes are the mutable metadata layer in the Opax model. They are required for:

- session linkage fallback when trailers are disabled
- future plugin annotations like evals, reviews, and test results
- rebuilds that need to enumerate note namespaces and note entries

The data spec names the namespace layout, but not the API contract. Without a dedicated notes feature, callers will hand-roll ref naming, payload validation, and missing-ref creation in inconsistent ways.

---

## Design

### Namespace Mapping

A logical namespace maps to one notes ref:

`refs/opax/notes/{namespace}`

Examples:

- `sessions` -> `refs/opax/notes/sessions`
- `ext-reviews` -> `refs/opax/notes/ext-reviews`

### Public API

```go
type Note struct {
    Namespace  string
    CommitHash string
    Content    json.RawMessage
}

func WriteNote(ctx *RepoContext, note Note) error
func ReadNote(ctx *RepoContext, namespace, commitHash string) (*Note, error)
func ListNotes(ctx *RepoContext, namespace string) ([]Note, error)
func ListNoteNamespaces(ctx *RepoContext) ([]string, error)
```

### Payload Contract

The plumbing layer validates only the common note envelope requirements:

- valid JSON
- top-level JSON object
- contains a numeric `version` field

It does not interpret plugin-specific fields.

---

## Specification

### Namespace Validation

Valid namespace format:

- lowercase letters, numbers, and dashes only
- no slashes
- no `..`
- first-party names may be plain names like `sessions`
- extension names use `ext-{name}`

Invalid namespace strings are rejected before any git I/O.

### `WriteNote`

Rules:

1. validate namespace format
2. validate that `CommitHash` resolves to a commit in the repo
3. validate JSON payload envelope
4. lazily create the notes ref if missing
5. write or replace the note content for the target commit

Notes are mutable by design. Rewriting an existing note in the same namespace is allowed.

### `ReadNote`

Returns the note for the given `(namespace, commit)` pair if present, otherwise a typed not-found error.

### `ListNotes`

Returns all notes for a namespace. This exists primarily for rebuild and sync logic.

### `ListNoteNamespaces`

Enumerates note refs directly under `refs/opax/notes/` so the materializer can walk every namespace it knows about, including extension namespaces it does not understand semantically.

---

## Edge Cases

- **Target commit missing** - reject the write; notes attach to real commits only
- **Existing note JSON malformed** - `ReadNote` should surface a malformed-note error, not silently return raw bytes as valid structured content
- **Missing notes ref** - `ReadNote` and `ListNotes` treat it as empty; `WriteNote` bootstraps it lazily
- **Concurrent note writers** - note refs are mutable; last successful writer wins unless later coordination chooses stronger semantics
- **Namespace with slash** - reject to avoid path ambiguity and ref escaping

---

## Acceptance Criteria

- `WriteNote` writes valid JSON note payloads under `refs/opax/notes/{namespace}`
- `WriteNote` rejects invalid namespaces and non-existent target commits
- `WriteNote` bootstraps a missing notes ref lazily
- `ReadNote` reads a note back by namespace and commit hash
- `ListNotes` enumerates all notes in a namespace for rebuild flows
- `ListNoteNamespaces` enumerates both first-party and extension note namespaces
- Existing notes may be overwritten intentionally because notes are mutable

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestWriteNoteSessionsNamespace` | First-party notes ref mapping | Note stored under `refs/opax/notes/sessions` |
| `TestWriteNoteExtensionNamespace` | Extension namespace mapping | Note stored under `refs/opax/notes/ext-reviews` |
| `TestWriteNoteRejectsBadNamespace` | Namespace validation | Validation error |
| `TestWriteNoteRejectsMissingCommit` | Target existence check | Validation error |
| `TestWriteNoteBootstrapsRef` | Missing-ref creation | First write succeeds without prior notes ref |
| `TestReadNote` | Point read | Returns stored JSON payload |
| `TestListNotes` | Namespace enumeration | Returns all notes in namespace |
| `TestListNoteNamespaces` | Namespace discovery | Returns first-party and extension refs |
