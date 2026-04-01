| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-30 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-31 |

# ADR-007 — Git CLI boundary for hook-time semantics

## Status
Superseded by ADR-008-native-git-backend-adapter-for-internal-git

## Superseded Note
This decision is kept for historical context. The active boundary is ADR-008, which makes native Git the production backend transport for `internal/git` while preserving Opax typed contracts in Go.

## Context
Opax is a git-native product. It depends on a standard Git environment for repositories, hooks, commit creation, and distribution. The boundary question is where Opax should stop reimplementing Git behavior itself.

Trailer rewriting in `prepare-commit-msg` exposed the boundary sharply. Pure-Go logic can read committed objects reliably, but broad parity with Git commit-message, trailer, comment-char, worktree, and template semantics is a moving target.

FEAT-0012 exposed a second pressure: the previous common-gitdir storage open path could fail in extension-enabled linked-worktree repositories (`extensions.worktreeConfig=true`) even when path-based go-git opens succeed.

## Options Considered

### Option A — Pure Go everywhere
- Pros: one runtime model, no shell-outs in production paths, deterministic unit tests.
- Cons: Opax owns Git message/trailer semantics and keeps rediscovering Git behavior edge cases.

### Option B — Native Git CLI for all Git operations
- Pros: maximum semantic fidelity with upstream Git behavior.
- Cons: weakens package boundaries, increases command orchestration/parsing overhead, and gives up typed in-process plumbing across the core data model.

### Option C — Bounded hybrid boundary
- Pros: keeps core repo/object/state operations in go-git while delegating only narrow Git-owned semantics to native Git.
- Cons: introduces a mixed model that must stay explicitly documented.

## Decision
Option C.

Opax uses go-git for major repository read/write plumbing, including linked-worktree repositories, by opening repositories from discovered worktree paths (`PlainOpenWithOptions(...DetectDotGit=true)`).

Native Git CLI is explicitly allowed only in bounded surfaces:
1. Hook-time trailer mutation/recognition semantics (`interpret-trailers`) where behavior must match Git.
2. Create-if-absent ref publication via `git update-ref <ref> <new> 000...` CAS semantics.

There is no broad compatibility fallback mode for major features in this decision.

The runtime claim remains: **single binary, no extra services beyond a standard Git environment**.

## Consequences

### Positive
- Trailer mutation tracks native Git behavior for comment chars, `core.commentChar=auto`, and worktree config.
- Major feature paths stay on one go-git backend rather than split fallback branches.
- Missing-ref create publication no longer writes ref files manually.
- The boundary stays explicit and narrow.

### Negative
- Production includes intentional shell-out boundaries in selected paths.
- If a future repository-format extension breaks path-based go-git opens, Opax will fail closed until another ADR broadens the boundary.

### Follow-up
- FEAT-0010 documents trailer mutation and trailer recognition as native-Git-owned behavior with Opax validation policy in Go.
- FEAT-0012 documents the path-based go-git cutover for extension-enabled linked-worktree compatibility.
- Future work keeps CLI usage limited to trailer semantics and `update-ref` CAS unless another ADR widens the boundary.

## References
- `docs/product/overview.md`
- `docs/product/data-spec.md`
- `docs/features/FEAT-0010-commit-trailer-support.md`
- `docs/features/FEAT-0012-git-boundary-compatibility-hardening.md`
- `docs/epics/EPIC-0001-git-plumbing-layer.md`
