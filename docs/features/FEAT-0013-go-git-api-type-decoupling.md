# FEAT-0013 - go-git API and Type Decoupling

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** In Progress
**Last synced:** 2026-04-15
**Dependencies:** FEAT-0012 (Native backend adapter migration)
**Dependents:** Future `internal/git` callers, module dependency cleanup

---

## Problem

FEAT-0012 completed the production transport migration to native Git, but it intentionally froze a compatibility surface around `github.com/go-git/go-git/v5/plumbing` so the backend cutover could land without widening the API change set.

That leaves two pieces of follow-up work:

- exported `internal/git` contracts still expose `go-git/plumbing` types
- production code still carries the frozen internal compatibility surface and the module dependency remains in `go.mod`

Until that follow-up lands, Opax is native Git in production behavior but not fully decoupled from `go-git`.

---

## Scope

### In Scope

- Replace exported `go-git/plumbing` contract exposure with Opax-owned types while preserving runtime behavior and typed errors.
- Remove remaining internal `go-git/plumbing` usage after the new API surface is stable.
- Drop the `github.com/go-git/go-git/v5` module dependency once no production or test contract requires it.
- Keep native Git as the only production transport for `internal/git`.

### Out of Scope

- Reopening FEAT-0012 production transport decisions.
- Adding dual backends or fallback runtime modes.
- Changing Opax validation rules, repository semantics, or typed error behavior without a separate feature/ADR decision.

---

## Contracts

- FEAT-0013 starts only after FEAT-0012 is complete and docs are synced to the native-backend production boundary.
- Stage 1 owns exported contract decoupling only:
  - replace exported `go-git/plumbing` types with Opax-owned equivalents
  - preserve runtime behavior, typed error semantics, and caller compatibility notes
- Stage 2 owns remaining internal cleanup only after Stage 1 is stable:
  - remove leftover `go-git/plumbing` imports in production code
  - remove obsolete test-oracle usage if it is still present
  - drop the `go-git` module dependency when no code path requires it
- Native Git remains the only production transport throughout both stages.

---

## Acceptance Criteria

- [ ] FEAT-0013 Stage 1 removes exported `go-git/plumbing` exposure from `internal/git` without changing behavior or typed errors.
- [ ] FEAT-0013 Stage 2 removes remaining production `go-git/plumbing` dependence and deletes the module dependency when safe.
- [ ] Docs and caller-facing compatibility notes are updated alongside each stage.
- [ ] `go test ./internal/git/...` and `make test` stay green throughout the transition.

---

## Test Plan

- Add focused caller-compatibility tests for the new Opax-owned exported types introduced in Stage 1.
- Keep the existing FEAT-0012 native-backend proof gates green while decoupling the API surface.
- Extend the import guard or equivalent repo check so it matches the allowed dependency surface for each stage.
- Verify `go.mod`/`go.sum` cleanup only after both production code and required tests no longer import `go-git`.

---

## Notes

- FEAT-0013 follows FEAT-0012 closeout and owns exported contract decoupling first, then remaining internal cleanup and module removal.
- This feature is intentionally staged so contract churn happens before module cleanup.
