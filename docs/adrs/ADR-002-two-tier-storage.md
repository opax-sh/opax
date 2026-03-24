| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-002 — Two-tier storage model

## Status
Accepted

## Context
Agent sessions produce both small metadata (IDs, timestamps, agent info) and large content (full transcripts, diffs, action logs). Storing everything in git bloats the repository. A 5-developer team producing ~100 MB/day of transcript data would make the repo unusable within weeks.

## Options Considered

### Option A — Everything in git
- Pros: simple, single storage layer.
- Cons: repository bloat. Git is not designed for large binary blobs. Clone times degrade. Hosting providers may reject large repos.

### Option B — Metadata in git, bulk content in content-addressed storage
- Pros: git footprint stays small (~2-5 MB/day). Bulk content referenced by SHA-256 hash from git metadata — tamper-verifiable via hash comparison. Content stored at `.git/opax/content/`, invisible to normal git operations.
- Cons: two storage layers to manage. Content not distributed by default git operations.

### Option C — External storage only (S3, etc.)
- Pros: unlimited scale.
- Cons: requires infrastructure. Loses zero-dependency local operation.

## Decision
Option B. Metadata and references live in git on the `opax/v1` orphan branch. Bulk content lives in CAS at `.git/opax/content/`, referenced by SHA-256 hash. Threshold: inline in git below 4 KB, CAS at 4 KB and above.

Tiered retention:
- Hot (0-30d): same repo, consolidated branch, SQLite
- Warm (30-90d): git remote (archive repo), fetch on demand
- Cold (90+d): git bundles on object storage

## Consequences

### Positive
- Git repository stays small and fast to clone
- Content integrity verifiable via hash comparison
- Retention tiers map to existing git remote infrastructure

### Negative
- `opax push` / `opax pull` must sync both git refs and CAS content
- Content not available on clone without explicit Opax sync

### Follow-up
- CAS sync protocol design
- Retention policy configuration
