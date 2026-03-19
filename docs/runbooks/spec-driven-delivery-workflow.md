| Field | Value |
|---|---|
| **Type** | Workflow |
| **Status** | Proposed |
| **Last synced** | 2026-03-18 |

# Plotpoint - Spec-Driven Delivery Workflow

## Purpose
Define a repeatable planning-to-delivery workflow for Plotpoint where epics are strategic initiatives and feature PRDs are implementation units.

## Canonical Flow
1. Create or update product roadmap as the ordered queue of MVP epics.
2. Create or update only the current epic doc.
3. Create architecture docs only for the current epic and only when they are needed.
4. Create feature PRDs only for the current epic.
5. Pick one current-epic feature PRD to implement.
6. Create a feature branch.
7. Set feature PRD status to `In Progress` and open a draft PR with a docs-only scaffolding commit.
8. Implement the feature.
9. Run tests and close acceptance criteria.
10. Approve PR, merge, update statuses, and only then move to the next feature or next epic.

## Planning Depth Rules
- The roadmap may list many future epics.
- Only the active epic should have a full doc in `docs/epics/`.
- Architecture docs are written just in time for the active epic.
- Feature PRDs are written just in time for the active epic.
- Future epic details should stay lightweight in the roadmap until the team is ready to work on them.

## Doc Types and Intent
- `docs/product/`: strategy and roadmap docs.
- `docs/runbooks/`: operational guides and repeatable team workflows.
- `docs/epics/`: broader initiatives, sequencing, and value narrative.
- `docs/architecture/`: cross-cutting design docs and technical constraints.
- `docs/adrs/`: non-obvious decisions with trade-offs.
- `docs/features/`: actionable implementation PRDs with acceptance criteria.

## Required Status Lifecycle
- Feature PRD: `Backlog -> In Progress -> In Review -> Done`
- Epic: `Planned -> In Progress -> Done`
- Roadmap: kept current as epic statuses change.

## Branch and PR Rules
- One feature PRD = one branch = one PR.
- Epic IDs use `EPIC-XXXX`; feature IDs use `FEAT-XXXX`.
- Branch naming: `feat/FEAT-XXXX-<slug>`.
- First commit on the branch is the scaffolding docs commit (status flip + links).
- PR starts in draft until acceptance criteria and tests pass.

## Proposed Skill or Agent Command
Create a command or skill named `/start-feature` that:
1. Validates a target feature PRD exists in `docs/features/`.
2. Reads linked epic and architecture docs.
3. Creates `feat/FEAT-XXXX-<slug>` branch from the selected feature PRD.
4. Updates the feature PRD status to `In Progress`.
5. Creates a scaffolding docs commit.
6. Opens a draft PR with acceptance criteria checklist.

Create a companion `/close-feature` that:
1. Runs required tests.
2. Validates PRD acceptance criteria.
3. Updates feature status to `In Review` or `Done`.
4. Posts merge notes and next feature suggestion.

## Suggested AGENTS.md Guardrails
- No implementation starts without a feature PRD in `docs/features/`.
- Every feature PRD links to one epic and relevant architecture docs.
- Architecture docs are required for cross-cutting or irreversible changes.
- Only the active epic should be fully documented; future epics stay in the roadmap until they become current.
- After merge, update feature, epic, and roadmap statuses in the same PR or immediate follow-up.
