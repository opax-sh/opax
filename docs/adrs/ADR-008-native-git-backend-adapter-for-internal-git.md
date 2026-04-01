| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-31 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-31 |

# ADR-008 — Native Git backend adapter for `internal/git`

## Status
Accepted

## Context
Opax is git-native, and `internal/git` is the production boundary for repository discovery, ref/object reads, and ref/object writes. The previous boundary (ADR-007) kept go-git as the primary production transport and allowed native Git only for narrow surfaces (`interpret-trailers` and create-if-absent CAS publication).

That model no longer holds. Extension-enabled linked-worktree repositories (`extensions.worktreeConfig=true`) exposed that go-git open/read semantics are not a reliable source of repository truth for Opax production paths. Treating this as isolated feature hardening leaves a fragmented boundary and repeated fallback logic.

We need one production transport model for `internal/git` that tracks Git-native repository semantics while preserving Opax-owned validation rules and typed error contracts.

## Options Considered

### Option A — Keep go-git as the production transport with narrow CLI sidecars
- Pros: minimal migration from prior implementation.
- Cons: keeps a split semantics model and retains known repository-topology risk.

### Option B — Per-feature shell-out migration without a shared adapter
- Pros: fastest incremental local patches.
- Cons: command execution/parsing logic fragments across files and weakens typed boundaries.

### Option C — Native Git as the single production transport behind one typed adapter
- Pros: one consistent production backend, Git-native semantics for repo/object/ref behavior, centralized command/parsing/error handling, preserved exported API and typed contract surface.
- Cons: runtime dependency on standard Git binary and version management discipline.

## Decision
Option C.

`internal/git` uses native Git as the only production transport, implemented via one unexported typed backend adapter that owns:
- command execution
- stdout/stderr capture and parsing
- exit-code translation
- minimum Git version checks
- typed helper surfaces for refs/commits/trees/blobs/CAS updates/trailer parsing

Go remains the contract owner above that transport. Opax rules stay in Go:
- request and identifier validation
- deterministic path/shard derivation
- traversal rejection
- sentinel and malformed-tree detection
- note namespace/payload validation
- `Opax-Save` value validation
- exported typed error semantics

## Consequences

### Positive
- Production behavior aligns with Git-native repository semantics across normal repos and linked worktrees.
- A single adapter boundary avoids ad hoc command orchestration in feature code.
- Exported `internal/git` APIs and typed errors remain stable while backend internals change.

### Negative
- Runtime now explicitly depends on a supported Git binary version.
- Backend tests must validate parsing and failure translation more aggressively than before.

### Follow-up
- Supersede ADR-007 and align feature/epic/product docs to the new boundary.
- Keep read-heavy paths batch-friendly (avoid subprocess-per-object designs).
- Keep go-git optional in tests only (fixtures/cross-checking), not as production semantics oracle.
- Define and enforce one minimum supported Git version in code and CI.

## References
- `docs/features/FEAT-0012-git-boundary-compatibility-hardening.md`
- `docs/epics/EPIC-0001-git-plumbing-layer.md`
- `docs/product/overview.md`
- `docs/adrs/ADR-007-git-cli-boundary-for-hook-semantics.md`
