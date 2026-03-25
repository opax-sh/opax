# FEAT-0005 - Repo Discovery

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Dependencies:** EPIC-0000 (config + lock utilities only)
**Dependents:** FEAT-0006 through FEAT-0011

---

## Problem

Every other git feature assumes it knows where the repository lives and where Opax state should be stored. That assumption is wrong often enough to cause subtle corruption:

- linked worktrees put `.git` behind a file indirection
- submodules have their own gitdir layout
- Opax state belongs in the common git dir, not in a worktree-private admin directory
- bare repositories do not provide the worktree assumptions Phase 0 relies on

If repo discovery is hand-waved, the lock file, CAS directory, branch writes, and config updates can all end up in the wrong place.

---

## Design

### Package

`internal/git/` owns repo discovery. No other package should infer git paths independently.

### Output Type

```go
type RepoContext struct {
    RepoRoot         string
    WorkTreeRoot     string
    GitDir           string
    CommonGitDir     string
    OpaxDir          string
    IsLinkedWorktree bool
}
```

### Public API

```go
// DiscoverRepo resolves repository paths starting from a worktree-visible startDir.
// It returns ErrNotGitRepo when no repository can be found.
// It returns ErrBareRepo for bare repositories, which are unsupported in Phase 0.
func DiscoverRepo(startDir string) (*RepoContext, error)

// EnsureOpaxDir creates CommonGitDir/opax if it does not already exist.
// It is safe to call repeatedly.
func EnsureOpaxDir(ctx *RepoContext) error
```

### Path Rules

- `startDir` is a worktree-visible path from the CLI boundary or another user-facing entrypoint
- `RepoRoot` and `WorkTreeRoot` are the same Phase 0 root path for that worktree
- `GitDir` is the repository's effective gitdir
- `CommonGitDir` is where shared repository state lives; on normal repos it equals `GitDir`
- `OpaxDir` is `filepath.Join(CommonGitDir, "opax")`

Key constraint: the lock file remains `filepath.Join(CommonGitDir, "opax.lock")`, matching the architecture invariant.

Non-goal: `DiscoverRepo` is not a generic "recover a repo from any Git admin path" API. Internal code should not call it from `.git/opax`, a separate common git dir, or other admin-only directories. If a future entrypoint genuinely starts from admin state, it should use a separate explicit resolver.

---

## Specification

### Discovery Algorithm

1. Normalize `startDir` to an absolute, symlink-resolved directory path
2. Open the repository from `startDir` with `go-git` parent discovery enabled
3. Ask the opened repository for its worktree; if none exists, return `ErrBareRepo`
4. Read the effective gitdir from repository storage
5. Resolve `CommonGitDir` from `gitdir/commondir` when present; otherwise use `GitDir`
6. Populate `OpaxDir = CommonGitDir/opax`
7. Return normalized absolute paths for all directory fields

### Linked Worktree Handling

For linked worktrees, the repo context must distinguish the worktree location from the shared git state location:

- worktree root: the directory the user is operating in
- worktree gitdir: typically `.git/worktrees/<name>` inside the main repo's common git dir
- common git dir: the main repository `.git`

Opax state uses the common git dir because `opax/v1`, notes refs, `.git/config`, and `.git/opax.lock` are repository-wide, not worktree-local.

### Submodules

If discovery is invoked inside a submodule, the submodule is treated as its own supported repo. The parent repository is irrelevant to this feature.

### Bare Repositories

Phase 0 rejects bare repos because later epics assume a normal developer workflow with hooks, passive capture, and local repo context. Error form:

`git: bare repositories are unsupported in Phase 0`

### Directory Creation

`EnsureOpaxDir` creates:

`{CommonGitDir}/opax/`

It does **not** create the lock file, CAS content dir, database file, or any hook files.

---

## Edge Cases

- **Nested directory invocation** - starting from `repo/internal/git` must still resolve the repository root
- **Nested path in a separate-git-dir worktree** - still resolve the worktree root and detached gitdir
- **Missing `.git` entry** - return `ErrNotGitRepo`, not a generic filesystem error
- **Missing `commondir` target** - fail closed; do not silently fall back to the worktree gitdir
- `**.git` symlink directory** - treat the symlink target as the gitdir, not as an invalid gitdir file
- **Existing `opax/` directory** - treat as success if it is a directory; error if it is a regular file
- **Symlinked start directory** - return cleaned absolute paths to avoid duplicate path identities

---

## Acceptance Criteria

- `DiscoverRepo` resolves a normal repository from both the repo root and nested directories
- `DiscoverRepo` resolves linked worktrees and points `CommonGitDir` at the shared git dir
- `DiscoverRepo` treats submodules as independent repositories
- `DiscoverRepo` rejects bare repositories with a clear Phase 0 error
- `DiscoverRepo` handles `.git` file indirection and `.git` symlink directories correctly
- `EnsureOpaxDir` creates `{CommonGitDir}/opax` idempotently
- `EnsureOpaxDir` errors if `{CommonGitDir}/opax` exists as a non-directory
- Returned paths are absolute, cleaned, and stable across repeated calls

---

## Test Plan


| Test                                           | What it verifies                | Pass condition                                |
| ---------------------------------------------- | ------------------------------- | --------------------------------------------- |
| `TestDiscoverRepoStandard`                     | Normal repo resolution          | Correct repo root, git dir, common git dir    |
| `TestDiscoverRepoNestedPath`                   | Upward search from subdirectory | Same result as repo root                      |
| `TestDiscoverRepoLinkedWorktree`               | Worktree resolution             | Common git dir points to shared `.git`        |
| `TestDiscoverRepoSubmodule`                    | Submodule handling              | Submodule treated as its own repo             |
| `TestDiscoverRepoBareRepo`                     | Unsupported topology            | Returns `ErrBareRepo`                         |
| `TestDiscoverRepoGitFileIndirection`           | `.git` file parsing             | Relative gitdir path resolved correctly       |
| `TestDiscoverRepoGitFileIndirectionNestedPath` | Separate git-dir nested path    | Same result as worktree root                  |
| `TestDiscoverRepoGitDirSymlink`                | `.git` symlink handling         | Symlink target is resolved as the git dir     |
| `TestEnsureOpaxDir`                            | Directory creation              | `CommonGitDir/opax` created once, repeat safe |
| `TestEnsureOpaxDirExistingFile`                | Invalid existing path           | Clear error when `opax` is not a directory    |
