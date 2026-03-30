# Opax Product Roadmap

**Version:** 1.2.0
**Date:** March 30, 2026
**Companion to:** [Product Overview](overview.md)

## Purpose

This roadmap is the strategic sequencing document for Opax.
It owns phase ordering, epic goals, and upcoming work at a planning level.
For current implementation state, active epics and features, and the complete docs inventory, see [docs/index.md](../index.md).

## Document Ownership

- `docs/index.md` owns current repository state.
- `docs/product/` owns strategy, phase planning, and durable product direction.
- `docs/architecture/` owns package boundaries and structural conventions.
- `docs/epics/`, `docs/features/`, and `docs/adrs/` own scoped design records and acceptance contracts.

## Vision Roadmap

```text
Phase 0: CLI + Passive Capture + Memory          <- distribution (free, open-source)
Phase 1: Workflows + Product Execution + Evals   <- orchestration foundation
Phase 2: Studio + Remote Execution + Postgres    <- first revenue
Phase 3: Ecosystem + Compliance + Adapters       <- ecosystem + enterprise
Phase 4: Intelligence Layer                      <- cross-repo memory and quality signals
Phase 5: Ecosystem & Generalization              <- broader platform surface
```

Memory and orchestration are a single product story. Phase 0 establishes the storage, capture, and query substrate. Phase 1 builds workflow coordination and repo-native product execution on top of that substrate. Later phases expand visibility, integrations, and hosted experiences.

Product management in Opax means git-first product execution for software teams: scoped docs, tasks, branches, sessions, reviews, and verification stay linked in one repository-backed system. It does not mean competing to become the generic cross-functional workspace for every team in the company.

## Phase 0

### Exit Criteria

Developer uses an agent with passive capture enabled, commits work, and gets durable session metadata plus transcript linkage.
`opax search` retrieves relevant sessions from local materialized state.
Another agent on the same repository can recover the same context.
Storage hygiene and compaction run without introducing a second source of truth.

### Sequencing

1. [EPIC-0000: Project Foundation](../epics/EPIC-0000-foundation.md)
   Goal: establish dependencies, canonical types, configuration loading, and administrative coordination primitives.
2. [EPIC-0001: Git Plumbing Layer](../epics/EPIC-0001-git-plumbing-layer.md)
   Goal: discover repositories safely, manage the Opax branch, and support append-only git-backed data operations.
3. EPIC-0002: Content-Addressed Storage
   Goal: store and retrieve bulk content by hash under `.git/opax/content/`.
4. EPIC-0003: Hygiene Pipeline
   Goal: scrub secrets before content reaches git or content-addressed storage.
5. EPIC-0004: Integrated Write Path
   Goal: connect capture, IDs, branch writes, hygiene, and trailer or note linkage into one durable write flow.
6. EPIC-0005: SQLite Materialization
   Goal: derive a local query database from git-backed Opax data.
7. EPIC-0006: Search & Query
   Goal: make `opax search` and `opax session` the first useful user-facing query surface.
8. EPIC-0007: Passive Capture Engine
   Goal: normalize agent-native session storage into a shared Opax session model.
9. EPIC-0008: Memory Plugin
   Goal: ship the first high-value plugin on top of the write and query substrate.
10. EPIC-0009: CLI Integration
    Goal: wire the published CLI shape to real implementations.
11. EPIC-0010: MCP Server
    Goal: expose the same core operations to clients without shell access.
12. EPIC-0011: Hooks & Init Lifecycle
    Goal: initialize repo-local Opax state and advance capture on git events.
13. EPIC-0012: Polish & Validation
    Goal: harden docs, checks, and operational correctness before broader adoption.

### Phase 0 Dependency Shape

```text
E0 -> E1 -> E2 -> E3 -> E4 -> E5 -> E6
                    \-> E7 -> E8
E6, E8 -> E9 -> E10/E11 -> E12
```

The earliest useful demo is manual write plus search.
The full Phase 0 loop requires passive capture, memory retrieval, and CLI wiring.

## Phase 1

Goal: workflows, repo-native product execution primitives, eval hooks, executor drivers, and early compliance support.
Opax moves from durable memory into coordinated multi-stage delivery, where planning docs, task state, agent handoffs, and review gates all run on the same git-backed substrate.

## Phase 2

Goal: studio UI, remote execution, and a hosted query plane.
This phase turns the local-first system into a team-facing product surface derived from repo truth, not a second source of execution state.

## Phase 3

Goal: ecosystem growth, adapter surface, and deeper compliance packaging.
The core remains thin while plugins and integrations broaden adoption.

## Phase 4

Goal: intelligence features that improve retrieval quality, cross-repo context, and evaluation loops.
These features depend on the durable records and workflow events established earlier.

## Phase 5

Goal: generalize the platform to a wider plugin and SDK ecosystem without changing the Phase 0 storage contract.

## Notes

- Feature-level design belongs in `docs/features/`.
- Epic-level scope and shared contracts belong in `docs/epics/`.
- When delivery state changes, update [docs/index.md](../index.md) instead of duplicating status here.
