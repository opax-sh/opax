# Opax — Product Requirements Document

**Version:** 1.0.0  
**Date:** March 16, 2026
**Status:** Architecture & Planning
**Companion Specs:** Git Data Spec · Privacy & Security · Compliance Framework · Storage & Scaling

---

## Vision

Opax is the structured recording layer for agent work, built on git.

### The problems

1. **Co-ordination** – Product development increasingly involves multiple agents working across platforms, models, and sessions, often over days or weeks. There is no standard way to share state between them. A conversation in Claude about authentication architecture exists only in that session. The Codex session that implements it starts with no knowledge of the discussion, and the Gemini session that writes tests starts from scratch again. Context doesn't flow between tools, sessions don't persist beyond their runtime, and handoffs between agents or platforms are entirely manual. Inter-session coordination (the connective tissue between discrete agent invocations) has no established infrastructure.
2. **Observability** – There is no structured record of agent activity across a project. Teams have no queryable way to determine which agent wrote which code, what decisions led to a particular implementation, what human review occurred, or how a line of production code traces back to the conversation that produced it. For teams in regulated industries, this is becoming a compliance requirement. The EU AI Act, NIST AI RMF, and an growing array of state-level laws require demonstrable audit trails for AI-assisted development. Current approaches treat compliance evidence as a separate deliverable rather than a natural byproduct of the development process.
3. **Ownership** – Agent memory, orchestration, evaluation, and observability tools each store data in their own proprietary formats and backends. Context lives in vector databases we don't control, workflow state lives in vendor runtimes, and eval results live in SaaS dashboards. None of it is inspectable with standard tools, portable across providers, or integrated with the development workflow developers already use. The data your agents produce is scattered across services rather than co-located with the code it produced.

How are we going to solve this? Turns out, we already did. Thirty years ago. With git.

Git gives us:

1. **Durability and coordination** – Every change is stored as immutable, content-addressed history. Repositories replicate across machines without centralized infrastructure. Any collaborator can clone the full record, work independently, and push their contributions back. Git already solves the problem of coordinating distributed work on a shared codebase. The same properties apply to any structured data stored alongside it.
2. **Observability by default** – Every object is cryptographically linked to its parent. History is append-only and tamper-evident. The full provenance chain of any change is preserved from the moment it enters the repository. These properties make git a natural audit log – not because it was designed for compliance, but because immutable, content-addressed history is exactly what compliance requires.
3. **Openness and portabilit –** Git is not owned by any vendor, not locked to any platform, and not going away. It is the one piece of infrastructure present in every software project. Data stored as git objects is inspectable with standard tools, portable across any hosting provider, and readable by anything that speaks the protocol.

### What opax adds

Opax doesn't replace git, it defines an open specification for how agent data is stored as standard git objects, provides a TypeScript SDK that makes reading and writing that data ergonomic, and maintains a local SQLite database as a materialized view for fast queries. Any tool that can read git can read opax. Any tool that can write git can extend the platform.

---

## What Opax Is

A **data specification** for storing structured agent activity data as git objects, an **SDK** that makes reading and writing that data ergonomic, and a **plugin system** that allows extensions to add capabilities like cross-platform memory, workflow sequencing, evaluations, and other tool adapters.

## What Opax Is Not

Opax is not an orchestration engine. It does not compete with LangGraph, Temporal, or Genkit for real-time agent coordination. It is not responsible for how agents do their work, how users review agent output, or how sessions are managed within a single agent platform. It does not run daemons, watchers, or persistent background processes locally. It does not manage containers, cloud sandboxes, or CI pipelines.

Opax is the durable state layer that orchestration tools write to, the continuity layer that agents read from, and the audit trail that compliance tools inspect.

**Strategic framing:** Opax is to GitHub what Datadog is to AWS CloudWatch: a specialized projection layer, not a hosting platform. The moment we start building code viewers, pull request reviews, or template registries, we've accidentally started building GitHub. Resist that pull. Opax renders the data GitHub ignores (notes, orphan branches, trailers), not the data GitHub already handles well.

