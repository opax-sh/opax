| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-30 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-30 |

# ADR-007 — Git CLI boundary for hook-time semantics

## Status
Accepted

## Context
Opax is a git-native product. It already depends on a standard Git environment for repositories, hooks, commit creation, and distribution. The open question is where Opax should stop reimplementing Git behavior itself.

Trailer rewriting in `prepare-commit-msg` exposed the boundary sharply. Pure-Go logic can read committed objects reliably, but broad parity with Git's own commit-message, trailer, comment-char, worktree, and template semantics is a moving target. That is not product differentiation; it is shadow maintenance of Git behavior.

At the same time, Opax should remain a single Go binary without a daemon, external database, or extra local services.

## Options Considered

### Option A — Pure Go everywhere
- Pros: one runtime model, no shell-outs in production paths, deterministic unit tests.
- Cons: Opax owns Git trailer/message semantics and keeps rediscovering edge cases around comment handling, worktree-local config, and future Git drift.

### Option B — Native Git CLI for all Git operations
- Pros: maximum semantic fidelity with upstream Git behavior.
- Cons: weakens package boundaries, makes non-hook object operations depend on shelling out, and gives up the benefits of go-git for most of the core data model.

### Option C — Hybrid boundary
- Pros: keeps core repo/object/state access in Go while delegating hook-time commit-message semantics to native Git, where Git behavior is the product surface.
- Cons: introduces a deliberate mixed model that must be documented and tested clearly.

## Decision
Option C.

Opax treats Git as the host platform, not an accidental dependency. The core remains a single Go binary and continues to use Go/go-git for repo discovery, object reads, notes/branch/CAS operations, and committed-object validation. Native Git CLI is explicitly allowed in the narrow hook-time path where commit-message and trailer semantics must match Git itself.

The runtime claim is therefore: **single binary, no extra services beyond a standard Git environment**.

Hook-time trailer mutation is Git-owned. Read-side trailer validation remains Opax-owned in Go.

## Consequences

### Positive
- Trailer mutation tracks native Git behavior for comment chars, `core.commentChar=auto`, and linked worktree config.
- The Go surface becomes smaller and easier to reason about: object access and validation instead of commit-message layout emulation.
- The product claim becomes more honest: Git is an expected platform dependency, while daemons and extra infra remain out of scope.

### Negative
- Production behavior now includes one intentional shell-out boundary.
- The mixed model needs clear tests and docs so future work does not expand CLI usage casually.

### Follow-up
- FEAT-0010 documents `UpsertSaveTrailer` as a hook-time helper backed by native Git semantics.
- Product docs replace "zero runtime dependencies" wording with "single binary, no extra services beyond a standard Git environment."
- Future hook lifecycle work keeps Git CLI limited to hook-time commit-message semantics unless another ADR widens the boundary.

## References
- `docs/product/overview.md`
- `docs/product/data-spec.md`
- `docs/features/FEAT-0010-commit-trailer-support.md`
- `docs/epics/EPIC-0001-git-plumbing-layer.md`
