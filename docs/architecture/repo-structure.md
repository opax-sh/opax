# Opax - Repository Structure and Conventions

**For:** Developers and agents working on the Opax codebase  
**Related docs:** [Product Overview](../product/overview.md), [Data Spec](../product/data-spec.md), [Roadmap](../product/roadmap.md)

---

## 1. Current Repository Layout

This reflects the current implementation in this repository.

```text
opax/
|- cmd/
|  |- opax/
|  |  |- main.go
|- internal/
|  |- capture/
|  |  |- capture.go
|  |  |- claude/
|  |  |  |- claude.go
|  |  |- codex/
|  |     |- codex.go
|  |- cas/
|  |  |- cas.go
|  |- config/
|  |  |- config.go
|  |  |- config_test.go
|  |- git/
|  |  |- git.go
|  |- hygiene/
|  |  |- hygiene.go
|  |- mcp/
|  |  |- mcp.go
|  |- plugin/
|  |  |- plugin.go
|  |- store/
|  |  |- store.go
|  |- types/
|  |  |- types.go
|  |  |- types_test.go
|  |- deps_smoke_test.go
|- plugins/
|  |- memory/
|     |- memory.go
|- docs/
|  |- architecture/
|  |- adrs/
|  |- epics/
|  |- features/
|  |- misc/
|  |- product/
|- Makefile
|- go.mod
|- go.sum
```

Notes:
- The repo currently has `docs/product/*` (not `docs/strategy/*`).
- Several packages are scaffolds with package docs only; implementation is tracked in product/epic/feature docs.

---

## 2. Current CLI Surface

Current command tree in `cmd/opax/main.go`:

```text
opax
|- version
|- init
|- search [query]
|- db rebuild
|- session list
|- session get [id]
|- storage stats
|- doctor
```

Current behavior:
- `opax version` is implemented.
- `init`, `search`, `db rebuild`, `session list/get`, `storage stats`, and `doctor` are currently command stubs.

---

## 3. Package Status (Current)

Implemented foundations:
- `internal/types`: canonical ID/types/enums and tests.
- `internal/config`: config defaults/load/merge/validate and tests.
- `internal/deps_smoke_test.go`: dependency smoke checks.

Scaffolded (package exists, major behavior still pending):
- `internal/git`
- `internal/store`
- `internal/cas`
- `internal/capture` (including `claude` and `codex`)
- `internal/hygiene`
- `internal/mcp`
- `internal/plugin`
- `plugins/memory`

---

## 4. Conventions

- Use `internal/` for all non-entrypoint Go code.
- Keep package boundaries explicit (`types`, `config`, `git`, `store`, etc.).
- Prefer table-driven tests and stdlib `testing`.
- Keep docs explicit about status:
  - `implemented` means code path works today.
  - `scaffolded` means package/command exists but behavior is not complete.
  - `planned` means design target, not present implementation.

---

## 5. Build and Verification

```bash
make build
make test
make lint
make clean
```

Current test command used in this repo:

```bash
go test ./...
```
