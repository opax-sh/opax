# Opax — Product Overview

**Version:** 3.1.0
**Date:** March 30, 2026
## Vision

Opax is the structured recording, coordination, and product execution layer for agent work, built on git.

### The problems

1. **Coordination** — Multiple agents work across platforms, models, and sessions over days or weeks. There is no standard way to share state between them. A Claude session about auth architecture exists only in that session. The Codex session that implements it starts from scratch. The Gemini session that tests it starts from scratch again. Context doesn't flow between tools, sessions don't persist beyond runtime, and handoffs are manual.

2. **Observability** — There is no structured record of agent activity across a project. Teams can't determine which agent wrote what code, what decisions led to an implementation, or how production code traces back to the conversation that produced it. For regulated industries, this is becoming a legal requirement — the EU AI Act, NIST AI RMF, and state-level laws require audit trails for AI-assisted development. Current approaches treat compliance as a separate deliverable rather than a byproduct of the workflow.

3. **Ownership** — Agent memory, orchestration, and observability tools store data in proprietary formats and backends. Context lives in vector databases you don't control, workflow state in vendor runtimes, eval results in SaaS dashboards. None of it is inspectable with standard tools, portable across providers, or co-located with the code it produced.

4. **Execution management** — Engineering teams increasingly split product intent across issue trackers, docs tools, chat, branches, PRs, and agent runtimes. The plan lives in one system, the work happens in another, and the reasoning behind execution is lost between them. Repo-native teams already manage real delivery in git, but the supporting context is still fragmented.

### Why git

Git already solves these problems for human developers:

- **Coordination** — immutable, content-addressed history that replicates across machines without centralized infrastructure. Any collaborator clones the full record, works independently, pushes back.
- **Observability** — every object cryptographically linked to its parent. History is append-only and tamper-evident. Provenance is preserved from the moment a change enters the repository.
- **Openness** — not owned by any vendor, not locked to any platform. Present in every software project. Data stored as git objects is inspectable, portable, and readable by anything that speaks the protocol.

The same properties apply to agent data stored alongside code.

### What Opax adds

Opax defines an open specification for storing agent data as standard git objects, provides an SDK for reading and writing that data, passive capture that records agent sessions after the fact, and a CLI for querying context. A local SQLite database provides fast queries over the git data.

Memory and orchestration are combined because neither is useful alone. Memory without orchestration means agents remember but work in isolation. Orchestration without memory means agents coordinate but start blind. Combined: agents coordinate and learn from each other's sessions.

Git already provides orchestration primitives — branches are work units, commits are stage gates, hooks are transitions, PRs are review gates, merge is delivery. Multiple agents on a repo is the same problem as multiple developers on a repo. Opax makes these primitives accessible for agent work by adding structured memory and context passing between stages.

For software teams, the long-term user-facing surface is repo-native product execution. Product intent, scoped docs, task state, agent sessions, branches, reviews, and verification records stay linked in one git-backed system. Memory and orchestration remain the substrate; product execution is the layer that makes them useful for day-to-day delivery.

---

## Scope

Opax is a **data spec** for agent data as git objects, an **SDK** for reading and writing that data, a **passive capture engine** for automatic session recording, a **coordination layer** using git's workflow primitives, and a **plugin system** for extensions.

Opax is not an intra-session orchestration engine (LangGraph, Temporal, Genkit). Those handle real-time coordination within a single agent session. Opax handles inter-session coordination: what work happens in what order, passing context between stages, enforcing review gates, recording what happened. Those tools are complementary — adapter plugins normalize their output into the Opax data format.

Opax is not an execution environment. It does not manage containers, sandboxes, or CI pipelines. Execution is pluggable via thin drivers. The orchestrator defines what happens; drivers handle where it runs.

Opax is also not a generic company-wide issue tracker or product workspace. The product-management surface is eng-first and git-first: it owns the path from scoped decision to reviewed code. Systems like Linear or Notion can remain upstream planning or publishing layers, but Opax keeps engineering execution state canonical inside the repository.

---

## Architecture

### Core

The core is thin. It owns data infrastructure; all domain logic lives in plugins.

