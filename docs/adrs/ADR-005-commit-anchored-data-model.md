| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-005 — Commit-anchored data model

## Status
Accepted

## Context
Opax needs a primary relationship between sessions (agent conversations) and the codebase. The question is which direction the anchor runs: from sessions to commits, or from commits to sessions.

## Options Considered

### Option A — Session-anchored (sessions own commits)
- Pros: natural grouping — "this session produced these commits."
- Cons: the interesting question for developers and auditors is the reverse: "what context produced this commit?" Session-anchored makes that query a reverse lookup.

### Option B — Commit-anchored (saves created on commit, sessions hang off saves)
- Pros: developers trace backward from code to context. Auditors start from a commit and find the full provenance chain. This is the natural direction — you're looking at code and want to know why it exists.
- Cons: sessions that don't produce commits (research, failed attempts) need separate handling.

## Decision
Option B. The primary question is "what context produced this commit?" Saves are created on commit via `Opax-Save` trailers. Session data hangs off the save.

Sessions and saves are dual-primary — neither is subordinate to the other. Sessions without saves (research, failed attempts, discussions) are first-class citizens with their own lifecycle.

Trailers are the default linkage mechanism — immutable, tamper-evident, cryptographically bound to the commit hash. `prepare-commit-msg` preallocates a save ID and inserts the trailer before the commit is created. Notes are used for post-commit data (test results, reviews, evals) and as a fallback when trailers are disabled.

## Consequences

### Positive
- Natural audit trail from code to context
- Trailers provide tamper-proof linkage without modifying commit content
- Git notes add post-commit annotations without changing commit hashes

### Negative
- Sessions without commits require separate discovery path
- `prepare-commit-msg` hook must be installed for trailer injection

### Follow-up
- Save ID allocation strategy
- `--no-trailers` fallback path via git notes
- Session lifecycle for non-commit-producing sessions