---

## Architecture: Core + Plugins

### Core (open-source)

The core is deliberately thin. It owns data infrastructure; all domain logic lives in plugins.

1. **Git Data Spec** — A published specification defining naming conventions, schemas, and semantics for storing agent data as git objects. All data lives under the `opax/` namespace. It uses five git primitives: orphan branches, commit trailers, git notes, custom refs, and annotated tags. See companion: _Data Spec_.
2. **SDK (`@opax/sdk`)** — A TypeScript library providing typed read/write access to spec-conformant data, git hook event capture, plugin loading, and storage lifecycle management. Shells out to `git` CLI for writes (maximum compatibility), uses direct `.git/` access for reads (performance). Concurrency via `.git/opax.lock` for shared refs; per-branch writes don't conflict.
3. **SQLite Materialized View** — A local database at `.git/opax/opax.db` derived from git state. Provides FTS5 full-text search, structured queries, and typed views over all Opax data. Always rebuildable from git via `opax db rebuild`. Zero-infrastructure — single file, no server, ships with the SDK. WAL mode for concurrent reads. See companion: _Storage Spec_.
4. **Storage Lifecycle** — Compaction, retention policies, garbage collection, and size management. Tiered retention: individual session branches → daily summary branches → archive repos. See companion: _Storage Spec_.
5. **Privacy Pipeline** — Layered content processing: secret scrubbing (Phase 0) → encryption at rest (Phase 1). Scrubbing always precedes encryption — secrets are never stored even in encrypted form. `PrivacyMetadata` type on all artifacts from Phase 0 to enable Phase 1 without rearchitecting. See companion: _Privacy & Security Spec_.

### First-Party Plugins (open-source, replaceable)

1. **Memory (**`@opax/plugin-memory`**)** — Cross-platform agent context persistence. Stores context artifacts and session archives. Exposes MCP tools and skills for any agent platform to persist and query context.
2. **Workflows (**`@opax/plugin-workflows`**)** — Simple DAG-based stage sequencing with git-event triggers and human approval gates. YAML workflow format is owned by this plugin, not the core spec. Deliberately simple: defers complex coordination to specialized tools. Can optionally fire-and-forget launch agents for stages, or just track state as external tools do the work.
3. **Evals (**`@opax/plugin-evals`**)** — Structured evaluation scoring attached as git notes to agent-produced commits. LLM-as-judge and custom eval criteria.
4. **Executors (**`@opax/executor-`**\*)** — Pluggable backends (local process, Docker, E2B, GitHub Actions) that run workflow stages in sandboxed environments. Used by the workflows plugin to dispatch work. Removed from core; reintroduced as plugins because the workflows plugin needs a place to put work.
5. **Adapters (**`@opax/adapter-`**\*)** — Bridges that normalize third-party tool data (LangGraph, Temporal, GitHub Actions, various agent platforms) into Opax's git format. Positions opax as the Rosetta Stone for agent data: every tool that writes data in its own format can have an adapter that translates it into the Opax spec. Adapters are the primary mechanism for ecosystem expansion.

### Clients

**CLI (`opax`)** — Primary human interface. Core provides base commands (`opax init`, `opax db`, `opax storage`); plugins register subcommands (`opax context`, `opax workflow`, `opax eval`).

**MCP Server (**`@opax/mcp`**)** — Exposed by the memory plugin. stdio process, starts when agent platform launches it, stops when session ends. Five tools: save, search, list, get, handoff.

**Studio (`@opax/studio`)** — Web UI for visualizing Opax data. Two deployment modes:

- **Local (free)** –`opax studio` launches a temporary local server (like Supabase Studio or Drizzle Studio). Reads from local SQLite. No daemon — runs only when invoked.
- **Hosted (paid) –** Always-on dashboard with Postgres backend, cross-repo views, notifications, cron triggers, team features. See _Commercial Model_ below.

Every first-party plugin ships with a Studio panel. Memory → context timeline and session browser. Workflows → DAG visualizer and gate approval UI. Evals → score dashboards and trend charts. Executors → execution log viewers. The more plugins you use, the richer Studio becomes.

