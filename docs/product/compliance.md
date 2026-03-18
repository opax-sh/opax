# Opax — Compliance Framework

**Version:** 1.0.0-draft
**Date:** March 16, 2026
**Companion to:** Opax PRD v2.0.0

---

## Overview

Opax's compliance positioning is "compliance for free" — the audit trail is a natural byproduct of using the product for its primary purpose (cross-platform memory and workflow orchestration). Developers don't do extra work; compliance officers get structured evidence. This is a genuinely differentiated position that no competing tool in the agent infrastructure space currently offers.

Git's cryptographic properties — content-addressed hashing, immutable history, distributed replication — provide tamper-evidence and provenance guarantees that purpose-built compliance tools struggle to match.

---

## 1. Regulatory Landscape

Four layers of regulation are converging, each with different requirements but significant overlap in what they demand.

### 1.1 EU AI Act

**Enforcement date:** August 2, 2026 (high-risk system requirements). Potential delay to December 2027 via Digital Omnibus package, but the European Commission has rejected blanket delays. Plan for August 2026.

**Penalties:** Up to €35 million or 7% of global annual turnover.

**Relevant obligations (for AI systems in development workflows):**

| Article | Requirement | Opax Mapping |
|---|---|---|
| Article 9 | Risk management system | Risk classification metadata on artifacts |
| Article 10 | Data governance | Source tracking on all session archives |
| Article 11 | Technical documentation | Session archives + workflow logs |
| Article 12 | Record-keeping (automatic logging) | Session archives, commit notes, workflow events — **strongest card** |
| Article 13 | Transparency | Agent identification via notes/trailers |
| Article 14 | Human oversight | Workflow gates with approval records |
| Article 15 | Accuracy, robustness, cybersecurity | Eval scores, test results as git notes |
| Article 26 | Deployer obligations | Provenance chain from prompt to production |

**Article 12 is the anchor.** It requires automatic logging of AI system events throughout the lifecycle. Session archives + commit notes + workflow event logs provide exactly this. Git's cryptographic integrity provides tamper-evidence without a separate compliance layer.

### 1.2 NIST AI Risk Management Framework (AI RMF)

**Status:** Voluntary framework, but Colorado AI Act creates a legal safe harbor for organizations demonstrating NIST alignment.

**Relevant functions:**

| Function | Requirement | Opax Mapping |
|---|---|---|
| GOVERN | Policies and procedures for AI risk | Workflow definitions as policy-as-code |
| MAP | Identify and categorize AI risks | Risk classification metadata |
| MEASURE | Assess AI risks quantitatively | Eval scores, test results |
| MANAGE | Respond to and mitigate AI risks | Gate approvals, human oversight records |

### 1.3 ISO 42001 (AI Management System)

**Status:** International standard for AI management. Certification becoming a procurement requirement for enterprise buyers.

**Relevant controls:**

| Control | Requirement | Opax Mapping |
|---|---|---|
| 6.1.2 | AI risk assessment | Risk metadata + eval history |
| 7.5 | Documented information | Session archives, workflow logs |
| 8.2 | AI system lifecycle processes | Workflow execution history |
| 8.4 | AI system operation and monitoring | Real-time workflow status, gate history |
| 9.1 | Monitoring, measurement, analysis | Eval trends, test result history |

### 1.4 US State Laws

**Colorado AI Act (SB 24-205):** Effective February 1, 2026. Requires deployers of high-risk AI to implement risk management policies and maintain records. **Critical: demonstrating NIST AI RMF compliance is an affirmative legal defense.** This makes NIST alignment a concrete legal asset, not just a nice-to-have.

**3-year retention requirement** conflicts with the PRD's default compaction policy (30d individual / 90d summary). Compliance mode must override compaction.

**California, Illinois, others:** Additional state-level AI regulation is emerging. Most align with federal NIST guidance. Opax's approach (capture everything, let retention policies be configurable per compliance regime) is general enough to accommodate.

---

## 2. Data Model Additions

The following metadata fields are added to support compliance requirements. All are optional — non-compliance users don't need to populate them.

### 2.1 Risk Classification

Added to all artifact `metadata.json`:

```json
{
  "compliance": {
    "risk_level": "minimal | limited | high | unacceptable",
    "risk_category": "development-tool | code-generation | decision-support | autonomous-action",
    "frameworks": ["eu-ai-act", "nist-ai-rmf", "iso-42001"],
    "retention_override": "3y",
    "classification_date": "2026-03-13T10:30:00Z",
    "classified_by": "auto | human"
  }
}
```

**Risk level mapping for agent development:**
- `minimal`: Agent used for code formatting, linting, simple refactors
- `limited`: Agent used for code generation with human review
- `high`: Agent making autonomous decisions affecting production systems
- `unacceptable`: Prohibited use cases (not applicable to development workflows in practice)

### 2.2 Human Oversight Records

Extended gate record in workflow stages:

```json
{
  "gate": {
    "type": "human-approval",
    "status": "approved",
    "reviewer": {
      "id": "developer-id",
      "type": "human | ai-assisted | fully-automated",
      "role": "maintainer | reviewer | approver"
    },
    "approved_at": "2026-03-13T11:20:00Z",
    "review_duration_seconds": 300,
    "artifacts_reviewed": ["ses_01JQXYZ...", "diff:abc1234"],
    "comment": "Reviewed implementation, approved for merge."
  }
}
```

The `reviewer.type` field is critical for Article 14 compliance — it distinguishes genuine human oversight from rubber-stamp automation.

### 2.3 Incident Records

New git note namespace for compliance incidents:

