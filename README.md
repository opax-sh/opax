# Opax

Opax is the structured recording, coordination, and product execution layer for agent work, built on git.

## Problems

1. Coordination — Multiple agents work across platforms, models, and sessions over days or weeks. There is no standard way to share state between them. A Claude session about auth architecture exists only in that session. The Codex session that implements it starts from scratch. The Gemini session that tests it starts from scratch again. Context doesn't flow between tools, sessions don't persist beyond runtime, and handoffs are manual.
2. Observability — There is no structured record of agent activity across a project. Teams can't determine which agent wrote what code, what decisions led to an implementation, or how production code traces back to the conversation that produced it. For regulated industries, this is becoming a legal requirement — the EU AI Act, NIST AI RMF, and state-level laws require audit trails for AI-assisted development. Current approaches treat compliance as a separate deliverable rather than a byproduct of the workflow.
3. Ownership — Agent memory, orchestration, and observability tools store data in proprietary formats and backends. Context lives in vector databases you don't control, workflow state in vendor runtimes, eval results in SaaS dashboards. None of it is inspectable with standard tools, portable across providers, or co-located with the code it produced.

## Why git?

Git already solves these problems for human developers:

1. Coordination — immutable, content-addressed history that replicates across machines without centralized infrastructure. Any collaborator clones the full record, works independently, pushes back.
2. Observability — every object cryptographically linked to its parent. History is append-only and tamper-evident. Provenance is preserved from the moment a change enters the repository.
3. Openness — not owned by any vendor, not locked to any platform. Present in every software project. Data stored as git objects is inspectable, portable, and readable by anything that speaks the protocol.

The same properties apply to agent data stored alongside code.

## What Opax adds

Opax defines an open specification for storing agent data as standard git objects, provides an SDK for reading and writing that data, passive capture that records agent sessions after the fact, and a CLI for querying context. A local SQLite database provides fast queries over the git data.

Git already provides orchestration primitives — branches are work units, commits are stage gates, hooks are transitions, PRs are review gates, merge is delivery. Multiple agents on a repo is the same problem as multiple developers on a repo. Opax makes these primitives accessible for agent work by adding structured memory and context passing between stages.

For software teams, the long-term user-facing surface is repo-native product execution. Product intent, scoped docs, task state, agent sessions, branches, reviews, and verification records stay linked in one git-backed system. Memory and orchestration remain the substrate; product execution is the layer that makes them useful for day-to-day delivery.

## Scope

Opax is a data spec for agent data as git objects, an SDK for reading and writing that data, a passive capture engine for automatic session recording, a coordination layer using git's workflow primitives, and a plugin system for extensions.

Opax is not an intra-session orchestration engine (LangGraph, Temporal, Genkit). Those handle real-time coordination within a single agent session. Opax handles inter-session coordination: what work happens in what order, passing context between stages, enforcing review gates, recording what happened. Those tools are complementary — adapter plugins normalize their output into the Opax data format.

Opax is not an execution environment. It does not manage containers, sandboxes, or CI pipelines. Execution is pluggable via thin drivers. The orchestrator defines what happens; drivers handle where it runs.

## Project Objective

Modern engineering teams increasingly spread execution across docs, issue trackers, chat, branches, pull requests, and multiple coding agents. Each tool captures part of the story, but the reasoning behind a shipped change is usually scattered or lost.

Opax exists to make the repository the durable system of record for AI-assisted delivery. The goal is to keep scoped intent, execution context, and proof of work connected in one place so another engineer, another agent, or an auditor can recover what happened without reconstructing it from disconnected systems.

## Goals And Non-Goals

### Goals

- Durable cross-session memory for coding work that spans tools, contributors, and time.
- Repo-native provenance from session to commit to review and verification.
- Open, portable records stored in standard git primitives rather than a proprietary backend.
- Product execution anchored in specs, branches, pull requests, and evidence, not just chat transcripts.

### Non-Goals

- A generic company-wide product management suite.
- A full agent runtime or intra-session orchestration framework.
- A broad LLM observability or eval platform competing head-on with specialized tools.

## Current Roadmap Status

Current status as of April 2, 2026:

| Area | Status | Why it matters |
| --- | --- | --- |
| `EPIC-0000` Foundation | Completed | Core dependencies, types, config, and coordination primitives are in place. |
| `EPIC-0001` Git Plumbing Layer | In Progress | The repository-native data and sync boundary is being hardened. |
| `FEAT-0012` Native Git hardening | Completed | Native Git is now the production transport for `internal/git`, which de-risks real repository semantics. |
| `FEAT-0011` Refspec configuration | Backlog | Explicit Opax sync still needs the safe default-sync contract for remote configuration. |
| `FEAT-0013` API/type decoupling | Blocked | Full removal of the frozen `go-git` compatibility surface is intentionally deferred behind a cleaner contract migration. |

The shape of the roadmap is deliberate.

- **Now:** finish the git-plumbing layer so Opax's storage and sync model is operationally trustworthy.
- **Next:** build the Phase 0 loop around capture, materialization, search, memory retrieval, CLI integration, and hooks.
- **Later:** layer workflows, repo-native product execution, remote visibility, and hosted surfaces on top of the same substrate instead of introducing a second source of truth.

For the strategic phase plan, see [`docs/product/roadmap.md`](docs/product/roadmap.md). For authoritative live implementation state, see [`docs/index.md`](docs/index.md).

## Key Decisions

| Decision | Why it matters |
| --- | --- |
| Git + SQLite event-sourced model | Git stays the durable write-ahead log and distribution layer; SQLite provides fast local query and search without introducing hosted infrastructure as a prerequisite. |
| Two-tier storage | Small metadata stays in git, while large transcripts and logs move to content-addressed storage so the repo remains fast and operationally usable. |
| Single orphan branch for Opax metadata | One dedicated branch keeps records portable and syncable without polluting the working tree or normal developer history. |
| Passive capture as the primary recording path | Opax can work with existing coding agents without requiring every vendor to implement a new integration contract first. |
| Commit-anchored provenance | The primary query becomes "what context produced this commit?", which matches how code review, debugging, and audit actually happen. |
| Native Git as the production backend boundary | Repository truth should come from Git-native semantics, especially in linked-worktree and real-world repo topologies where library abstractions drift. |

These decisions are what make the product opinionated instead of generic. They bias toward repo truth, operational reliability, and portability over convenience abstractions that only work inside a single vendor surface.

## Artifacts

- [`docs/product/overview.md`](docs/product/overview.md) captures the product thesis, scope, and target operating model.
- [`docs/product/roadmap.md`](docs/product/roadmap.md) sequences the work by phase so the product story and build order stay aligned.
- `docs/features/` scopes concrete execution slices with acceptance criteria and test plans.
- `docs/adrs/` records the non-obvious tradeoffs and why one option beat the alternatives.
- [`docs/index.md`](docs/index.md) is the current-state ledger for what is complete, in progress, backlog, or blocked.