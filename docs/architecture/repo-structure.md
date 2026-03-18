# Opax — Repository Structure & Code Conventions

**For:** Developers and agents working on the Opax codebase
**Prerequisite reading:** [Strategy Overview](../strategy/overview.md) for product context

---

## 1. Repository Layout

```
opax/
├── cmd/
│   └── opax/
│       └── main.go              # CLI entry point — Cobra command tree, single binary
├── internal/
│   ├── git/
│   │   └── git.go               # go-git plumbing: orphan branches, notes, trailers, refs
│   ├── store/
│   │   └── store.go             # SQLite materialization + FTS5 search
│   ├── cas/
│   │   └── cas.go               # Content-addressed storage (.git/opax/content/)
│   ├── capture/
│   │   ├── capture.go           # Passive capture engine coordinator
│   │   ├── claudecode/
│   │   │   └── claudecode.go    # Claude Code JSONL session reader
│   │   └── codex/
│   │       └── codex.go         # OpenAI Codex session reader
│   ├── privacy/
│   │   └── privacy.go           # Secret scrubbing pipeline
│   ├── mcp/
│   │   └── mcp.go               # MCP server (stdio transport)
│   └── plugin/
│       └── plugin.go            # Plugin interface + subprocess JSON-RPC protocol
├── plugins/
│   └── memory/
│       └── memory.go            # Built-in memory plugin (cross-platform context)
├── docs/
│   ├── architecture/            # Developer-facing architecture docs (you are here)
│   ├── strategy/                # Product strategy, data spec, storage spec, privacy, compliance
│   ├── adrs/                    # Architecture Decision Records
│   ├── plans/                   # Implementation plans
│   ├── prds/                    # Product requirements
│   └── tasks/                   # Task tracking
├── Makefile                     # Build targets: build, test, lint, clean
├── go.mod                       # Module: github.com/opax-sh/opax
└── go.sum                       # Dependency checksums
```

Three top-level Go directories:

- **`cmd/`** — Binary entry points. Currently only `cmd/opax/` (the CLI). One `main.go` per binary.
- **`internal/`** — All library code. The `internal/` prefix enforces Go's visibility rule: these packages cannot be imported by external modules. This is intentional — no public Go API until one is deliberately designed.
- **`plugins/`** — Built-in plugins compiled into the binary. Same `OpaxPlugin` interface as community plugins, but linked at build time rather than communicating over subprocess JSON-RPC.

---

## 2. `cmd/opax/` — CLI Entry Point

### Structure

Single `main.go` file containing the Cobra command tree. All commands are registered in `init()` and executed from `main()`.

### Command Tree

```
opax
├── version                      # Print version
├── init                         # Initialize Opax in a git repo
├── search [query]               # Full-text search over agent context
├── doctor                       # Check installation and repo health
├── db
│   └── rebuild                  # Rebuild SQLite from git state
├── session
│   ├── list                     # List session archives
│   └── get [id]                 # Get a specific session archive
└── storage
    └── stats                    # Show storage statistics
```

### `--json` Flag

A persistent `--json` flag is registered on the root command and inherited by all subcommands. Every command must support JSON output — this is the SDK contract. When `--json` is set, commands emit structured JSON to stdout. This enables agents and scripts to consume Opax programmatically.

### Adding a New Subcommand

1. Define a `*cobra.Command` variable in `main.go` (or in a new file under `cmd/opax/` if the file grows large)
2. Register it with `parentCmd.AddCommand(newCmd)` in `init()`
3. Implement `--json` output from day one
4. If the command belongs to a plugin, the plugin registers it via the `OpaxPlugin` interface — not in `main.go`

---

## 3. `internal/` — Core Packages

### `internal/git` — Git Plumbing

**Responsibility:** All git operations for the Opax data layer. Orphan branch management (`opax/v1`), git notes (read/write across `refs/opax/notes/*` namespaces), commit trailers, custom refs (`refs/opax/*`), and ref updates.

**Key dependencies:** `go-git` (plumbing-level git operations)

**Boundaries:** Never touches the working tree. Never checks out branches. Uses git plumbing commands (`hash-object`, `mktree`, `commit-tree`, `update-ref`) or their go-git equivalents. Does not know about SQLite, capture, or privacy — it only moves git objects.