**Namespace:** `refs/opax/notes/incidents`

```json
{
  "version": 1,
  "incident_id": "inc_01JQXYZ...",
  "type": "security | quality | compliance | safety",
  "severity": "low | medium | high | critical",
  "description": "Agent committed code with known vulnerability",
  "related_commits": ["abc1234"],
  "related_sessions": ["ses_01JQXYZ..."],
  "resolution": {
    "action": "reverted | patched | accepted-risk",
    "resolved_by": "developer-id",
    "resolved_at": "2026-03-14T09:00:00Z"
  },
  "reported_at": "2026-03-13T15:00:00Z"
}
```

---

## 3. Retention Policies

### Conflict Resolution

The PRD's default compaction policy (30d individual / 90d summary) conflicts with regulatory retention requirements:

| Framework | Retention Requirement |
|---|---|
| EU AI Act Article 12 | Lifetime of the AI system + reasonable period |
| Colorado AI Act | 3 years |
| ISO 42001 | Defined by the organization's AIMS |
| NIST AI RMF | Organization-defined |

### Compliance Mode

When compliance mode is enabled, retention policies are overridden:

```yaml
compliance:
  mode:
    enabled: true
    frameworks:
      - eu-ai-act
      - nist-ai-rmf
    retention:
      minimum: 3y              # longest applicable requirement
      compaction_allowed: true  # can compact, but not delete
      archive_destination: opax-archive  # separate git repo for old data
```

**Behavior in compliance mode:**
- Individual session archives compact into daily summaries after 30d (as normal)
- Daily summaries are **never deleted** — moved to the archive repo after 90d
- Session archives are retained for the full compliance period
- Workflow logs, gate records, and notes are retained for the full compliance period
- `opax storage compact` respects the minimum retention floor

### Archive Repos

For teams generating >36 GB/year (see *Storage & Scaling Spec*), compliance retention requires moving old data to a separate archive repository rather than keeping it in the working repo.

The archive repo is a standard git repo containing only Opax orphan branches older than the compaction threshold. `opax storage archive` moves branches to the archive repo and updates the SQLite index to reference the archive. Queries span both repos transparently (the SDK checks the archive repo when records aren't in the primary).

---

## 4. Compliance Reporting

### Evidence Packages

`opax compliance report` generates a structured evidence package for a specific compliance framework:

**EU AI Act Article 12 report:**
- Total session count and time range
- Agent identification summary (which agents, which models, session counts)
- Human oversight summary (gate approvals, review counts, reviewer breakdown by type)
- Test and eval results summary
- Incident records
- Data retention attestation

**NIST AI RMF alignment report:**
- GOVERN: workflow definitions as documented policies
- MAP: risk classification distribution across artifacts
- MEASURE: eval score trends, test result trends
- MANAGE: gate approval rates, incident resolution times

These reports are generated from the SQLite materialized view. They reference git commit hashes for every data point, providing cryptographic verifiability. Reports can be exported as JSON (for automated processing) or Markdown (for human review).

### Compliance-as-Code (Future)

Express framework requirements as executable policies that the workflows plugin enforces:

```yaml
# .opax/compliance/eu-ai-act.yaml
compliance:
  framework: eu-ai-act
  policies:
    - name: human-review-required
      description: "All agent-produced code must have human review before merge"
      rule:
        workflow_stage: merge
        requires_gate: true
        gate_reviewer_type: human

    - name: session-recording
      description: "All agent sessions must be archived"
      rule:
        on: session_end
        requires: session_archive

    - name: eval-minimum
      description: "Agent code must score above threshold on evals"
      rule:
        on: eval_complete
        requires:
          eval_score:
            correctness: ">= 0.8"
```

This is a Phase 3 feature. The compliance config format is owned by a future `@opax/plugin-compliance` plugin, not the core spec.

---

## 5. Strategic Positioning

### "Compliance for Free"

The pitch: "Every `sessions.archive()` call is simultaneously an Article 12 record-keeping artifact. Every workflow gate approval is simultaneously an Article 14 human oversight record. Developers don't do extra work; compliance officers get structured evidence."

This inverts the traditional GRC (Governance, Risk, Compliance) tool model where organizations adopt new workflows and manually feed data into compliance systems. Opax produces compliance data as a natural byproduct of its primary value proposition.

### Colorado Safe Harbor as Enterprise GTM

Colorado's affirmative legal defense for NIST-aligned organizations makes compliance a concrete legal asset. An Opax compliance report demonstrating NIST AI RMF alignment is not just documentation — it's legal protection. This is a much easier enterprise sale than "nice-to-have audit trails."

### Timeline Advantage

The August 2026 EU AI Act enforcement creates time-bound urgency that no competitor currently addresses with git-level provenance guarantees. Opax's path runs through that window: ship fast, ship the compliance reporting before August 2026, and position as the only tool that gives you Article 12 compliance by default.

---

## 6. Implementation Priority

| Priority | Capability | Phase |
|---|---|---|
| P0 | `hygiene` metadata on session/save artifacts | Phase 0 |
| P0 | Secret scrubbing pipeline | Phase 0 |
| P0 | Session archive structure (Article 12 foundation) | Phase 0 |
| P1 | Risk classification metadata | Phase 1 |
| P1 | Human oversight record extensions | Phase 1 |
| P1 | Configurable retention policies with compliance floor | Phase 1 |
| P2 | `opax compliance report` (EU AI Act, NIST) | Phase 2 |
| P2 | Incident records namespace | Phase 2 |
| P3 | Compliance-as-code policies | Phase 3 |
| P3 | ISO 42001 alignment report | Phase 3 |
