# Opax — Hygiene Spec (Secret Scrubbing)

**Version:** 1.0.0-draft  
**Date:** March 17, 2026  
**Companion to:** Opax PRD v2.0.0

---

## Overview

Agent sessions routinely encounter API keys, database credentials, authentication tokens, and other secrets. Opax stores session transcripts and related bulk content in git-backed storage. The **hygiene** system ensures sensitive literal secrets are not persisted: detection and scrubbing run on all content **before** any write.

**Invariant:** scrub before any future encryption step. Secrets must never be stored, even in encrypted form. (Encryption and authorization are out of scope for the current phase; they will be specified separately before ship.)

---

## Pipeline (Phase 0)

```
Content enters Opax
       │
       ▼
┌─────────────────┐
│ Secret Detection │  ← Pattern matching, entropy analysis, source file scanning
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Scrubbing     │  ← Redact, reject, or warn based on config
└────────┬────────┘
         │
         ▼
    Git / CAS storage
```

---

## Configuration

Hygiene settings live in `config.yaml`: `.opax/config.yaml` (repo-shared) and `~/.config/opax/config.yaml` (personal overrides). See [FEAT-0003](../features/FEAT-0003-configuration-system.md).

```yaml
hygiene:
  version: 1

  scrubbing:
    mode: redact  # redact | reject | warn
    builtin_detectors:
      - aws_keys
      - github_tokens
      - jwt_tokens
      - private_keys
      - connection_strings
      - generic_api_keys
    custom_patterns: []
    source_files:
      - .env
      - .env.local
    entropy:
      enabled: true
      threshold: 4.5
      min_length: 20
    allowlist: []
```

### Scrubbing behavior

- **redact (default):** Replace detected secrets with `[REDACTED:{detector_name}]`.
- **reject:** Refuse to write; return an error.
- **warn:** Store as-is but log a warning (development only; not recommended for shared repos).

---

## Hygiene metadata on records

Present on session and save `metadata.json` (see [data-spec.md](data-spec.md)).

```json
{
  "scrubbed": true,
  "scrub_version": "1.0.0",
  "scrub_detectors": ["aws_keys"]
}
```

Field `scrub_detectors` is omitted when empty.

---

## Configuration hierarchy

1. Per-record overrides at write time (SDK)
2. `~/.config/opax/config.yaml`
3. `.opax/config.yaml`
4. SDK defaults

---

## Integration points

- **Write path:** All bulk content and inline payloads pass through the hygiene pipeline before CAS/git write.
- **MCP / hooks:** Same pipeline before persisting transcripts or summaries.

Future phases may add encryption and access control; this document will be extended or split when those ship.
