# Opax

Structured recording layer for agent work, built on git. Go monorepo, single binary.

## Instructions
Act as my high-level advisor. Challenge my thinking, question my assumptions, and expose blind spots. Stop defaulting to agreement. If my reasoning is weak, break it down and show me why. Take new ideas and plans with a degree of skepticism: do not accept them as blind truths, and evaluate them carefully.

## Commands

```bash
make build              # Build → bin/opax
make test               # go test ./...
make lint               # go vet ./...
make clean              # rm -rf bin/
go test ./internal/cas/ # Single-package test
go mod tidy             # After adding/removing imports
```

## Development Workflow

**Docs first, code second.** Before writing code for any new feature or significant change:

1. Check `docs/product/roadmap.md` for the epic/feature this maps to
2. Write or update the relevant doc in `docs/` before opening a PR:
   - `docs/epics/E{N}-{name}.md` — epic spec (scope, acceptance criteria, dependencies)
   - `docs/features/E{N}.{M}-{name}.md` — feature spec (design, edge cases, test plan)
   - `docs/adrs/ADR-{NNN}-{name}.md` — architecture decision record (for non-obvious choices)
   - `docs/tasks/` — task breakdowns when a feature needs sub-steps
3. Reference the doc in your commit message or PR description

This applies to agents too. If an agent is asked to implement a feature, it should check for an existing doc first and create one if missing.

## Don'ts

- **Don't touch the working tree from `internal/git/`** — plumbing only (hash-object, mktree, commit-tree, update-ref)
- **Don't write to git from `internal/store/`** — it's a read-only materialized view, always rebuildable
- **Don't store secrets, even encrypted** — scrub before encrypt, always. Pipeline order is non-negotiable
- **Don't add a `pkg/` directory** — all library code goes in `internal/`
- **Don't use testify, gomock, or any test framework** — stdlib `testing` only, table-driven tests
- **Don't use `panic` in library code** — return errors to the caller
- **Don't add global state** — no package-level variables holding state, no `init()` side effects outside `cmd/opax/main.go`
- **Don't skip `--json` support** — every CLI command must support JSON output from day one
- **Don't use CGo** — pure-Go dependencies only (modernc.org/sqlite, not mattn). Single binary, zero runtime deps
- **Don't make plugins complex enough to feel like products** — if a plugin is getting big, build an adapter for another vendor instead
- **Don't store artifacts/docs in Opax** — Opax records what happened during development, not what development produced

## Code Conventions

- Error wrapping: `fmt.Errorf("package: operation failed: %w", err)` — always include package name
- Interfaces at package boundaries (`StorageBackend`, `SearchStrategy`, `OpaxPlugin`), concrete types everywhere else
- Constructors return structs, not interfaces
- Test files alongside code: `store_test.go` next to `store.go`
- CLI commands: define `*cobra.Command` var, register in `init()` via `parentCmd.AddCommand()`
- Plugin commands register via `OpaxPlugin` interface, not in `main.go`
- Go naming: `camelCase` unexported, `PascalCase` exported. Packages short, lowercase, single-word

## Architecture Invariants

These are non-negotiable. Violating any of these is a bug.

1. **Scrub before encrypt** — secrets must never be stored, not even encrypted. Privacy pipeline runs before any write
2. **Two-tier storage** — metadata on git (`opax/v1` branch), bulk content in CAS (`.git/opax/content/`). 4 KB threshold: inline < 4 KB, CAS >= 4 KB
3. **Single orphan branch** — all Opax data lives on `opax/v1`, sharded directory layout. Never create per-record branches
4. **Commit-anchored** — the primary question is "what context produced this commit?". Saves anchor to commits, sessions hang off saves
5. **SQLite is a cache** — `.git/opax/opax.db` is always rebuildable from git via `opax db rebuild`. Never treat it as source of truth
6. **Passive capture primary** — hooks read agent session files from disk after the fact. MCP is a complement for platforms without shell access, not the primary path
7. **Write serialization** — `.git/opax.lock` for all writes to the consolidated branch. No concurrent writes in Phase 0
8. **Commit trailers use `Opax-` prefix** — branch namespace uses `opax/`, and trailers keep `Opax-` (e.g., `Opax-Save`)
