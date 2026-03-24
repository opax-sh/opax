# EPIC-0001: Git Plumbing Layer

**Status:** In Progress  
**Version:** 1.0.0-draft
**Date:** March 18, 2026
**Dependencies:** EPIC-0000 (types, config, admin lock utility)
**Dependents:** E4 (Integrated Write Path), E5 (SQLite Materialization), E8 (Memory Plugin), E9 (CLI Integration), E11 (Hooks & Init Lifecycle)

---

## Goal

Provide the low-level git substrate for Opax Phase 0: discover the repository safely, create and validate the `opax/v1` orphan branch, write and read records on that branch without touching the working tree, manage git notes under `refs/opax/notes/`*, support `Opax-Save` trailer parsing and insertion, and generate conservative refspec configuration for later `opax init`, `opax pull`, and `opax push` flows.

This epic is authoritative for EPIC-0001 if `docs/product/roadmap.md`, `docs/product/overview.md`, or `docs/product/data-spec.md` drift. The roadmap remains the execution index; this document resolves Phase 0 implementation details.

---

## Why This Epic Matters

Everything in Phase 0 depends on trustworthy git plumbing. If this layer is sloppy, every downstream promise becomes false:

- The write path cannot claim immutability if it overwrites refs blindly.
- The memory plugin cannot claim portability if it depends on the working tree or shell-only behavior.
- The materializer cannot claim rebuildability if branch reads are ambiguous or notes refs are inconsistent.
- The trailer story collapses if save IDs are only created after the commit already exists.

This is the riskiest Phase 0 epic because it combines custom git object manipulation, repo topology edge cases, and hook-driven commit lifecycle behavior.

---

## Resolved Design Decisions

### 1. Branch and Ref Names Are Fixed in Phase 0

Phase 0 does **not** treat these names as user-configurable, even though some product docs discuss future configurability.

- Data branch: `refs/heads/opax/v1`
- Notes refs: `refs/opax/notes/{namespace}`
- Commit trailer key: `Opax-Save`

This avoids accidental complexity in the hardest plumbing epic. Future configurability can be layered on after the branch, notes, and trailer flows are proven.

### 2. Trailer Preallocation, Save Finalization Later

Phase 0 uses the preallocation model selected during planning:

1. `prepare-commit-msg` allocates a fresh `sav_` ID
2. The hook inserts or replaces exactly one `Opax-Save: <save-id>` trailer
3. The commit is created normally
4. A later post-commit flow reads the committed trailer from `HEAD` and finalizes the save using the real commit hash

Implications:

- Aborted commits can leave unused `sav_` IDs; this is acceptable
- `FEAT-0010` owns trailer text manipulation and parsing, not save creation
- `FEAT-0026`, `FEAT-0048`, and `FEAT-0062` consume the preallocated save ID later

### 3. Direct Branch Reads Are Internal Primitives

`FEAT-0008` exists so rebuild, sync, and debugging code can read records directly from `opax/v1`. It is **not** the public Phase 0 query surface. Public search and list flows still go through SQLite in later epics.

### 4. Mutable Ref Publication Uses Per-Ref CAS + Retry

`FEAT-0007` and `FEAT-0009` publish mutable refs with expected-old-tip checks (`CheckAndSetReference`). No blind `update-ref`.

Phase 0 retry contract:

- `maxRefPublishAttempts = 8`
- exponential backoff starts at `10ms`, capped at `100ms`
- missing-ref first publication is create-if-absent CAS (no blind `CheckAndSetReference(newRef, nil)`)
- immutable object creation remains concurrent
- `.git/opax.lock` is not part of steady-state record/note publication
- repo-wide lock remains for bootstrap/admin flows only

### 5. Branch Commits Are Machine-Generated

Commits written to `opax/v1` are internal system commits, not user-authored source commits. The plumbing layer should not depend on `user.name` / `user.email` being configured. It uses a fixed machine identity:

- Name: `Opax`
- Email: `opax@local`

This keeps initialization and capture flows deterministic on clean machines and CI-style environments.

### 6. Stealth Default Means No Opax Data in Plain Git Fetch/Push

Phase 0 keeps Opax data out of default code sync flows:

- Plain `git fetch` should not fetch `opax/v1`
- Plain `git push` should not start pushing Opax refs implicitly
- Later `opax pull` / `opax push` commands use explicit refspecs

This resolves the roadmap/product-doc drift without violating the stealth-default decision in `docs/product/overview.md`.

---

## Feature Breakdown


| Feature ID | Feature                  | Purpose                                                                          | Notes                                         |
| ---------- | ------------------------ | -------------------------------------------------------------------------------- | --------------------------------------------- |
| FEAT-0005  | Repo discovery           | Resolve repo root, real git dir, common git dir, and `.git/opax/` ownership      | Foundation for every other git feature        |
| FEAT-0006  | Orphan branch management | Create and validate `opax/v1` with a root sentinel                               | Defines what a valid Opax branch is           |
| FEAT-0007  | Write records to branch  | Append-only record writes using blobs, trees, commits, and per-ref CAS retry      | Hardest feature in the epic                   |
| FEAT-0008  | Read records from branch | Point reads from `opax/v1` by record ID/path                                     | Internal primitive for rebuild/sync/debugging |
| FEAT-0009  | Git notes operations     | Read/write/list notes under `refs/opax/notes/`* with per-namespace CAS retry      | Mutable metadata layer                        |
| FEAT-0010  | Commit trailer support   | Insert and parse `Opax-Save` trailers using save-ID preallocation                | Hook installation happens later               |
| FEAT-0011  | Refspec configuration    | Generate conservative config for later init/pull/push flows                      | Must preserve stealth default                 |


---

## Shared Contracts

