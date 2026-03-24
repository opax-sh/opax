| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-003 — Single orphan branch for all metadata

## Status
Accepted

## Context
Opax metadata (sessions, saves, plugin data) needs to live in git without polluting the project's working tree or commit history. The data must be distributable via standard git push/pull and mergeable across contributors.

## Options Considered

### Option A — Per-record branches
- Pros: natural isolation between records.
- Cons: ref enumeration becomes slow with thousands of branches. Git hosting UIs become cluttered. Merge strategy unclear.

### Option B — Git notes only
- Pros: built-in git feature, attached to commits.
- Cons: limited structure. Notes are single-ref, making namespaced data awkward. No good story for session data that isn't commit-attached.

### Option C — Single orphan branch with sharded directory layout
- Pros: one ref to sync. Git shares tree objects between commits, delta compression works across full history. Sharded directories (first two chars of ID) prevent single-directory bloat. Excluded from default fetch — invisible unless explicitly synced.
- Cons: all writes serialized to one branch (acceptable in Phase 0 with `.git/opax.lock`).

## Decision
Option C. All Opax metadata lives on `opax/v1`, a single orphan branch with sharded directory structure. Adopted from Entire.io's architecture.

The branch is excluded from default fetch via refspec design — `opax pull` and `opax push` (or explicit git refspecs) sync it explicitly.

## Consequences

### Positive
- Single ref to track and sync
- Git's built-in compression handles deduplication across commits
- Invisible to developers who don't use Opax

### Negative
- Write serialization via lock file (acceptable for Phase 0 volumes)
- Concurrent multi-machine writes require conflict resolution (deferred to hosted tier)

### Follow-up
- Lock file implementation in SDK
- Conflict resolution strategy for hosted/multi-machine scenarios
