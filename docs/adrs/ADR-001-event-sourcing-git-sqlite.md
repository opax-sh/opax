| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-001 — Event sourcing with git and SQLite

## Status
Accepted

## Context
Opax needs to store structured agent data (sessions, saves, metadata) in a way that is durable, distributable, and queryable. The data model must support full-text search, structured queries, and fast reads while remaining portable across machines via standard git operations.

## Options Considered

### Option A — Git as source of truth, SQLite as materialized query database
- Pros: git provides immutability, distribution, tamper-evidence, and content-addressing for free. SQLite provides FTS5, structured queries, and fast reads locally. The combination gives CQRS semantics without infrastructure.
- Cons: writes require both git and SQLite operations. SQLite must be rebuilt if it drifts.

### Option B — SQLite as primary store
- Pros: simpler write path, faster queries.
- Cons: no distribution without custom sync. No tamper-evidence. Not inspectable with standard tools. Requires backup strategy.

### Option C — Custom database or vector store
- Pros: purpose-built for the domain.
- Cons: another dependency, another format, another sync problem. Loses git's distribution and integrity properties.

## Decision
Option A. Git is the write-ahead log and distribution mechanism. SQLite at `.git/opax/opax.db` is the materialized query database — it handles 100% of reads and search. Always rebuildable from git via `opax db rebuild`. WAL mode for concurrent reads.

Phase 0 uses SQLite only. Postgres enters at the hosted control plane (Phase 2), abstracted behind `StorageBackend` interface. The SDK's public API is unchanged between backends.

## Consequences

### Positive
- `git clone` + `opax init` bootstraps a fully queryable local database from any machine
- Data is inspectable with standard git tools
- No infrastructure beyond git for local use

### Negative
- Write path touches both git and SQLite
- SQLite rebuild can be slow for large histories (mitigated by incremental materialization)

### Follow-up
- `StorageBackend` interface design for Phase 2 Postgres support
