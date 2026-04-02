# FEAT-0011 - Refspec Configuration

**Epic:** [EPIC-0001 - Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
**Status:** Completed
**Last synced:** 2026-04-02
**Dependencies:** FEAT-0005 (Repo discovery), FEAT-0006 (Opax branch identity)
**Dependents:** E9 `opax init`, future `opax pull` / `opax push`, E11 hook/setup validation

---

## Problem

The roadmap and product docs want two things at once:

- Opax data should not inflate normal code fetch/push behavior
- Opax branch and notes refs still need a reproducible sync story

Those goals are compatible, but only if the refspec design is conservative. If `opax init` mutates standard push behavior carelessly, it can make plain `git push` start sending Opax refs unexpectedly. That would violate default-sync isolation and surprise users.

---

## Terminology

FEAT-0011 standardizes on **default-sync isolation**:

- plain `git fetch` / `git push` remain code-centric
- Opax sync refs are configured separately and used only by explicit Opax sync flows

This replaces the ambiguous phrase "stealth default" for this feature contract.

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
    OpaxFetch              []string
    OpaxPush               []string
}

type RefspecViolationCode string

const (
    RefspecViolationMissingDefaultFetchExclusion RefspecViolationCode = "missing_default_fetch_exclusion"
    RefspecViolationDefaultFetchMatchesOpaxRefs  RefspecViolationCode = "default_fetch_matches_opax_refs"
    RefspecViolationRemotePushMirrorEnabled      RefspecViolationCode = "remote_push_mirror_enabled"
    RefspecViolationOpaxRefsInRemotePush         RefspecViolationCode = "opax_refs_in_remote_push"
    RefspecViolationInvalidOpaxManagedConfig     RefspecViolationCode = "invalid_opax_managed_config"
)

type RefspecState struct {
    DefaultFetchExclusionPresent bool
    DefaultSyncIsolationEnforced bool
    Violations                   []RefspecViolationCode
    OpaxFetch                    []string
    OpaxPush                     []string
}

var (
    ErrRemoteNameInvalid            = errors.New("git: invalid remote name")
    ErrRemoteMissing                = errors.New("git: remote not found")
    ErrDefaultSyncIsolationViolation = errors.New("git: default-sync isolation violated")
    ErrInvalidRefspecConfig         = errors.New("git: invalid refspec config")
)