### `internal/store` — SQLite Materialization

**Responsibility:** Manages the SQLite database at `.git/opax/opax.db`. Owns schema creation, incremental sync from git, full rebuild, FTS5 search, and the `StorageBackend` interface that abstracts database dialect.

**Key dependencies:** `modernc.org/sqlite` (pure-Go SQLite, no CGo)

**Boundaries:** Read-only view of git data — never writes to git. The database is always rebuildable from git via `opax db rebuild`. Does not implement business logic beyond materialization and query.

### `internal/cas` — Content-Addressed Storage

**Responsibility:** Stores and retrieves bulk content (transcripts, diffs, action logs) at `.git/opax/content/`. SHA-256 hashing, sharded directory layout (first two chars of hash), integrity verification.

**Key dependencies:** stdlib `crypto/sha256`, `os`

**Boundaries:** Knows nothing about git objects, metadata schemas, or record types. It stores bytes, returns hashes, retrieves bytes by hash. The caller decides what to store and where to record the hash.

### `internal/capture/` — Passive Capture Engine

**Responsibility:** Platform-agnostic coordinator for passive session capture. Detects agent sessions, delegates to platform-specific readers, normalizes output into Opax's common transcript format.

**Boundaries:** Does not write to git directly — produces normalized data that the CLI or hooks pipe through the privacy pipeline and then into `internal/git` and `internal/cas`.

#### `capture/claudecode` — Claude Code Reader

**Responsibility:** Reads Claude Code JSONL session files from disk and normalizes them into the common transcript format.

#### `capture/codex` — Codex Reader

**Responsibility:** Reads OpenAI Codex session logs and normalizes them into the common transcript format.

**Adding a new capture source:** Create a new sub-package under `internal/capture/` (e.g., `capture/cursor/`). Implement the same reader interface. Register it in the coordinator.

### `internal/privacy` — Secret Scrubbing

**Responsibility:** The secret scrubbing pipeline. Detects and removes secrets (API keys, tokens, credentials) from content before storage.

**Key dependencies:** stdlib (pattern matching, regex)

**Boundaries:** Pipeline order is non-negotiable: **scrub before encrypt**. Secrets must never be stored even in encrypted form. Phase 0 implements scrubbing only. The `PrivacyMetadata` type ships now to scaffold Phase 1 encryption without rearchitecting.

### `internal/mcp` — MCP Server

**Responsibility:** MCP server using stdio transport. Secondary interface for web-only agent platforms (Claude web, ChatGPT) that lack shell access. Exposes three tools: `search_sessions`, `list_sessions`, `get_session`.

**Key dependencies:** Official MCP Go SDK

**Boundaries:** Wraps the same operations as the CLI. Not the primary integration point — most agents use the CLI directly. Does not implement business logic; delegates to the same internal packages the CLI uses.

### `internal/plugin` — Plugin System

**Responsibility:** Defines the `OpaxPlugin` interface and the subprocess JSON-RPC protocol for community plugins. Handles plugin loading, namespace registration, schema extension, and CLI/MCP tool registration.

**Key dependencies:** stdlib `encoding/json`, `os/exec` (for subprocess plugins)

**Boundaries:** First-party plugins are compiled in (no subprocess overhead). Community plugins use the `opax-plugin-*` naming convention and communicate via JSON-RPC over stdin/stdout. The plugin system does not implement any domain logic itself.

---

## 4. `plugins/` — Built-in Plugins

### `plugins/memory/` — Cross-Platform Context Persistence

The primary value plugin. Enables context to flow between agent sessions across platforms (Claude Code, Codex, ChatGPT, etc.).

**What it owns:**
- Session archive storage
- Save creation (commit-anchored, with session attribution via file overlap + temporal proximity)
- Search over sessions

**Compiled into the binary** — not a subprocess plugin. Uses the same `OpaxPlugin` interface as community plugins, so it can be replaced or extended.

### Adding a New Built-in Plugin