### Plugin System

Plugins implement a common `OpaxPlugin` interface that provides: namespace registration (claiming a path prefix under `opax/`), SQLite schema extensions (tables and views materialized from their git data), CLI subcommand registration, MCP tool registration, and Studio panel registration. The plugin loading system is the architectural centerpiece — it enables the "build our own plugins + adapt every external tool" strategy.

---

## Design Principles

- **Git as state –** Opax's defensible position is as a projection/query layer over git. Conflating this with orchestration or hosting risks building a GitHub competitor by accident.
- **Fire-and-forget –** No daemon or watcher locally. All state advances reactively on user triggers, git hooks, external webhooks, or cron. Hooks fire asynchronously and return immediately, adding zero perceptible latency to git operations.
- **Event sourcing –** Git serves as the write-ahead log and distribution mechanism. SQLite serves as the materialized view optimized for queries. The database is always derivable from git. `git clone` + `opax init` always works.
- **Scrubbing before encryption** – Secrets must never be stored even in encrypted form. The privacy pipeline order is non-negotiable.
- **Layered metadata** – A single `OA-Session` trailer on each commit provides a tamper-proof link to the session archive. Detailed metadata lives in git notes, which are invisible by default and do not modify the commit hash. Teams that need stronger audit guarantees can enable signed commits on Opax data branches, pin archive hashes in trailers, or enforce branch protection rules on `opax/`\* refs. Opax provides the tooling for each layer. Teams choose what they need.
- **Plugin ownership –** The workflows YAML format belongs to the plugin, not the spec. Keeps the core thin and the plugin replaceable. Same principle applies to eval criteria, adapter schemas, and executor configs.
- **Open spec first –** The git data format is implementable by third-party tools without the Opax SDK. This is the key ecosystem and defensibility lever: network effects come from the spec, not the runtime.
- **Phased infrastructure –** SQLite locally (zero friction), Postgres only at the web control plane where its strengths (JSONB/GIN indexes, `LISTEN`/`NOTIFY`, `pgvector`, concurrent writes) are warranted.

---

## Use Cases

### Scenario 1: User → Agent (Cross-Platform Memory)

Developer explores auth architecture in Claude web. Agent calls `save` via MCP to persist the conversation. Developer opens Claude Code in the same repo. Agent calls `search` and retrieves the full architecture discussion. Developer opens Cursor to write tests. Same search, same context. Developer runs `git push`. All context is shared with teammates.

No manual handoff documents and no copy-paste. Context is stored as git objects in the same repository the code lives in.

### Scenario 2: Agent → Agent (Workflow Sequencing)

Team defines a workflow: implement → review → test → merge, with human gates. Agent A implements on a branch, commits. The post-commit hook fires, Opax evaluates triggers, and transitions the workflow state to "review." Agent B is launched (or a human is notified) for review. On approval, tests run in Docker via an executor plugin. Results are written as git notes. On pass, merge happens (with or without a final human gate).

Opax advances state reactively. The workflow state machine only moves forward when something external happens. No process shepherds the workflow. Git is the state store, hooks are the event mechanism.

### Scenario 3: Agent → Human (Audit & Compliance)

Every agent-produced commit is annotated with structured metadata via git notes: which agent produced it, which workflow stage, how long the session took. Review assessments, test results, and eval scores are also notes. The complete provenance chain from initial prompt to production code is captured as immutable, cryptographically-linked git history.

This maps directly to EU AI Act Article 12 (record-keeping), Article 14 (human oversight via gates), NIST AI RMF, and ISO 42001 requirements. Developers don't do extra compliance work — the audit trail is a natural byproduct of using the product. See companion: _Compliance Framework_.

---

## Competitive Position

No existing tool combines cross-platform agent memory, git-native audit trails, declarative workflow sequencing, and pluggable execution in a single open data format.

**Vs. Mem0/Letta/Zep:** These use vector databases or proprietary storage for agent memory. Opax's data is inspectable with standard git commands, portable across hosting platforms, and distributed via `git push`. Cross-platform by design, not locked to one provider.

