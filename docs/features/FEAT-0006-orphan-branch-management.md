# FEAT-0006 - Orphan Branch Management

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
**Dependencies:** FEAT-0005 (Repo discovery)
**Dependents:** FEAT-0007, FEAT-0008, FEAT-0011, E5 rebuild/sync, E9 init

---

## Problem

Opax needs a single append-only data branch that is clearly not part of the source history. The roadmap says `opax/v1` should be created if absent, but that leaves the important questions unanswered:

- what exactly makes an existing branch a valid Opax branch?
- how does the code distinguish a real Opax branch from a user branch that happens to share the name?
- what does the first commit contain if git cannot store empty directories?

If initialization is vague, later writes may append to the wrong history or silently treat a corrupt branch as healthy.

---

## Design

### Branch Identity

Phase 0 uses exactly one data branch:

`refs/heads/opax/v1`

### Root Sentinel

The orphan root commit contains a required sentinel file:

`meta/version.json`

Content:

```json
{
  "branch": "opax/v1",
  "layout_version": 1,
  "created_by": "opax"
}
```

The sentinel exists to make validation explicit. A directory shape alone is too easy to fake accidentally.

### Public API

```go
// EnsureOpaxBranch creates refs/heads/opax/v1 if absent and validates it if present.
// Returns the current branch tip after creation or validation.
func EnsureOpaxBranch(ctx *RepoContext) (plumbing.Hash, error)

// GetOpaxBranchTip returns the current opax/v1 tip if the branch exists.
func GetOpaxBranchTip(ctx *RepoContext) (plumbing.Hash, error)

// ValidateOpaxBranch verifies that the branch identity and sentinel are correct.
func ValidateOpaxBranch(ctx *RepoContext) error
```

### Commit Identity

The initialization commit uses the fixed machine identity declared in the epic doc:

- Author: `Opax <opax@local>`
- Committer: `Opax <opax@local>`

Commit message:

`opax: initialize opax/v1`

---

## Specification

### Creation Rules

If `refs/heads/opax/v1` does not exist:

1. Create a root tree containing only `meta/version.json`
2. Create an orphan commit with no parents
3. Point `refs/heads/opax/v1` at that commit

No sessions, saves, or extension directories are created at init time because git does not track empty directories.

### Validation Rules

An existing `refs/heads/opax/v1` is valid only if all of the following hold:

- the ref resolves to a commit
- the branch ancestry bottoms out in a root commit with zero parents
- the current tip tree still contains `meta/version.json`
- the sentinel JSON parses and matches:
  - `branch == "opax/v1"`
  - `layout_version == 1`
  - `created_by == "opax"`

### Failure Policy

This feature fails closed. It does **not** repair malformed branches automatically.

Examples of invalid state:

- `refs/heads/opax/v1` points at a non-commit object
- the root commit is not orphaned
- `meta/version.json` is missing from the tip tree
- the sentinel payload is malformed or mismatched

These return a clear validation error and leave the branch untouched.

### Idempotency

Calling `EnsureOpaxBranch` on an already valid branch is a no-op that returns the existing tip.

---

## Edge Cases

- **Branch name exists but points to source history** - reject; do not append Opax data to code history
- **Sentinel exists but payload mismatches** - reject as invalid branch identity
- **Current tip lost the sentinel due to a buggy prior write** - reject; this is branch corruption
- **Remote-tracking `origin/opax/v1` exists but local branch does not** - out of scope here; this feature manages the local branch only
- **Initialization interrupted before ref update** - unreachable objects are acceptable; the branch remains absent and the operation can be retried

---

## Acceptance Criteria

- `EnsureOpaxBranch` creates `refs/heads/opax/v1` when absent
- The initial branch commit is orphaned and contains `meta/version.json`
- The sentinel payload matches the required branch identity and layout version
- `EnsureOpaxBranch` is idempotent when the branch already exists and is valid
- `ValidateOpaxBranch` rejects non-commit refs, non-orphan roots, missing sentinel files, and malformed sentinel JSON
- Initialization does not require `user.name` or `user.email`

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestEnsureOpaxBranchCreatesRoot` | First-time initialization | Local branch created with orphan root commit |
| `TestEnsureOpaxBranchSentinel` | Sentinel contents | `meta/version.json` matches required JSON payload |
| `TestEnsureOpaxBranchIdempotent` | Repeat creation | Existing valid tip returned, no new commit |
| `TestValidateOpaxBranchMissingSentinel` | Corrupt branch detection | Validation error |
| `TestValidateOpaxBranchWrongPayload` | Sentinel identity checks | Validation error |
| `TestValidateOpaxBranchNonCommitRef` | Ref type safety | Validation error |
| `TestValidateOpaxBranchNonOrphanRoot` | Root ancestry check | Validation error |
