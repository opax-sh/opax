# Opax - Repository Structure and Conventions

**Related docs:** [Documentation Index](../index.md), [Product Overview](../product/overview.md), [Product Roadmap](../product/roadmap.md)

## Repository Layout

The repository is organized around clear package ownership boundaries:

- `cmd/opax/`: user-facing CLI entrypoint.
- `internal/types/`: canonical IDs, enums, and record metadata types.
- `internal/config/`: config defaults, merge semantics, and validation.
- `internal/lock/`: repository-local coordination for administrative mutations.
- `internal/git/`: git plumbing for repo discovery, Opax branch lifecycle, and git-backed data operations.
- `internal/cas/`: content-addressed storage primitives.
- `internal/store/`: materialized query-store layer.
- `internal/capture/`: agent-specific capture readers and normalization.
- `internal/hygiene/`: secret scrubbing and hygiene metadata.
- `internal/plugin/`: plugin contracts and registration points.
- `internal/mcp/`: MCP-facing integration surface.
- `docs/`: product, architecture, epic, feature, ADR, and runbook docs.

## CLI Shape

The stable user-facing command surface is:

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

This document defines the shape of the interface and package boundaries.
Current implementation state for that surface lives in [docs/index.md](../index.md).

## Documentation Contract

- `docs/index.md` is the only authoritative current-state document.
- `docs/product/` captures strategy and phase planning.
- `docs/architecture/` captures durable repository conventions.
- `docs/epics/`, `docs/features/`, and `docs/adrs/` capture scoped design records.

## Build and Verification

```bash
make build
go test ./...
cd tools && go test ./...
go vet ./...
```