**Vs. LangGraph/Temporal/Genkit:** These are real-time intra-session orchestration engines. Opax handles inter-session orchestration: the durable state between sessions. They're complementary; Opax's adapter plugins normalize their output into the git data layer.

**Vs. Act/Dagger:** These run CI pipelines locally. Opax's executor plugins dispatch work to these (and other) backends. Different layer.

**Vs. Braintrust/Langfuse:** These are production AI observability platforms for teams shipping AI products to end users. Opax operates at the development layer: agent sessions, not production API traces. Different scale and data model. Opax is the data layer beneath; observability platforms consume Opax data, not compete with it.

**Key differentiators:** Git as the data layer (inspectable, portable, distributed). Open specification (ecosystem API, not proprietary format). Compliance-ready by design (cryptographic integrity, immutable history). Provider-agnostic (works across Claude, Codex, ChatGPT, Gemini, OLLAMA, mobile).

**Biggest threat:** GitHub Agentic Workflows + GitHub MCP Registry in the next 12 months. Mitigation: ship fast, establish the spec before vendors move. The open format creates switching costs: ecosystem tools built on the format persist even if vendors offer alternatives.

---

## Commercial Model

### Open-Source Core (Apache 2.0)

The SDK, all first-party plugins, the CLI, the data spec, and Studio in local mode are open-source.

### Hosted Tier (Paid)

The free/paid boundary maps onto the local/hosted boundary, which maps onto the no-daemon principle. Every paid feature requires persistent infrastructure that's structurally impossible to deliver locally.

| Capability      | Local (Free)              | Hosted (Paid)                           |
| --------------- | ------------------------- | --------------------------------------- |
| Data storage    | Git + local SQLite        | Git + hosted Postgres                   |
| Search & query  | FTS5, single-repo         | Full Postgres FTS, cross-repo           |
| Web UI          | `opax studio` (temporary) | Always-on dashboard                     |
| Notifications   | None (no daemon)          | Slack, email, webhooks                  |
| Cron triggers   | None (no daemon)          | Scheduled workflow dispatch             |
| Team dashboards | Single-repo               | Cross-repo, cross-team                  |
| Monitoring      | `opax status`             | Anomaly detection, trend alerts         |
| Retention       | Limited by git/disk       | Extended hosted storage + archive repos |
| Access controls | Git repo permissions      | SSO, RBAC, team workspaces              |

The Postgres layer at the hosted tier uses a `StorageBackend` interface so the SDK's public API remains unchanged. The upgrade path from local to hosted is configuration, not migration — the SQLite-backed local mode and the Postgres-backed hosted mode share the same materialization logic.

**Phase 2 control plane note:** Andrew Nesbitt's `omni_git`/`gitgres` project (git smart HTTP protocol as a Postgres extension) is worth evaluating for the hosted tier. The git wire protocol as a sync mechanism between canonical git repos and Postgres query tables could be more elegant than a custom sync pipeline. Not a Phase 0 concern.

---

## Development Phases

### Phase 0: Core SDK + Memory Plugin + MCP Server

Cross-platform agent memory that works today. **This is the wedge.**

**Deliverables:**

- `@opax/sdk` — core data operations, SQLite materialization, plugin system, git hook management, storage lifecycle, privacy pipeline (scrubbing only).
- `@opax/plugin-memory` — context and session storage, search, handoff generation.
- `@opax/mcp` — MCP server backed by the memory plugin. Five tools: save, search, list, get, handoff.
- Git Data Spec v1.0 — core conventions, namespace structure, extension mechanism.
- Setup guides for Claude Projects, Claude Code, Codex, Aider, Goose.

**Exit criteria:** Developer configures MCP in Claude web + Claude Code, persists architectural discussion, retrieves it in CLI session with zero manual handoff. SQLite materialization and FTS5 search work. Storage compaction runs. Secret scrubbing catches API keys in session transcripts.

### Phase 1: Workflows Plugin + Evals Plugin + Encryption