1. **Git Data Spec** — naming conventions, schemas, and semantics for agent data as git objects. Five git primitives: orphan branches, commit trailers, git notes, custom refs, annotated tags. All data under the `opax/` namespace.
2. **SDK** — typed read/write access to spec-conformant data, hook event capture, plugin loading, storage lifecycle. Go, using a typed native Git backend adapter for production repository/object/ref operations and modernc.org/sqlite for the query database. Single binary, no daemon or extra services beyond a standard Git environment.
3. **SQLite query database** — local database at `.git/opax/opax.db` derived from git state. FTS5 full-text search, structured queries, typed views. Always rebuildable from git via `opax db rebuild`.
4. **Two-tier storage** — metadata and references in git, bulk content (transcripts, diffs, action logs) in content-addressed storage at `.git/opax/content/`, referenced by SHA-256 hash. Tiered retention: hot (same repo) → warm (archive remote) → cold (git bundles).
5. **Passive capture engine** — reads agent-native storage after the fact. Agent-specific readers know where each platform stores sessions (Claude Code's JSONL, Codex session logs). Hooks detect sessions on commit. Zero agent cooperation required.
6. **Hygiene pipeline** — secret detection and scrubbing on all content before storage. Scrubbing precedes any future encryption. Secrets are never stored.

### Plugins

First-party plugins are open-source and replaceable. Each plugin owns its format and schema.

1. **Memory** — session recording and search. CLI (`opax search`, `opax session`) is the primary query interface. MCP server provides read access for platforms without shell access (Claude web, ChatGPT).
2. **Workflows** — DAG-based stage sequencing with git-event triggers and human approval gates. Definitions live in `.opax/workflows/`, versioned alongside code. YAML format is owned by this plugin, not the core spec.
3. **Evals** — thin note format for attaching eval scores to commits as git notes. Not a full eval framework — teams needing that use Braintrust or Langfuse with an Opax adapter.
4. **Executors** — pluggable backends that run workflow stages. Each executor implements a common contract: given a branch, a context bundle (Opax memory), and a task spec, spin up a session and signal completion. New environments need a new driver, not changes to the orchestrator.
5. **Adapters** — bridges that normalize third-party tool data (LangGraph, Temporal, GitHub Actions) into the Opax git format. If a first-party plugin starts feeling like its own product, build an adapter instead.

### Clients

**CLI (`opax`)** — primary interface for humans and agents with shell access. Core provides base commands (`opax init`, `opax db`, `opax storage`); plugins register subcommands (`opax session`, `opax workflow`, `opax eval`).

**MCP Server** — secondary interface for platforms without shell access. Wraps the same SDK operations as the CLI.

**Studio** — web UI for visualizing Opax data. Local mode (`opax studio`) launches a temporary server reading from SQLite. Hosted mode provides an always-on dashboard with Postgres backend, cross-repo views, and team features. Each plugin ships a Studio panel.

### Plugin system

Plugins implement `OpaxPlugin`: namespace registration (path prefix under `opax/`), SQLite schema extensions, CLI subcommand registration, MCP tool registration, and Studio panel registration.

---

## Design Principles

- **Event sourcing** — git is the write-ahead log and distribution mechanism. SQLite is the materialized query database, always derivable from git. `git clone` + `opax init` always works.
- **Commit-anchored** — the primary question is "what context produced this commit?" Saves are created on commit. Session data hangs off the save. Sessions without saves (research, failed attempts) are first-class.
- **Passive capture first** — agents don't need to cooperate with Opax. The system reads agent-native storage after the fact. MCP complements for platforms without shell access.
- **Fire-and-forget** — no daemon or watcher locally. State advances on user triggers, git hooks, or webhooks. Hooks fire asynchronously and return immediately.
- **Git is the host platform** — native Git is the production transport for repo/object/ref semantics in `internal/git`; Go keeps Opax validation rules, deterministic pathing, and typed error contracts.
- **Scrub before encrypt** — secrets are never stored, even encrypted. The hygiene pipeline order is non-negotiable.
- **Layered metadata** — `Opax-Save` trailers on commits provide tamper-proof links. Detailed metadata lives on the Opax branch. Post-commit annotations (test results, reviews, evals) live in git notes.
- **Plugin ownership** — domain formats belong to plugins, not the core spec. Keeps the core thin and plugins replaceable.
- **Open spec** — the git data format is implementable without the Opax SDK. Any tool that reads git can read Opax data.

---

## Use Cases

### Cross-platform session history

Developer uses Claude Code to implement auth. Passive capture records the session on commit. Developer switches to Codex for a follow-up task. `opax search "auth architecture"` retrieves the previous session. Another teammate gets the same results. Session data syncs via `opax push` / `opax pull`.

### Multi-agent workflow

Team defines a workflow: implement → review → test → merge, with human gates. Agent A implements on a feature branch and commits. The commit event triggers the next stage — a review agent dispatched with full context from Agent A's session. The review agent sees not just the diff but the reasoning that produced it. On approval, tests run. Results are written as git notes. On pass, merge.

Each stage gets the previous stage's context from Opax memory. Context flows through git. The workflow advances on git events.

### Repo-native product execution

Team keeps its product intent in repo docs: strategy in `docs/product/`, scoped design in `docs/epics/` and `docs/features/`, task breakdowns in `docs/tasks/`, and execution on branches and PRs. An engineer or agent picks up a scoped task, works with prior session context, and updates the same git-backed record as implementation advances. Another engineer can clone the repo and recover both the plan and the execution trail without opening a separate PM tool.

### Audit trail

Every agent-produced commit carries an `Opax-Save` trailer linking to structured session records. Agent identity, timing, session details, review assessments, test results, and eval scores are all recorded as git objects. The provenance chain from prompt to production code is immutable and cryptographically linked.

This maps to EU AI Act Article 12 (record-keeping), Article 14 (human oversight via gates), NIST AI RMF, and ISO 42001. The audit trail is a byproduct of using the product.

---

## References

| Document              | Location                              | Scope                                                                |
| --------------------- | ------------------------------------- | -------------------------------------------------------------------- |
| Documentation Index   | `docs/index.md`                       | Authoritative current repo state and full docs inventory             |
| Roadmap               | `docs/product/roadmap.md`             | Phased delivery plan, epics, exit criteria                           |
| Git Data Spec         | `docs/product/data-spec.md`           | Namespace conventions, schemas, SQLite materialization                |
| Hygiene Spec          | `docs/product/hygiene.md`             | Secret scrubbing pipeline, config, metadata                          |
| Compliance Framework  | `docs/product/compliance.md`          | EU AI Act, NIST, ISO 42001 mapping, retention                        |
| Storage & Scaling     | `docs/product/storage.md`             | Two-tier storage, archive tiers, StorageBackend interface, compaction |
| Architecture Decisions| `docs/index.md#adrs`                  | ADR inventory and links                                              |
| Repo Structure        | `docs/architecture/repo-structure.md` | Package layout, conventions, build commands                          |