func BuildRefspecPlan(remote string) (*RefspecPlan, error)
func ApplyRefspecPlan(ctx *RepoContext, remote string, plan *RefspecPlan) (*RefspecState, error)
func ReadRefspecState(ctx *RepoContext, remote string) (*RefspecState, error)
```

### Config Shape

Standard Git config remains responsible for code fetch behavior. Opax stores explicit sync refspecs under custom config keys:

```gitconfig
[remote "origin"]
    fetch = +refs/heads/*:refs/remotes/origin/*
    fetch = ^refs/heads/opax/v1

[opax "remote.origin"]
    fetch = +refs/heads/opax/v1:refs/remotes/origin/opax/v1
    fetch = +refs/opax/*:refs/opax/*
    fetch = +refs/notes/opax/*:refs/notes/opax/*
    push = +refs/heads/opax/v1:refs/heads/opax/v1
    push = +refs/opax/*:refs/opax/*
    push = +refs/notes/opax/*:refs/notes/opax/*
```

---

## Specification

### Remote Name Validation

All entrypoints use one strict shared remote-name validator:

- allowed characters: `A-Za-z0-9._/-`
- disallow leading `-`
- disallow whitespace, control characters, and metacharacters (for example `*`, `?`, `[`)

Remote names with dots/slashes are valid (for example `team.origin`, `team/prod`).

### Build Contract

`BuildRefspecPlan` is a pure planner:

- validates remote name format only
- does not inspect repository state
- returns the canonical plan values

### Apply Flow

`ApplyRefspecPlan` uses a preflight-then-lock flow:

1. Preflight (read-only): validate remote name/plan, enforce local Git version gate, verify remote exists via `git remote get-url <name>`, validate fetch baseline, and reject default-sync isolation violations.
2. Acquire `.git/opax.lock`.
3. Re-run mutable preflight checks under lock (remote exists + isolation guards) to close preflight-to-write races.
4. Apply mutations: update managed default exclusion, reconcile Opax-owned multivars to canonical values, preserve unrelated user config.
5. Read and return final `RefspecState`.

Config operations run with the existing `RepoContext`-bound git backend and write with `git config --local`.

### Default Fetch Behavior

`ApplyRefspecPlan` must ensure the selected remote contains:

- at least one positive branch mapping in `remote.<name>.fetch`
- one managed exclusion: `^refs/heads/opax/v1`

Rules:

- preserve existing order of unrelated `remote.<name>.fetch` entries
- append the exclusion only when missing
- do not synthesize default branch mappings when baseline is missing
- do not normalize/rewrite unrelated user fetch lines
- reject fetch refspecs that still reach `refs/opax/*`, `refs/notes/opax/*`, or other broad `refs/*` coverage after the managed exclusion is considered
- duplicate managed exclusions are invalid config (`ErrInvalidRefspecConfig`)

### Explicit Opax Fetch Refspecs

Stored under `opax.remote.<name>.fetch`:

- `+refs/heads/opax/v1:refs/remotes/<name>/opax/v1`
- `+refs/opax/*:refs/opax/*`
- `+refs/notes/opax/*:refs/notes/opax/*`

### Explicit Opax Push Refspecs

Stored under `opax.remote.<name>.push`:

- `+refs/heads/opax/v1:refs/heads/opax/v1`
- `+refs/opax/*:refs/opax/*`
- `+refs/notes/opax/*:refs/notes/opax/*`

### Opax-Owned Reconciliation

`opax.remote.<name>.fetch` and `opax.remote.<name>.push` are Opax-owned multivars.

- `ApplyRefspecPlan` reconciles these to canonical values by clear-and-rewrite (`--unset-all` then `--add` canonical lines)
- recognized non-canonical Opax-managed multivars are auto-healed
- unknown/malformed Opax-managed fetch/push entries are invalid config (`ErrInvalidRefspecConfig`)
- unknown non-fetch/push keys in `[opax "remote.<name>"]` are ignored

### Default-Sync Isolation Guardrails

`ApplyRefspecPlan` must fail before writes if plain Git defaults still have an Opax delivery path.

- return `ErrDefaultSyncIsolationViolation`
- include concrete offending values in error context (sanitized and bounded)
- do not mutate `remote.<name>.push` automatically in Phase 0
- reject `remote.<name>.mirror=true`
- reject default fetch refspecs that still match Opax refs after the managed branch exclusion would be applied

### Read Contract

`ReadRefspecState` is the only read API for FEAT-0011 (clean cutover; `ReadRefspecPlan` is removed).

- reports `DefaultFetchExclusionPresent`
- reports derived `DefaultSyncIsolationEnforced` (`true` only when the managed exclusion is present exactly once, push mirror is off, and plain fetch/push have no Opax delivery path)
- reports stable machine-readable violation codes
- canonicalizes returned Opax fetch/push order to the canonical plan order
- fails closed on invalid Opax-managed fetch/push config

### Multi-Remote Behavior

This feature works per remote. It does not assume `origin` beyond convenience. `opax init` later decides which remote(s) to configure.

### Idempotency

Apply semantics are **converge-on-retry**:

- partial writes are tolerated
- rerunning converges to canonical state
- repeated apply does not duplicate managed entries

---

## Edge Cases

- **Remote missing** - return `ErrRemoteMissing`; do not create phantom remotes.
- **Remote name invalid** - return `ErrRemoteNameInvalid`; do not probe git config.
- **Git version below supported minimum** - fail before any config write.
- **`remote.<name>.fetch` baseline missing positive branch mapping** - return `ErrInvalidRefspecConfig`.
- **`remote.<name>.fetch` still reaches Opax refs after the managed exclusion** - return `ErrDefaultSyncIsolationViolation` with offending values.
- **`remote.<name>.mirror=true`** - return `ErrDefaultSyncIsolationViolation`.
- **`remote.<name>.push` contains Opax refs** - return `ErrDefaultSyncIsolationViolation` with offending values.
- **Managed exclusion duplicated** - return `ErrInvalidRefspecConfig`; unmanaged fetch duplication remains untouched.
- **Malformed Opax-managed fetch/push entries** - fail closed with `ErrInvalidRefspecConfig`.
- **Concurrent apply/init flows** - serialize writes with `.git/opax.lock` and re-check mutable guards under lock.
- **Multiple remotes configured** - each remote remains independently planned/applied/read.

---

## Acceptance Criteria

- `BuildRefspecPlan` is pure: validates remote name format only and returns canonical managed values.
- `ApplyRefspecPlan` gates writes on supported git version, existing remote, fetch-baseline validity, and default-sync isolation checks.
- `ApplyRefspecPlan` takes `.git/opax.lock`, revalidates mutable conditions under lock, and applies only repo-wide local config mutations.
- `ApplyRefspecPlan` preserves unrelated user remote config and never writes Opax refs to `remote.<name>.push` or rewrites `remote.<name>.mirror`.
- `ApplyRefspecPlan` reconciles Opax-owned multivars to canonical fetch/push sets and converges on retry without managed duplication.
- `ApplyRefspecPlan` hard-fails with typed errors for isolation/config violations, including push mirror mode and offending fetch/push refspecs that keep plain Git defaults Opax-aware.
- `ReadRefspecState` is the sole read surface and returns canonical Opax sets plus derived isolation boolean and stable violation codes for missing exclusion, fetch leakage, push mirror, and push leakage.
- Reapplying the same plan converges to the same final `RefspecState`.

---

## Test Plan

Multivar assertions use set equality plus explicit invariant checks, not strict line ordering.

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestBuildRefspecPlan` | Generated values | Canonical exclusion and explicit refspecs |
| `TestBuildRefspecPlanRemoteNameValidation` | Planner validation | Invalid names fail with `ErrRemoteNameInvalid` |
| `TestApplyRefspecPlanVersionGateNoWrites` | Fail-fast runtime compatibility | Unsupported git fails before any config mutation |
| `TestApplyRefspecPlanMissingRemote` | Remote validation | Missing remote fails with `ErrRemoteMissing` |
| `TestApplyRefspecPlanRejectsRemotePushMirror` | Push-mirror isolation guard | `remote.<name>.mirror=true` fails with `ErrDefaultSyncIsolationViolation` |
| `TestApplyRefspecPlanRejectsRemotePushOpaxRefs` | Default-sync isolation guard | Fails with `ErrDefaultSyncIsolationViolation` and offending values |
| `TestApplyRefspecPlanRejectsRemoteFetchOpaxRefs` | Fetch-side isolation guard | Broad/default fetch refspecs that still reach Opax refs fail with `ErrDefaultSyncIsolationViolation` |
| `TestApplyRefspecPlanRequiresPositiveFetchBaseline` | Conservative baseline rules | Missing positive fetch mapping fails with `ErrInvalidRefspecConfig` |
| `TestApplyRefspecPlanAddsNegativeFetchPreservingOrder` | Default fetch isolation | Managed exclusion present; unrelated fetch order unchanged |
| `TestApplyRefspecPlanReconcilesOpaxManagedMultivars` | Opax-owned convergence | Opax fetch/push rewritten to exact canonical set |
| `TestApplyRefspecPlanIdempotentConverges` | Repeat safety | Reapply yields same canonical state with no managed duplicates |
| `TestApplyRefspecPlanLocksAndRechecks` | Concurrency safety | Writes serialized and mutable checks revalidated under lock |
| `TestReadRefspecStateCanonicalizesAndReportsViolations` | Read contract | Canonical Opax ordering, boolean isolation flag, stable machine codes |
| `TestReadRefspecStateReportsRemotePushMirrorViolation` | Read-side mirror reporting | Push mirror mode is surfaced as a stable violation code and disables isolation |
| `TestReadRefspecStateRejectsInvalidManagedEntries` | Fail-closed read behavior | Unknown/malformed Opax-managed fetch/push entries fail with typed error |
