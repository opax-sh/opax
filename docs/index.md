# Documentation Index

`docs/index.md` is the authoritative current-state document for this repository.
Use it as the one-page status rollup across all epics and features.

## Current State

### Epic Status Rollup


| Epic      | Status      | Doc                                                                            |
| --------- | ----------- | ------------------------------------------------------------------------------ |
| EPIC-0000 | Completed   | [epics/EPIC-0000-foundation.md](epics/EPIC-0000-foundation.md)                 |
| EPIC-0001 | In Progress | [epics/EPIC-0001-git-plumbing-layer.md](epics/EPIC-0001-git-plumbing-layer.md) |


### Feature Status Rollup


| Feature   | Epic      | Status    | Doc                                                                                              |
| --------- | --------- | --------- | ------------------------------------------------------------------------------------------------ |
| FEAT-0001 | EPIC-0000 | Completed | [features/FEAT-0001-add-dependencies.md](features/FEAT-0001-add-dependencies.md)                 |
| FEAT-0002 | EPIC-0000 | Completed | [features/FEAT-0002-core-domain-types.md](features/FEAT-0002-core-domain-types.md)               |
| FEAT-0003 | EPIC-0000 | Completed | [features/FEAT-0003-configuration-system.md](features/FEAT-0003-configuration-system.md)         |
| FEAT-0004 | EPIC-0000 | Completed | [features/FEAT-0004-file-lock-utility.md](features/FEAT-0004-file-lock-utility.md)               |
| FEAT-0005 | EPIC-0001 | Completed | [features/FEAT-0005-repo-discovery.md](features/FEAT-0005-repo-discovery.md)                     |
| FEAT-0006 | EPIC-0001 | Completed | [features/FEAT-0006-orphan-branch-management.md](features/FEAT-0006-orphan-branch-management.md) |
| FEAT-0007 | EPIC-0001 | Completed | [features/FEAT-0007-write-records-to-branch.md](features/FEAT-0007-write-records-to-branch.md)   |
| FEAT-0008 | EPIC-0001 | Completed | [features/FEAT-0008-read-records-from-branch.md](features/FEAT-0008-read-records-from-branch.md) |
| FEAT-0009 | EPIC-0001 | Completed | [features/FEAT-0009-git-notes-operations.md](features/FEAT-0009-git-notes-operations.md)         |
| FEAT-0010 | EPIC-0001 | In Progress | [features/FEAT-0010-commit-trailer-support.md](features/FEAT-0010-commit-trailer-support.md)     |
| FEAT-0011 | EPIC-0001 | Backlog   | [features/FEAT-0011-refspec-configuration.md](features/FEAT-0011-refspec-configuration.md)       |


## Update Rules

- Work-state changes update both the scoped epic/feature doc status and this rollup.
- Scope, contracts, acceptance criteria, or test-plan changes update the scoped epic or feature doc.
- Strategy, phase planning, and cross-cutting product direction changes update `docs/product/`.
- Structural conventions and package boundaries update `docs/architecture/`.
- Non-obvious decisions with trade-offs update `docs/adrs/`.

## Docs Inventory

### `product/`

- [product/overview.md](product/overview.md)
- [product/roadmap.md](product/roadmap.md)
- [product/data-spec.md](product/data-spec.md)
- [product/gtm-report.md](product/gtm-report.md)
- [product/storage.md](product/storage.md)
- [product/hygiene.md](product/hygiene.md)
- [product/compliance.md](product/compliance.md)

### `runbooks/`

- [runbooks/doc-authoring-quickstart.md](runbooks/doc-authoring-quickstart.md)
- [runbooks/spec-driven-delivery-workflow.md](runbooks/spec-driven-delivery-workflow.md)
- [runbooks/_template.md](runbooks/_template.md)

### `epics/`

- [epics/EPIC-0000-foundation.md](epics/EPIC-0000-foundation.md)
- [epics/EPIC-0001-git-plumbing-layer.md](epics/EPIC-0001-git-plumbing-layer.md)
- [epics/_template.md](epics/_template.md)

### `features/`

- [features/FEAT-0001-add-dependencies.md](features/FEAT-0001-add-dependencies.md)
- [features/FEAT-0002-core-domain-types.md](features/FEAT-0002-core-domain-types.md)
- [features/FEAT-0003-configuration-system.md](features/FEAT-0003-configuration-system.md)
- [features/FEAT-0004-file-lock-utility.md](features/FEAT-0004-file-lock-utility.md)
- [features/FEAT-0005-repo-discovery.md](features/FEAT-0005-repo-discovery.md)
- [features/FEAT-0006-orphan-branch-management.md](features/FEAT-0006-orphan-branch-management.md)
- [features/FEAT-0007-write-records-to-branch.md](features/FEAT-0007-write-records-to-branch.md)
- [features/FEAT-0008-read-records-from-branch.md](features/FEAT-0008-read-records-from-branch.md)
- [features/FEAT-0009-git-notes-operations.md](features/FEAT-0009-git-notes-operations.md)
- [features/FEAT-0010-commit-trailer-support.md](features/FEAT-0010-commit-trailer-support.md)
- [features/FEAT-0011-refspec-configuration.md](features/FEAT-0011-refspec-configuration.md)
- [features/_template.md](features/_template.md)

### `architecture/`

- [architecture/repo-structure.md](architecture/repo-structure.md)

### `adrs/`

- [adrs/ADR-001-event-sourcing-git-sqlite.md](adrs/ADR-001-event-sourcing-git-sqlite.md)
- [adrs/ADR-002-two-tier-storage.md](adrs/ADR-002-two-tier-storage.md)
- [adrs/ADR-003-single-orphan-branch.md](adrs/ADR-003-single-orphan-branch.md)
- [adrs/ADR-004-passive-capture.md](adrs/ADR-004-passive-capture.md)
- [adrs/ADR-005-commit-anchored-data-model.md](adrs/ADR-005-commit-anchored-data-model.md)
- [adrs/ADR-006-execution-drivers.md](adrs/ADR-006-execution-drivers.md)
- [adrs/_template.md](adrs/_template.md)

### `misc/`

- [misc/sharding-research.md](misc/sharding-research.md)
- [misc/git-sqlite-research.md](misc/git-sqlite-research.md)
