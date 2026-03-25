# Runbook - Doc Authoring Quickstart

## Purpose

Choose the right document type quickly and keep mutable state in one place.

## Use This Doc Type Guide

- `docs/index.md`: current implementation state, active epic and feature pointers, and the full docs inventory.
- `docs/product/`: strategy, phase planning, and durable product direction.
- `docs/architecture/`: package boundaries, CLI shape, and structural conventions.
- `docs/epics/`: shared scope and contracts for a multi-feature initiative.
- `docs/features/`: scoped design, acceptance criteria, and test plan for one feature.
- `docs/adrs/`: non-obvious decisions and trade-offs.

## Authoring Order

1. Update `docs/product/roadmap.md` only when phase sequencing or epic planning changes.
2. Update the relevant epic doc only when shared scope or contracts change.
3. Update the relevant feature doc only when feature scope, acceptance criteria, or test plan changes.
4. Update `docs/index.md` whenever active work or implementation state changes.
5. Add an ADR when a design decision needs an explicit trade-off record.

## Linking Rules

- Each feature doc links one epic.
- Epic and feature docs link any required architecture docs and ADRs.
- Stable reference docs should point to real files, not placeholder paths.
- `docs/index.md` lists every file under `docs/`.

## Verification Checklist

- [ ] The chosen doc type matches the change being made.
- [ ] Mutable work-state changes are captured only in `docs/index.md`.
- [ ] Stable reference docs link to real files.
- [ ] Epic and feature docs describe scope and acceptance criteria without mutable status metadata.

## References

- [Documentation Index](../index.md)
- [Product Roadmap](../product/roadmap.md)
- [Repository Structure](../architecture/repo-structure.md)
- [Spec-Driven Delivery Workflow](spec-driven-delivery-workflow.md)
- [Epic Template](../epics/_template.md)
- [Feature Template](../features/_template.md)
- [ADR Template](../adrs/_template.md)
