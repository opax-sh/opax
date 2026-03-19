# FEAT-0011 - Refspec Configuration

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Planned
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Opax branch identity)
**Dependents:** E9 `opax init`, future `opax pull` / `opax push`, E11 hook/setup validation

---

## Problem

The roadmap and product docs want two things at once:

- Opax data should not inflate normal code fetch/push behavior
- Opax branch and notes refs still need a reproducible sync story

Those goals are compatible, but only if the refspec design is conservative. If `opax init` mutates standard push behavior carelessly, it can make plain `git push` start sending Opax refs unexpectedly. That would violate the stealth default and surprise users.

---

## Design

### Principle

Phase 0 separates **default code sync** from **explicit Opax sync**.

- default code sync stays on standard Git remote config
- explicit Opax sync uses Opax-owned config keys storing refspecs for later commands

### Public API

```go
type RefspecPlan struct {
    DefaultFetchExclusions []string
    OpaxFetch             []string
    OpaxPush              []string
}

func BuildRefspecPlan(remote string) (*RefspecPlan, error)
func ApplyRefspecPlan(ctx *RepoContext, remote string, plan *RefspecPlan) error
func ReadRefspecPlan(ctx *RepoContext, remote string) (*RefspecPlan, error)
```

### Config Shape

Standard Git config remains responsible for code fetch behavior. Opax stores its explicit sync refspecs under custom config keys:

```gitconfig
[remote "origin"]
    fetch = +refs/heads/*:refs/remotes/origin/*
    fetch = ^refs/heads/opax/v1

[opax "remote.origin"]
    fetch = +refs/heads/opax/v1:refs/remotes/origin/opax/v1
    fetch = +refs/opax/*:refs/opax/*
    push = +refs/heads/opax/v1:refs/heads/opax/v1
    push = +refs/opax/*:refs/opax/*
```

This keeps plain `git fetch` / `git push` predictable while giving later `opax pull` / `opax push` a canonical source of explicit refspecs.

---

## Specification

### Default Fetch Behavior

`ApplyRefspecPlan` must ensure the selected remote contains a negative fetch refspec excluding the Opax branch:

`^refs/heads/opax/v1`

This relies on Git's negative refspec support for fetch. The exclusion must be added idempotently and must not remove unrelated existing fetch lines.

### Explicit Opax Fetch Refspecs

Stored under `opax.remote.<name>.fetch` (represented in git-config as `[opax "remote.<name>"] fetch = ...`):

- `+refs/heads/opax/v1:refs/remotes/<name>/opax/v1`
- `+refs/opax/*:refs/opax/*`

These are not activated by plain `git fetch`; later Opax commands will use them explicitly.

### Explicit Opax Push Refspecs

Stored under `opax.remote.<name>.push`:

- `+refs/heads/opax/v1:refs/heads/opax/v1`
- `+refs/opax/*:refs/opax/*`

Phase 0 must **not** write `remote.<name>.push` for Opax refs. That would change plain `git push` behavior.

### Multi-Remote Behavior

This feature works per remote. It does not assume `origin` beyond convenience. `opax init` later decides which remote(s) to configure.

### Idempotency

Applying the same plan multiple times must not duplicate fetch exclusions or Opax explicit refspecs.

---

## Edge Cases

- **Remote missing** - return a clear error; do not create phantom remotes
- **Existing negative fetch refspec already present** - no duplicate entry
- **User already has custom fetch lines** - preserve them untouched
- **Multiple remotes configured** - plans are independent per remote
- **Git version lacks negative fetch refspec support** - surface a compatibility error during apply, not later during fetch

---

## Acceptance Criteria

- `BuildRefspecPlan` generates the default exclusion plus explicit Opax fetch/push refspecs for a given remote
- `ApplyRefspecPlan` adds `^refs/heads/opax/v1` to `remote.<name>.fetch` idempotently
- `ApplyRefspecPlan` stores explicit Opax fetch and push refspecs under Opax-owned config keys
- `ApplyRefspecPlan` preserves unrelated user remote config
- The feature does not modify `remote.<name>.push` in Phase 0
- Reapplying the same plan does not duplicate config entries

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestBuildRefspecPlan` | Generated values | Correct fetch exclusion and explicit refspecs |
| `TestApplyRefspecPlanAddsNegativeFetch` | Default fetch stealth | `remote.<name>.fetch` contains `^refs/heads/opax/v1` |
| `TestApplyRefspecPlanStoresOpaxFetchPush` | Explicit sync config | Opax config keys contain expected refspecs |
| `TestApplyRefspecPlanIdempotent` | Repeat safety | No duplicate config entries |
| `TestApplyRefspecPlanPreservesExistingConfig` | User config safety | Unrelated fetch/push lines untouched |
| `TestApplyRefspecPlanMissingRemote` | Remote validation | Clear error |
