# Opax - Spec-Driven Delivery Workflow

## Purpose

Define the update contract for planning, implementation, and docs maintenance in this repository.

## Document Ownership

- Current state lives in [docs/index.md](../index.md).
- Strategy and phase planning live in [docs/product/](../product/overview.md).
- Structure and conventions live in [docs/architecture/](../architecture/repo-structure.md).
- Scoped design records live in `docs/epics/`, `docs/features/`, and `docs/adrs/`.

## Update Rules

1. Work-state changes update [docs/index.md](../index.md).
2. Scope, contracts, acceptance criteria, or test-plan changes update the affected epic or feature doc.
3. Strategy, roadmap sequencing, or product-direction changes update `docs/product/`.
4. Package boundaries or structural conventions update `docs/architecture/`.
5. Non-obvious trade-offs update `docs/adrs/`.

## Delivery Flow

1. Confirm that the epic or feature doc captures the intended scope and acceptance criteria.
2. Implement the change in code.
3. Run the relevant verification.
4. If the work changed current repo state, update [docs/index.md](../index.md).
5. If the work changed scope or acceptance criteria, update the scoped design doc in the same patch.

## Guardrails

- Do not use product or architecture docs as execution dashboards.
- Do not duplicate mutable implementation status across roadmap, overview, repo-structure, epic, and feature docs.
- Keep `docs/index.md` as the single place that answers "what is active right now?" and "what is implemented right now?"