Git-event-driven workflow sequencing and structured evaluation scoring.

**Deliverables:**

- `@opax/plugin-workflows` — YAML parsing/validation, trigger evaluation, stage dispatch, gate management, git hook integration.
- `@opax/plugin-evals` — eval scoring, LLM-as-judge framework, git note attachment.
- `@opax/plugin-executor-local` — local process executor.
- `@opax/plugin-executor-docker` — Docker executor.
- Privacy Phase 1: encryption at rest via `age`, per-tier recipient key sets.
- Commit metadata via git notes (not trailers by default).
- CLI extensions for workflow management (`opax workflow start/status/approve/reject`).

**Exit criteria:** 3-stage workflow (implement → test → merge) runs end-to-end, triggered by git commits, with a human gate. Test stage runs in Docker. Results visible as git notes. Encrypted content readable only by authorized recipients.

### Phase 2: Remote Execution + Web Control Plane

Remote executors and the first rich UI. Postgres enters the stack at this layer.

**Deliverables:**

- `@opax/plugin-executor-e2b` — E2B sandbox executor.
- `@opax/plugin-executor-github-actions` — GitHub Actions executor.
- `@opax/studio` — local and hosted modes. Hosted mode backed by Postgres.
- First adapter plugins (LangGraph, GitHub Actions data normalization).
- Webhook notifications for gates and workflow completion.
- `StorageBackend` interface with Postgres implementation.

**Exit criteria:** Same workflow runs with test stage on E2B. Studio shows live workflow progress. Gate approved from Studio. Adapter normalizes GitHub Actions run data into Opax format.

### Phase 3: Ecosystem + Compliance + Polish

Third-party integration, compliance tooling, and community.

**Deliverables:**

- Git Data Spec v2.0 with extension guidelines.
- Compliance reporting module (EU AI Act, NIST AI RMF, ISO 42001 mapping).
- Additional adapter plugins (Temporal, Braintrust, Langfuse).
- Semantic search (local embeddings) for context queries.
- Plugin registry or npm discovery mechanism.
- `opax doctor` diagnostic command.
- Team features (shared workflow configs, notification channels).

**Exit criteria:** Third-party tool reads session archives and writes eval scores as git notes using only the published spec, without importing the SDK. Compliance report generates evidence package for EU AI Act Article 12 from existing Opax data.

---

## Key Decisions Log

Accumulated architectural decisions from design conversations, in chronological order. Each is final unless explicitly revisited.