1. Create `plugins/{name}/{name}.go`
2. Implement the `OpaxPlugin` interface (namespace registration, schema extensions, CLI subcommands, MCP tools)
3. Import and register the plugin in `cmd/opax/main.go`
4. The plugin owns its namespace under `opax/v1/` and creates SQLite views over `opax_notes` (not new tables)

---

## 5. Code Patterns & Conventions

### `internal/` for everything

No `pkg/` directory. All library code is internal. A public Go API will be extracted if and when external consumers need one. Until then, `internal/` keeps the API surface locked down.

### Interfaces at boundaries

Define interfaces where packages meet: `StorageBackend`, `SearchStrategy`, `OpaxPlugin`. Concrete implementations sit behind interfaces. This enables the SQLite → Postgres transition, FTS5 → semantic search evolution, and first-party → community plugin interchangeability.

### Error handling

- stdlib `errors` and `fmt.Errorf` with `%w` wrapping
- No `panic` in library code
- Return errors to the caller; let the CLI decide how to present them
- Wrap errors with context: `fmt.Errorf("store: rebuild failed: %w", err)`

### Testing

- stdlib `testing` only — no testify, no gomock
- Table-driven tests
- Test files alongside code: `store_test.go` next to `store.go`
- `make test` runs `go test ./...`

### No global state

Pass dependencies explicitly. Constructors return structs, not interfaces. No `init()` side effects outside of `cmd/opax/main.go`. No package-level variables that hold state.

### Concurrency

- `.git/opax.lock` for write serialization to the consolidated branch
- No goroutine pools in Phase 0
- SQLite in WAL mode for concurrent reads

### JSON output

Every CLI command supports `--json`. This is the SDK contract for agent consumption. JSON output format must be stable — breaking changes require a version bump.

### Naming

- Go standard: `camelCase` for unexported, `PascalCase` for exported
- Package names are short, lowercase, single-word where possible
- Files named after the package or the primary type they contain

---

## 6. Dependency Map

| Package | External Dependency | Why |
|---|---|---|
| `cmd/opax` | `github.com/spf13/cobra` | CLI framework — command tree, flags, help generation |
| `internal/git` | `go-git` (planned) | Plumbing-level git operations without shelling out |
| `internal/store` | `modernc.org/sqlite` (planned) | Pure-Go SQLite — no CGo, no native dependencies, single-binary friendly |
| `internal/mcp` | Official MCP Go SDK (planned) | MCP server with stdio transport |
| `internal/cas` | stdlib only | SHA-256 hashing, file I/O |
| `internal/privacy` | stdlib only | Pattern matching, regex |
| `internal/capture` | stdlib only | File I/O, JSON parsing |
| `internal/plugin` | stdlib only | JSON-RPC, subprocess management |
| `plugins/memory` | (uses internal packages) | No direct external dependencies |

Currently only `cobra` is in `go.mod`. Other dependencies will be added as packages are implemented.

---

## 7. Build & Development

```bash
make build    # → bin/opax (go build -o bin/opax ./cmd/opax/)
make test     # → go test ./...
make lint     # → go vet ./...
make clean    # → rm -rf bin/
```

Dependency management: `go mod tidy` after adding/removing imports.

The binary is self-contained — zero runtime dependencies. No Docker, no external database, no daemon.

---

## 8. Data Flow Overview

### Write Path (Capture → Storage)

```
Agent session ends
    → Passive capture reads session files from disk
    → Platform reader (claudecode/codex) normalizes to common format
    → Privacy pipeline scrubs secrets (non-negotiable: scrub before encrypt)
    → CAS stores bulk content, returns SHA-256 hash
    → Git plumbing writes metadata to opax/v1 branch
    → SQLite materializes new records (incremental sync)
```

### Read Path (Query → Response)

```
CLI `opax search` or MCP `search_sessions`
    → SQLite FTS5 full-text search
    → Return metadata (id, title, tags, timestamps)
    → If full content requested: fetch from CAS using content_hash
    → Optionally verify integrity (sha256sum comparison)
```

For detailed schemas, directory layouts, and storage mechanics, see:
- [Data Spec](../strategy/data-spec.md) — git primitives, record formats, SQLite schema
- [Storage Spec](../strategy/storage.md) — two-tier model, CAS, archive tiers, `StorageBackend` interface
