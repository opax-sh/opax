| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-006 — Execution as pluggable drivers

## Status
Accepted

## Context
Opax workflows need to dispatch work to agents. Agents run in many environments: local processes, Docker containers, GitHub Actions, cloud sandboxes (E2B, Codespaces), serverless functions, SSH remotes. The orchestration layer needs to work with all of them without coupling to any.

## Options Considered

### Option A — Built-in execution for each environment
- Pros: tight integration, optimized per environment.
- Cons: core grows with every new environment. Opax starts competing with execution platforms.

### Option B — Pluggable execution drivers with a common contract
- Pros: the orchestrator defines what happens and in what order. Drivers handle where it runs. Adding a new environment means writing a driver, not changing the orchestrator. A single workflow can span multiple environments.
- Cons: driver contract must be general enough for all environments while specific enough to be useful.

## Decision
Option B. Execution is removed from core and implemented as pluggable drivers.

Driver contract: given a branch, a context bundle (Opax memory), and a task spec, spin up an agent session and signal completion. The orchestrator doesn't manage compute — it manages the workflow.

Phase 0: no executors (manual). Phase 1: local process driver, Docker driver. Phase 2: remote drivers (E2B, GitHub Actions, Cloud Run).

## Consequences

### Positive
- Opax never competes with execution platforms
- New environments require only a new driver
- Users pay their own execution costs; Opax manages coordination

### Negative
- Driver contract design is critical — too abstract and drivers are painful to write, too specific and they don't generalize
- Each driver needs its own testing strategy

### Follow-up
- Driver interface definition
- Local process driver implementation
- Docker driver implementation
