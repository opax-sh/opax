# Opax — Product Requirements Document

**Version:** 2.0.0
**Date:** March 17, 2026
**Status:** Architecture & Planning
**Note:** Incorporates architectural decisions from design conversations post-v1
**Companion Specs:** Git Data Spec · Hygiene (secret scrubbing) · Compliance Framework · Storage & Scaling

---

## Vision

Opax is the structured recording layer for agent work, built on git.

### The problems

1. **Co-ordination** – Product development increasingly involves multiple agents working across platforms, models, and sessions, often over days or weeks. There is no standard way to share state between them. A conversation in Claude about authentication architecture exists only in that session. The Codex session that implements it starts with no knowledge of the discussion, and the Gemini session that writes tests starts from scratch again. Context doesn't flow between tools, sessions don't persist beyond their runtime, and handoffs between agents or platforms are entirely manual. Inter-session coordination (the connective tissue between discrete agent invocations) has no established infrastructure.
2. **Observability** – There is no structured record of agent activity across a project. Teams have no queryable way to determine which agent wrote what code, what decisions led to a particular implementation, what human review occurred, or how a line of production code traces back to the conversation that produced it. For teams in regulated industries, this is becoming a compliance requirement. The EU AI Act, NIST AI RMF, and an growing array of state-level laws require demonstrable audit trails for AI-assisted development. Current approaches treat compliance evidence as a separate deliverable rather than a natural byproduct of the development process.
3. **Ownership** – Agent memory, orchestration, evaluation, and observability tools each store data in their own proprietary formats and backends. Context lives in vector databases we don't control, workflow state lives in vendor runtimes, and eval results live in SaaS dashboards. None of it is inspectable with standard tools, portable across providers, or integrated with the development workflow developers already use. The data your agents produce is scattered across services rather than co-located with the code it produced.

How are we going to solve this? Turns out, we already did: thirty years ago, with git.

Git gives us:

1. **Durability and coordination** – Every change is stored as immutable, content-addressed history. Repositories replicate across machines without centralized infrastructure. Any collaborator can clone the full record, work independently, and push their contributions back. Git already solves the problem of coordinating distributed work on a shared codebase. The same properties apply to any structured data stored alongside it.
2. **Observability by default** – Every object is cryptographically linked to its parent. History is append-only and tamper-evident. The full provenance chain of any change is preserved from the moment it enters the repository. These properties make git a natural audit log – not because it was designed for compliance, but because immutable, content-addressed history is exactly what compliance requires.
3. **Openness and portability –** Git is not owned by any vendor, not locked to any platform, and not going away. It is the one piece of infrastructure present in every software project. Data stored as git objects is inspectable with standard tools, portable across any hosting provider, and readable by anything that speaks the protocol.

### What Opax adds

Opax doesn't replace git. It defines an open specification for how agent data is stored as standard git objects, provides an SDK (Go) that makes reading and writing that data ergonomic, passive session capture that records agent activity after the fact, a CLI for querying context, and an MCP server for web-only platforms. A local SQLite database serves as a materialized view for fast queries. Passive capture — reading agent session files after the fact, inspired by Entire.io's approach — is the primary recording mechanism. Any tool that can read git can read Opax. Any tool that can write git can extend the platform.

---

## What Opax Is

A **data specification** for storing structured agent activity data as git objects, an **SDK** that makes reading and writing that data ergonomic, a **passive capture engine** that records agent sessions automatically, and a **plugin system** that allows extensions to add capabilities like cross-platform memory, workflow sequencing, evaluations, and other tool adapters.

## What Opax Is Not

Opax is not an orchestration engine. It does not compete with LangGraph, Temporal, or Genkit for real-time agent coordination. It is not responsible for how agents do their work, how users review agent output, or how sessions are managed within a single agent platform. It does not run daemons, watchers, or persistent background processes locally. It does not manage containers, cloud sandboxes, or CI pipelines.