1. **Name:** Open Axiom (Opax). CLI: `opax`. Namespace: `opax/`. npm: `@opax`. GitHub: `opax-sh`. Domains: `opax.dev`, `opax.sh`.
2. **Language:** Entire stack in TypeScript for Phase 0. Rust deferred to future terminal app. `GitDataStore` abstraction as future Rust extraction boundary if perf requires it.
3. **Config format:** YAML with strict JSON Schema validation. Not TOML (ecosystem unfamiliarity), not Markdown (insufficient structure).
4. **Storage pattern:** Event sourcing / CQRS. Git = WAL + distribution. SQLite = materialized view. Database at `.git/opax/opax.db`, always rebuildable.
5. **Phased databases:** SQLite locally (Phase 0). Postgres at hosted control plane only (Phase 2). Abstracted behind `StorageBackend` interface.
6. **Architecture:** Thin core + plugin system. Not four co-equal layers. Core owns data infrastructure; all domain logic lives in plugins.
7. **Orchestration positioning:** Opax handles inter-session orchestration (durable state between sessions). Intra-session orchestration (LangGraph's domain) is out of scope.
8. **Plugin naming:** "Workflows" not "orchestration" or "dispatch." The name avoids undermining the positioning.
9. **No daemon locally.** Fire-and-forget. Hooks fire async. No persistent process. Every feature requiring a persistent process is on the paid hosted tier.
10. **Notes over trailers by default.** Notes don't modify commit hashes. Trailers are opt-in. Avoids the "Claude signature" backlash problem.
11. **Privacy pipeline order:** Scrub → encrypt. Non-negotiable. `PrivacyMetadata` type ships in Phase 0 to scaffold Phase 1 encryption.
12. **Encryption tool:** `age`. Per-tier recipient key sets. Hybrid approach: encrypt content files, leave `metadata.json` plaintext to preserve git delta compression.
13. **Execution environments:** Removed from core, reintroduced as executor plugins. The workflows plugin dispatches to them; the core doesn't know or care.
14. **Terminal app:** Deferred. Stack leaning Rust + Dioxus + libghostty-vt via zmx. Not a Phase 0–2 concern.
15. **MCP server as wedge.** Ships first, standalone. Lowest-friction entry point. Validated by MCP ecosystem growth (5,800+ servers, 97M monthly SDK downloads) and the gap between commoditized thin-wrapper servers and a properly engineered git-backed memory server.
16. **Compliance as natural byproduct.** Session archives = Article 12 record-keeping. Workflow gates = Article 14 human oversight. Git integrity = tamper-evidence. Don't bolt on a compliance layer; the data model serves compliance structurally.
17. **Retention tensions.** PRD compaction (30d individual / 90d summary) conflicts with EU AI Act (system lifetime) and Colorado (3 years). Compliance mode overrides compaction settings. Addressed in _Storage & Scaling Spec_ and _Compliance Framework_.
18. **Competitive positioning.** Opax is the data layer beneath observability platforms (Braintrust, Langfuse), not a direct competitor. Ship the spec, make evals expressive enough for them to consume Opax data. Expand upward only after the spec wins.

---

## Open Questions

1. **Hook conflict.** User has existing git hooks — merge with theirs, use a hooks directory, or use a hook manager like husky? `core.hooksPath` and Git 2.36+ hooks directory are promising. Needs testing.
2. **Context deduplication.** Same conversation persisted twice from different sessions — merge, deduplicate, or leave both? Leaning toward leaving both with a dedup view in Studio.
3. **Cross-repo context.** Developer works on a monorepo but also has context in a separate microservice repo. Locally, SQLite is scoped to one repo. Hosted tier can materialize across repos. Is there a local solution worth building?
4. **Search quality.** FTS5 keyword search is v1. When to add local embeddings for semantic search? Which model? Phase 3 concern, but design the search interface to accommodate it.
5. **Plugin discovery.** Registry (like npm but scoped to Opax) or just npm search with conventional naming (`@opax/plugin-`_, `opax-plugin-`_)?
6. **Sync latency.** After `git pull` brings in 500 new records, the next SDK read triggers a sync that could take seconds. Eager (background on pull), lazy (on first read), or explicit (`opax db sync`)? Leaning lazy with stale-data indicator.
7. **Branch model.** Per-record orphan branches (conceptually clean, scales poorly) vs. consolidated branches per data type (more complex writes, better scaling). SQLite is the read path either way. Leaning consolidated given scaling math.
8. **Session capture completeness.** Hook-based capture gets baseline metadata (commits, timing, diffs) without agent cooperation. Full transcripts require MCP integration or agent wrapping (`opax run claude-code`). What's the minimum viable capture for the wedge to work?
9. **Beads coexistence.** Beads (Steve Yegge) validates "agent memory in git" thesis but uses Dolt, not standard git. Adjacent, not threatening. Worth tracking. Their `--stealth` mode (use locally without polluting shared repo) is a UX pattern worth stealing.

---

## References

| Document                  | Scope                                                                                        |
| ------------------------- | -------------------------------------------------------------------------------------------- |
| _Git Data Spec_           | Namespace conventions, git primitives, schemas, SQLite materialization, plugin registration  |
| _Privacy & Security Spec_ | Secret scrubbing pipeline, encryption at rest, PrivacyMetadata, git compression implications |
| _Compliance Framework_    | EU AI Act, NIST AI RMF, ISO 42001, Colorado AI Act mapping, data model additions, retention  |
| _Storage & Scaling Spec_  | Capacity math, branch consolidation, archive repos, StorageBackend interface, compaction     |