### Package Boundary

`internal/git/` remains plumbing only. It may:

- open repositories
- inspect refs, commits, trees, and notes
- write branch commits on `opax/v1`
- read and modify commit message text for trailers
- read and write Opax-specific git config values

It may **not**:

- touch the working tree or index
- decide save attribution rules
- perform hygiene or CAS logic
- materialize SQLite rows
- install hook wrapper scripts

Steady-state writes must follow optimistic per-ref CAS publication. Repo-wide lock usage is exceptional (bootstrap/admin only).

### Repo Context Contract

All downstream git operations consume a discovery result from `FEAT-0005`, not ad hoc path guessing.

Expected fields:

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

Key rule: Opax state lives under `CommonGitDir`, not under a worktree-specific gitdir.

### Branch Identity Contract

`FEAT-0006` defines a valid Opax branch as one whose history descends from an orphan root commit containing `meta/version.json` with the expected branch identity and layout version.

Sentinel path:

`meta/version.json`

Sentinel payload shape:

```json
{
  "branch": "opax/v1",
  "layout_version": 1,
  "created_by": "opax"
}
```

### Record Path Contract

`FEAT-0007` and `FEAT-0008` share the same deterministic path derivation rules:

- `sessions/{sha256(id)[:2]}/{id}/...`
- `saves/{sha256(id)[:2]}/{id}/...`
- `ext-{plugin}/{sha256(id)[:2]}/{id}/...`

No caller-supplied absolute paths, parent traversal, or unscoped tree edits.

### Notes Contract

`FEAT-0009` owns notes refs under `refs/opax/notes/*` only. It does not reuse normal branch writes.

- First-party example: `refs/opax/notes/sessions`
- Third-party/community example: `refs/opax/notes/ext-reviews`

### Trailer Contract

`FEAT-0010` guarantees:

- exactly one `Opax-Save` trailer in the final prepared message
- valid `sav_` IDs only
- replace-on-regenerate semantics for amend/rebase/cherry-pick/squash flows
- parser helpers for existing commits and raw commit messages

### Refspec Contract

`FEAT-0011` must treat these as distinct objects:

- branch ref: `refs/heads/opax/v1`
- custom refs and notes: `refs/opax/*`

The feature must not blur them into a fake shared namespace.

---

## Support Matrix


| Repo topology                    | Phase 0 support | Notes                                     |
| -------------------------------- | --------------- | ----------------------------------------- |
| Normal non-bare repo             | Yes             | Primary path                              |
| Linked worktree                  | Yes             | Use common git dir for Opax state         |
| Submodule repo                   | Yes             | Treat the submodule as its own repository |
| Bare repo                        | No              | Clear error in Phase 0                    |
| Detached `.git` file indirection | Yes             | Must resolve real gitdir safely           |


---

## Non-Goals

These are out of scope for EPIC-0001:

- CAS file storage and size-threshold decisions
- hygiene pipeline execution
- save attribution logic (`file_overlap`, `temporal`)
- hook wrapper installation and conflict handling
- SQLite schema, rebuild, sync, or FTS queries
- cross-remote sync UX (`opax pull`, `opax push`) beyond refspec generation
- automatic recovery of invalid Opax branches or corrupt notes refs

If the code starts solving any of the above inside `internal/git/`, the epic has drifted.

---

## Risks


| Risk                                                                 | Impact      | Mitigation                                                                            |
| -------------------------------------------------------------------- | ----------- | ------------------------------------------------------------------------------------- |
| Tree mutation logic in go-git is awkward or incomplete               | High        | Keep a narrow internal fallback to shell plumbing with identical tests and semantics  |
| Worktree/common-dir resolution is mishandled                         | High        | Make `RepoContext` authoritative and test linked worktrees explicitly                 |
| Stale-tip writes silently overwrite branch history                   | High        | Require compare-and-swap updates and explicit conflict errors                         |
| Refspec config changes accidentally change plain `git push` behavior | High        | Store Opax explicit refspecs separately; never mutate `remote.<name>.push` in Phase 0 |
| Trailer helper reuses stale save IDs across amend/rebase flows       | Medium-High | Regenerate and replace `Opax-Save` on every prepare phase                             |
| Invalid existing branch or note state causes silent corruption       | Medium      | Validate aggressively and fail closed; no auto-repair in Phase 0                      |


---

## Verification Checklist

- `FEAT-0005` resolves normal repos, worktrees, and submodules correctly
- `FEAT-0006` creates `refs/heads/opax/v1` idempotently and rejects malformed branches
- `FEAT-0006` takes `.git/opax.lock` only for missing-branch bootstrap; existing-branch validation is lock-free
- `FEAT-0007` writes records without touching the working tree and rejects duplicate record IDs
- `FEAT-0007` uses per-ref CAS publish with bounded retry; `.git/opax.lock` is bootstrap/admin-only
- `FEAT-0008` can read branch records directly by deterministic path
- `FEAT-0009` can bootstrap missing notes refs and enumerate notes for rebuild
- `FEAT-0010` inserts exactly one valid `Opax-Save` trailer and parses it back from commits
- `FEAT-0011` preserves stealth default: plain `git fetch` and `git push` remain code-centric
- All git writes use machine identity `Opax <opax@local>`
- No code in `internal/git/` checks out or modifies the working tree

---

## Files Planned


| File                       | Role                                                                                           |
| -------------------------- | ---------------------------------------------------------------------------------------------- |
| `internal/git/git.go`      | Repo discovery, branch management, branch read/write helpers, notes, trailers, refspec helpers |
| `internal/git/git_test.go` | Table-driven tests and repo-fixture integration tests                                          |
| `internal/git/testdata/`   | Small test repositories and message fixtures as needed                                         |