Opax is the durable state layer that orchestration tools write to, the continuity layer that agents read from, and the audit trail that compliance tools inspect.

**Strategic framing:** Opax is to GitHub what Datadog is to AWS CloudWatch: a specialized projection layer, not a hosting platform. The moment we start building code viewers, pull request reviews, or template registries, we've accidentally started building GitHub. Resist that pull. Opax renders the data GitHub ignores (notes, orphan branches, trailers), not the data GitHub already handles well.

---

## Architecture: Core + Plugins

### Core (open-source)

The core is deliberately thin. It owns data infrastructure; all domain logic lives in plugins.

1. **Git Data Spec** — A published specification defining naming conventions, schemas, and semantics for storing agent data as git objects. All data lives under the `opax/` namespace. It uses five git primitives: orphan branches, commit trailers, git notes, custom refs, and annotated tags. See companion: *Data Spec*.
2. **SDK** — A library providing typed read/write access to spec-conformant data, git hook event capture, plugin loading, and storage lifecycle management. Uses git plumbing commands or a git library for writes (never touches working tree). Reads via direct `.git/` object access or SQLite. Language: Go (go-git for git operations, modernc.org/sqlite for embedded database). Single-binary distribution with zero runtime dependencies. Concurrency via `.git/opax.lock` which serializes writes to the consolidated branch.
3. **SQLite Materialized View** — A local database at `.git/opax/opax.db` derived from git state. Provides FTS5 full-text search, structured queries, and typed views over all Opax data. Always rebuildable from git via `opax db rebuild`. Zero-infrastructure — single file, no server, ships with the SDK. WAL mode for concurrent reads. See companion: *Storage Spec*.
4. **Storage Lifecycle** — Two-tier storage model: metadata and references in git, bulk content (transcripts, diffs, action logs) in content-addressed storage referenced by hash. Tiered retention across hot (same repo) → warm (archive remote) → cold (git bundles on object storage). See companion: *Storage Spec*.
5. **Passive Capture Engine** — Operates outside agent sessions, reading agent-native storage after the fact. Agent-specific plugins know where each agent stores transcripts and session data (e.g., Claude Code's JSONL files, Codex session logs). Hooks detect sessions on commit. This is the primary recording mechanism — zero agent cooperation required. MCP server provides read-only query access for web-only platforms.
6. **Hygiene pipeline** — Secret detection and scrubbing on all content before storage. Scrubbing always precedes any future encryption — secrets are never stored even in encrypted form. `hygiene` metadata on session/save records records scrubbing applied at write time. See companion: *Hygiene Spec*.

### First-Party Plugins (open-source, replaceable)

1. **Memory** — Agent session recording and search. Passive capture records sessions automatically. CLI (`opax search`, `opax session`) is the primary query interface for agents with shell access. MCP server provides read-only query access for web-only platforms (Claude web, ChatGPT).
2. **Workflows** — Simple DAG-based stage sequencing with git-event triggers and human approval gates. YAML workflow format is owned by this plugin, not the core spec. This is a thin reference implementation, not a competing product. Teams that outgrow it should use Temporal or LangGraph with an Opax adapter. Can optionally fire-and-forget launch agents for stages, or just track state as external tools do the work.
3. **Evals** — A thin note format and CLI for attaching eval scores to commits as git notes. Not an evaluation framework — teams needing serious eval infrastructure use Braintrust or Langfuse with an Opax adapter.
4. **Executors** — Pluggable backends (local process, Docker, E2B, GitHub Actions) that run workflow stages in sandboxed environments. Used by the workflows plugin to dispatch work.
5. **Adapters** — Bridges that normalize third-party tool data (LangGraph, Temporal, GitHub Actions, various agent platforms) into Opax's git format. Positions Opax as the Rosetta Stone for agent data: every tool that writes data in its own format can have an adapter that translates it into the Opax spec. Adapters are the highest-leverage investment after memory. Every adapter expands the ecosystem without building competing products. Design principle: if a first-party plugin feels like its own product, stop and build an adapter instead. Potential Entire.io adapter: consume Entire's save format and normalize into Opax's query surface, giving Entire users cross-tool unification and compliance without switching capture tooling.

### Clients

**CLI (`opax`)** — Primary interface for both humans AND agents with shell access (Claude Code, Codex, Aider, Goose). Agents learn about Opax via CLAUDE.md / project docs and query via `opax search`. Core provides base commands (`opax init`, `opax db`, `opax storage`); plugins register subcommands (`opax session`, `opax workflow`, `opax eval`).

**MCP Server** — Secondary interface for agent platforms without shell access (Claude web, ChatGPT, mobile). Wraps the same SDK operations as the CLI. Tools: `search_sessions`, `list_sessions`, `get_session`. Not the primary integration point — most agents use the CLI directly.

**Studio** — Web UI for visualizing Opax data. Two deployment modes:

- **Local (free)** – `opax studio` launches a temporary local server (like Supabase Studio or Drizzle Studio). Reads from local SQLite. No daemon — runs only when invoked.
- **Hosted (paid) –** Always-on dashboard with Postgres backend, cross-repo views, notifications, cron triggers, team features. See *Commercial Model* below.

Every first-party plugin ships with a Studio panel. Memory → session timeline and browser. Workflows → DAG visualizer and gate approval UI. Evals → score dashboards and trend charts. Executors → execution log viewers. The more plugins you use, the richer Studio becomes.

### Plugin System

Plugins implement a common `OpaxPlugin` interface that provides: namespace registration (claiming a path prefix under `opax/`), SQLite schema extensions (tables and views materialized from their git data), CLI subcommand registration, MCP tool registration, and Studio panel registration. The plugin loading system is the architectural centerpiece — it enables the "build our own plugins + adapt every external tool" strategy.

---

## Design Principles

- **Git as state –** Opax's defensible position is as a projection/query layer over git. Conflating this with orchestration or hosting risks building a GitHub competitor by accident.
- **Fire-and-forget –** No daemon or watcher locally. All state advances reactively on user triggers, git hooks, external webhooks, or cron. Hooks fire asynchronously and return immediately, adding zero perceptible latency to git operations.
- **Event sourcing –** Git serves as the write-ahead log and distribution mechanism. SQLite serves as the materialized view optimized for queries. The database is always derivable from git. `git clone` + `opax init` always works.
- **Commit-anchored –** The primary question is "what context produced this commit?" not "what commits did this session produce?" Saves are created on commit. Session data hangs off the save. This produces a natural audit trail — developers and auditors trace backward from code to context.
- **Passive capture first –** Agents should not need to actively cooperate with Opax. The system reads agent-native storage after the fact. MCP is a complement for platforms without shell access, not the primary integration.
- **Scrubbing before encryption** – Secrets must never be stored even in encrypted form. The hygiene pipeline order is non-negotiable.
- **Layered metadata** – A single `Opax-Save` trailer on each commit provides a tamper-proof link to the session archive. Detailed metadata lives in git notes, which are invisible by default and do not modify the commit hash. Teams that need stronger audit guarantees can enable signed commits on Opax data branches, pin archive hashes in trailers, or enforce branch protection rules on `opax/` refs. Opax provides the tooling for each layer. Teams choose what they need.
- **Plugin ownership –** The workflows YAML format belongs to the plugin, not the spec. Keeps the core thin and the plugin replaceable. Same principle applies to eval criteria, adapter schemas, and executor configs.
- **Open spec first –** The git data format is implementable by third-party tools without the Opax SDK. This is the key ecosystem and defensibility lever: network effects come from the spec, not the runtime.
- **Phased infrastructure –** SQLite locally (zero friction), Postgres only at the web control plane where its strengths (JSONB/GIN indexes, `LISTEN`/`NOTIFY`, `pgvector`, concurrent writes) are warranted.

---

## Use Cases

### Scenario 1: Cross-Platform Session History (Passive Capture + CLI)

Developer uses Claude Code to implement auth → passive capture records the session on commit. Developer switches to Codex for a follow-up task → `opax search "auth architecture"` retrieves the previous session's summary and metadata. Another teammate on the same repo gets the same results. On `git push`, session metadata is shared with the team.

No manual handoff documents and no copy-paste. Session history is stored as git objects in the same repository the code lives in.

### Scenario 2: Agent → Agent (Workflow Sequencing)

Team defines a workflow: implement → review → test → merge, with human gates. Agent A implements on a branch, commits. The post-commit hook fires, Opax evaluates triggers, and transitions the workflow state to "review." Agent B is launched (or a human is notified) for review. On approval, tests run in Docker via an executor plugin. Results are written as git notes. On pass, merge happens (with or without a final human gate).

Opax advances state reactively. The workflow state machine only moves forward when something external happens. No process shepherds the workflow. Git is the state store, hooks are the event mechanism.

### Scenario 3: Agent → Human (Audit & Compliance)

Every agent-produced commit is annotated with structured metadata via git notes and an `Opax-Save` trailer: which agent produced it, which workflow stage, how long the session took. Passive capture ensures session recording without agent cooperation. Review assessments, test results, and eval scores are also notes. The complete provenance chain from initial prompt to production code is captured as immutable, cryptographically-linked git history.

For commits without any agent session (pure human coding), no session record exists — this is correct behavior since compliance concerns AI system logging, not all development.

This maps directly to EU AI Act Article 12 (record-keeping), Article 14 (human oversight via gates), NIST AI RMF, and ISO 42001 requirements. Developers don't do extra compliance work — the audit trail is a natural byproduct of using the product. See companion: *Compliance Framework*.

---

## Competitive Position

No existing tool combines cross-platform agent memory, git-native audit trails, declarative workflow sequencing, and pluggable execution in a single open data format.

**Vs. Mem0/Letta/Zep:** These use vector databases or proprietary storage for agent memory. Opax's data is inspectable with standard git commands, portable across hosting platforms, and distributed via `git push`. Cross-platform by design, not locked to one provider.

**Vs. LangGraph/Temporal/Genkit:** These are real-time intra-session orchestration engines. Opax handles inter-session orchestration: the durable state between sessions. They're complementary; Opax's adapter plugins normalize their output into the git data layer.

**Vs. Act/Dagger:** These run CI pipelines locally. Opax's executor plugins dispatch work to these (and other) backends. Different layer.

**Vs. Braintrust/Langfuse:** These are production AI observability platforms for teams shipping AI products to end users. Opax operates at the development layer: agent sessions, not production API traces. Different scale and data model. Opax is the data layer beneath; observability platforms consume Opax data, not compete with it.

**Vs. Entire.io:** Entire is a session recording and observability tool — it captures what agents did. Opax connects what agents know. Entire is write-only: agents cannot read previous sessions back. Opax is read-write: the CLI and MCP server provide a query path that enables agents to start warm with previous context. Entire has no compliance framing, no workflow orchestration, and no open data spec. Opax's passive capture learns from Entire's architecture (single consolidated branch, commit-anchored saves, agent plugin protocol) while adding the coordination, compliance, and adapter ecosystem layers Entire structurally cannot provide.

**Key differentiators:** Git as the data layer (inspectable, portable, distributed). Open specification (ecosystem API, not proprietary format). Compliance-ready by design (cryptographic integrity, immutable history). Provider-agnostic (works across Claude, Codex, ChatGPT, Gemini, OLLAMA, mobile).

**Biggest threat:** GitHub Agentic Workflows + GitHub MCP Registry in the next 12 months. Mitigation: ship fast, establish the spec before vendors move. The open format creates switching costs: ecosystem tools built on the format persist even if vendors offer alternatives. Secondary threat: Entire.io adding a read path (MCP server or CLI query). Their save data contains full transcripts — there's no structural barrier to building search. The open spec and adapter ecosystem are the durable moat.

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

### Phase 0: Core SDK + Passive Capture + Memory Plugin + CLI

Agent session recording that works across platforms. Passive capture + CLI search is the wedge.

**Deliverables:**

- `opax` binary (Go) — single binary containing CLI, SDK, plugin system, passive capture, MCP server
- Core: git data operations (go-git), SQLite materialization (modernc.org/sqlite), FTS5 search, plugin loading, hygiene pipeline (scrubbing), content-addressed storage
- Memory plugin (built-in): session recording, search
- Passive capture: Claude Code hook integration + Codex session reader. Save creation on commit. Transcript normalization into common format.
- CLI: `opax init`, `opax search`, `opax session list/get`, `opax db rebuild`, `opax storage stats`, `opax doctor`
- MCP server (built-in, secondary): for web-only platforms (Claude web, ChatGPT)
- Git Data Spec v1.0
- Setup guides for Claude Code, Codex

**Exit criteria:** Developer uses Claude Code with passive capture enabled. On commit, a save is created with session metadata + transcript hash. Developer runs `opax search "auth"` and retrieves relevant sessions. Another agent (Codex) in same repo runs same query, gets same results. Storage compaction runs. Secret scrubbing catches API keys.

### Phase 1: Workflows + Evals + Encryption + Basic Compliance

Git-event-driven workflow sequencing, structured evaluation scoring, and basic compliance reporting.

**Deliverables:**

- Workflows plugin — YAML parsing/validation, trigger evaluation, stage dispatch, gate management, git hook integration.
- Evals plugin — eval scoring, LLM-as-judge framework, git note attachment.
- Local process executor + Docker executor.
- Future phase: encryption at rest and authorization (spec TBD before ship).
- Basic compliance reporting: Article 12 evidence generation (session count, agent summary, human oversight records). EU AI Act enforces August 2026.
- Additional agent capture plugins: Cursor, Gemini CLI.
- CLI extensions for workflow management (`opax workflow start/status/approve/reject`).

**Exit criteria:** 3-stage workflow (implement → test → merge) runs end-to-end, triggered by git commits, with a human gate. Test stage runs in Docker. Results visible as git notes. Encrypted content readable only by authorized recipients. Basic Article 12 compliance report generated from existing data.

### Phase 2: Remote Execution + Web Control Plane

Remote executors and the first rich UI. Postgres enters the stack at this layer.

**Deliverables:**

- E2B sandbox executor.
- GitHub Actions executor.
- Studio — local and hosted modes. Hosted mode backed by Postgres.
- First adapter plugins (LangGraph, GitHub Actions data normalization).
- Webhook notifications for gates and workflow completion.
- `StorageBackend` interface with Postgres implementation.

**Exit criteria:** Same workflow runs with test stage on E2B. Studio shows live workflow progress. Gate approved from Studio. Adapter normalizes GitHub Actions run data into Opax format.

### Phase 3: Ecosystem + Compliance + Polish

Third-party integration, full compliance tooling, and community.

**Deliverables:**

- Git Data Spec v2.0 with extension guidelines.
- Full compliance reporting module (EU AI Act, NIST AI RMF, ISO 42001 mapping).
- Additional adapter plugins (Temporal, Braintrust, Langfuse).
- Semantic search (local embeddings) for context queries. Move earlier if FTS5 proves insufficient in Phase 0.
- Plugin registry or discovery mechanism.
- Team features (shared workflow configs, notification channels).

**Exit criteria:** Third-party tool reads session archives and writes eval scores as git notes using only the published spec, without importing the SDK. Compliance report generates evidence package for EU AI Act Article 12 from existing Opax data.

---

## Key Decisions Log

Accumulated architectural decisions from design conversations, in chronological order. Each is final unless explicitly revisited.

1. **Name:** Opax. CLI: `opax`. Namespace: `opax/`. GitHub: `opax-sh`. Domains: `opax.dev`, `opax.sh`.
2. **Language: Go.** Single-binary distribution with no runtime dependencies. go-git for git operations (plumbing-level access without touching working tree). modernc.org/sqlite for pure-Go SQLite (no CGo, no native deps). Fast startup, low memory. Studio (Phase 2 web UI) may use TypeScript/React — it's a separate deliverable. The Rust extraction path (`GitDataStore`) is no longer needed since Go already provides the performance characteristics that motivated it.
3. **Config format:** YAML with strict JSON Schema validation. Not TOML (ecosystem unfamiliarity), not Markdown (insufficient structure).
4. **Storage pattern:** Event sourcing / CQRS. Git = WAL + distribution. SQLite = materialized view. Database at `.git/opax/opax.db`, always rebuildable.
5. **Phased databases:** SQLite locally (Phase 0). Postgres at hosted control plane only (Phase 2). Abstracted behind `StorageBackend` interface.
6. **Architecture:** Thin core + plugin system. Core owns data infrastructure; all domain logic lives in plugins.
7. **Orchestration:** Opax handles inter-session orchestration (durable state between sessions). Intra-session orchestration (LangGraph's domain) is out of scope. We call them "workflows".
8. **Plugin naming:** "Workflows" not "orchestration" or "dispatch." The name avoids undermining the positioning.
9. **No daemon locally.** Fire-and-forget. Hooks fire async. No persistent process. Every feature requiring a persistent process is on the paid hosted tier.
10. **Trailers by default, notes as fallback.** Trailers are the default session linkage mechanism — immutable, tamper-evident, cryptographically bound to the commit hash. Notes are used for post-commit plugin data (test results, review verdicts, eval scores) that arrives after the commit. Teams that object to modified commit messages can disable trailers via `--no-trailers`, falling back to notes for session linkage.
11. **Hygiene pipeline order:** Scrub before any future encrypt. Non-negotiable. Session/save records carry `hygiene` metadata for scrubbing provenance.
12. **Future encryption:** Spec TBD before ship (e.g. `age`, content-focused encryption to limit git/CAS overhead).
13. **Execution environments:** Removed from core, reintroduced as executor plugins. The workflows plugin dispatches to them; the core doesn't know or care.
14. **Compliance as natural byproduct.** Session archives = Article 12 record-keeping. Workflow gates = Article 14 human oversight. Git integrity = tamper-evidence. Don't bolt on a compliance layer; the data model serves compliance structurally.
15. **Retention tensions.** PRD compaction (30d individual / 90d summary) conflicts with EU AI Act (system lifetime) and Colorado (3 years). Compliance mode overrides compaction settings. Addressed in *Storage & Scaling Spec* and *Compliance Framework*.
16. **Competitive positioning.** Opax is the data layer beneath observability platforms (Braintrust, Langfuse), not a direct competitor. Ship the spec, make evals expressive enough for them to consume Opax data. Expand upward only after the spec wins.
17. **Hook conflict strategy.** Wrapper script pattern: `opax init` installs thin wrapper hooks that back up pre-existing hooks (as `post-commit.pre-opax`), run the original first, then run Opax's hook async (fire-and-forget). For husky/lefthook users, Opax detects the hook manager and adds itself to the user's hook config. `opax init --no-hooks` skips installation entirely; session capture falls back to explicit MCP calls only.
18. **No session deduplication.** Every session is distinct — different agent, different model, different framing. No dedup logic, no similarity scoring. Store everything, return everything.
19. **Local cross-repo queries via SQLite ATTACH DATABASE.** Each repo has its own SQLite database. Cross-repo queries possible locally via `ATTACH DATABASE` (open multiple repo databases simultaneously). Not a primary feature but available for power users. Formal cross-repo is hosted-tier (Postgres materializes across repos). For explicit cross-boundary transfer, use `search_sessions` and `get_session` to pull context from the source repo.
20. **Search interface forward-compatibility.** `SearchStrategy` interface abstracts the search backend. Phase 0 ships `FTS5Strategy` only. `SearchOptions` includes a `search_mode` field (`keyword | semantic | hybrid`, default `keyword`); `semantic` and `hybrid` reserved but unimplemented until Phase 3. Embedding model choice deferred — landscape too volatile.
21. **Plugin discovery via conventions.** First-party plugins are built into the `opax` binary in Phase 0. Community plugins use `opax-plugin-`* naming. Discovery via search. No custom registry. If curation matters later, a lightweight JSON listing on `opax.dev` supplements without changing the install mechanism.
22. **Lazy sync on first read.** `post-merge` hook sets a dirty flag (touch `.git/opax/dirty`) — zero-cost staleness signal. SDK checks the flag on read, syncs transparently if stale. Progress callback for large deltas. No background process, no manual step, no daemon.
23. **Single consolidated orphan branch.** All Opax data lives on one branch (`opax/v1`) with sharded directory structure (first two chars of ID). Adopted from Entire.io's architecture. Git shares tree objects between commits, delta compression works across full history, ref enumeration stays fast. Phase 0 stores everything on this branch; bulk content migrates to CAS when scale demands it.
24. **Passive capture as primary recording.** Hooks detect agent sessions and read transcripts from disk after the agent writes them (Entire.io pattern). Zero agent cooperation required. MCP server provides read-only session query access for web-only platforms. CLI is the primary query interface for agents with shell access.
25. **Stealth is default.** `opax/`* branches aren't in the default refspec, push is explicit via `opax push`. No special stealth flag needed; the refspec design already achieves it.
26. **Two-tier storage model.** Metadata and references in git (small, tied to git objects, benefits from integrity and distribution). Bulk content (transcripts, diffs, action logs) in content-addressed storage at `.git/opax/content/`, referenced by SHA-256 hash from git metadata. Content hash in metadata provides explicit tamper-verification via sha256sum comparison. Dramatically reduces git footprint (~2-5 MB/day vs ~100 MB/day for 5-dev team).
27. **Commit-anchored data model.** Primary question: "what context produced this commit?" not "what commits did this session produce?" Saves are created on commit. Session data hangs off the save. Adopted from Entire.io. More natural audit trail — developers and auditors trace backward from code to context.
28. **Artifacts are not Opax's purview.** Opax records what happened during development (sessions, decisions, reviews), not the artifacts development produces. ADRs, architecture docs, etc. belong in docs/ folder, Notion, Jira, Linear. Session records capture that discussions happened and link to resulting artifacts.
29. **Plugin strategy discipline.** Memory is the real product and deserves deep investment. Workflows, evals, executors are thin reference implementations. Adapters are the high-leverage investment after memory. If a first-party plugin feels like its own product, stop and build an adapter instead.
30. **Archive tiers.** Hot (0-30d): same repo, consolidated branch, SQLite. Warm (30-90d): git remote (archive repo), fetch on demand. Cold (90+d): git bundles on object storage, download + fetch from bundle. Hosted: git alternates (shared object pool), Postgres query surface.
31. **Future access control.** Encryption or other authorization TBD before ship; external CAS/storage boundaries enforce access at deployment time today.
32. **Competitive position vs Entire.io.** "Entire captures what agents did. Opax connects what agents know." Don't compete on session recording — compete on the read path, unified query surface, compliance, and adapter ecosystem. Learn from their architecture; build what they structurally cannot.

---

## References


| Document                  | Scope                                                                                        |
| ------------------------- | -------------------------------------------------------------------------------------------- |
| *Git Data Spec*           | Namespace conventions, git primitives, schemas, SQLite materialization, plugin registration  |
| *Hygiene Spec* | Secret scrubbing pipeline, config, metadata on records |
| *Compliance Framework*    | EU AI Act, NIST AI RMF, ISO 42001, Colorado AI Act mapping, data model additions, retention  |
| *Storage & Scaling Spec*  | Two-tier storage, capacity math, archive tiers, StorageBackend interface, compaction         |


