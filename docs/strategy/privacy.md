# Opax — Privacy & Security Spec

**Version:** 1.0.0-draft
**Date:** March 16, 2026
**Companion to:** Opax PRD v4

---

## Overview

Agent sessions routinely encounter API keys, database credentials, authentication tokens, and other secrets. Since Opax stores session transcripts and context artifacts in git — a system designed for permanent, distributed storage — the privacy system must prevent sensitive content from ever being persisted, and control who can read stored data.

The privacy system is a **layered pipeline** with a non-negotiable ordering: scrubbing always precedes encryption. Secrets must never be stored even in encrypted form, because encryption keys can be compromised, key rotation doesn't retroactively protect historical data, and the attack surface is smaller when secrets simply don't exist in the data layer.

---

## Pipeline Architecture

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
┌─────────────────┐
│ Tier Assignment │  ← Classify as public/team/private
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Encryption    │  ← Phase 1: age encryption with per-tier recipients
└────────┬────────┘
         │
         ▼
    Git storage
```

Phase 0 ships steps 1–3 (detection, scrubbing, classification). Phase 1 adds step 4 (encryption). The `PrivacyMetadata` type is present on all artifacts from Phase 0 so Phase 1 slots in without rearchitecting.

---

## Phase 0: Secret Scrubbing

Ships with the core SDK and MCP server. Runs on all content before it's written to git.

### Configuration: `privacy.yaml`

Located at `.opax/privacy.yaml` (committed, team-shared) or `~/.config/opax/privacy.yaml` (personal overrides).

```yaml
privacy:
  version: 1

  scrubbing:
    mode: redact  # redact | reject | warn
    # redact: replace detected secrets with [REDACTED:{type}]
    # reject: refuse to store content containing secrets
    # warn: store content but log a warning

    # Built-in detectors (enabled by default)
    builtin_detectors:
      - aws_keys
      - github_tokens
      - jwt_tokens
      - private_keys
      - connection_strings
      - generic_api_keys

    # Custom detection patterns
    custom_patterns:
      - name: internal_service_token
        pattern: "svc_[a-zA-Z0-9]{32}"
        description: "Internal service authentication token"

      - name: database_url
        pattern: "postgres://[^\\s]+"
        description: "PostgreSQL connection string"

    # Source file scanning — detect secrets defined in these files
    # and scrub them from transcripts even if patterns don't match
    source_files:
      - .env
      - .env.local
      - .env.production
      - "**/.env*"

    # Entropy-based detection
    entropy:
      enabled: true
      threshold: 4.5  # Shannon entropy threshold
      min_length: 20   # Minimum string length to check

    # Allowlist — strings that match patterns but are not secrets
    allowlist:
      - "EXAMPLE_KEY_DO_NOT_USE"
      - "sk-test-.*"  # Test/example keys

  # Privacy tier defaults for new artifacts
  default_tiers:
    context: public
    session: team
    workflow: team
    action: team
```

### Detection Pipeline

1. **Source file scanning:** Read `.env` and other configured source files. Extract key-value pairs. Build a lookup table of known secret values. Any exact match in content is flagged regardless of pattern.

2. **Pattern matching:** Run all enabled detectors (built-in + custom) against content. Built-in detectors cover common formats: AWS access keys (`AKIA[0-9A-Z]{16}`), GitHub tokens (`ghp_`, `gho_`, `ghs_`, `github_pat_`), JWTs (three dot-separated base64 segments), PEM private keys, connection strings with credentials, and generic high-entropy strings matching API key patterns.

3. **Entropy analysis:** For strings longer than `min_length` that don't match any pattern, compute Shannon entropy. Flag strings above the threshold as potential secrets. This catches novel secret formats that patterns miss.

4. **Allowlist filtering:** Remove false positives. Allowlist entries can be exact strings or regex patterns.

### Scrubbing Behavior

**Redact mode (default):** Replace detected secrets with `[REDACTED:{detector_name}]`. The original content is never stored. Example:

```
Input:  "Set GITHUB_TOKEN=ghp_abc123def456... in your environment"
Output: "Set GITHUB_TOKEN=[REDACTED:github_token] in your environment"
```

**Reject mode:** Refuse to write the artifact. Return an error indicating which detectors fired and approximate positions. The caller can clean the content and retry.

**Warn mode:** Write the content as-is but log a warning with detector details. Intended for development/debugging only — not recommended for shared repos.

### PrivacyMetadata Type

Present on all artifact `metadata.json` from Phase 0. This is the scaffold that Phase 1 encryption builds on.

```typescript
interface PrivacyMetadata {
  tier: 'public' | 'team' | 'private';
  scrubbed: boolean;
  scrub_version: string;      // version of scrubbing rules applied
  scrub_detectors?: string[]; // which detectors fired (if any)
  encrypted: boolean;         // always false in Phase 0
  encryption_recipients?: string[]; // populated in Phase 1
}
```

---

## Phase 1: Encryption at Rest

Adds encryption using `age` (Actually Good Encryption). Content is encrypted before being written to git, decryptable only by authorized recipients.

### Why `age`

- Simple, audited, no configuration complexity (unlike GPG)
- Supports multiple recipients per file
- X25519 key exchange — small keys, fast operations
- File format is well-specified and stable
- CLI tool and Go/Rust libraries available
- Recipients are public keys — no web of trust, no key servers

### Tier-Based Encryption

Each privacy tier maps to a set of `age` recipients (public keys):

```yaml
privacy:
  encryption:
    enabled: true
    tiers:
      public:
        encrypted: false  # public data is not encrypted
      team:
        recipients:
          - age1team1...  # team shared key
          - age1team2...  # backup key
      private:
        recipients:
          - age1user1...  # individual developer key only
```

**Key management:**
- Team keys stored in `.opax/keys/team.pub` (committed to repo)
- Private keys stored in `~/.config/opax/keys/` (never committed)
- Key rotation: new recipients added to tier config; old data re-encrypted on next compaction cycle

### File-Level Encryption

Only content files are encrypted. Metadata remains plaintext to preserve git delta compression and enable filtered queries without decryption.

```
oa/memory/context/ctx_01JQXYZ.../
├── metadata.json         # PLAINTEXT — title, tags, timestamps, PrivacyMetadata
├── content.md.age        # ENCRYPTED — actual content
├── encryption.json       # recipient list, algorithm, encrypted-at timestamp
└── related/
    └── refs.json         # PLAINTEXT — just IDs
```

**encryption.json:**

```json
{
  "version": 1,
  "algorithm": "age-x25519",
  "tier": "team",
  "recipients": ["age1team1...", "age1team2..."],
  "encrypted_at": "2026-03-13T10:30:00Z"
}
```

### Git Compression Implications

Encrypted content is statistically random, which defeats both of git's compression layers:

- **zlib compression** on loose objects: ~0% compression on encrypted data (vs. ~60-70% on plaintext)
- **Delta compression** in packfiles: encrypted files share no byte sequences between versions, so git stores full copies instead of deltas

**Impact:** Encrypted artifacts use approximately 3–5x more storage than plaintext equivalents. For a team generating 36 GB/year of plaintext Opax data, full encryption would push storage to ~100-180 GB/year.

**Mitigation — hybrid approach:** Encrypt only content files (`.md`, `.patch`, transcripts). Leave `metadata.json` in plaintext. Metadata participates in delta compression normally (metadata changes are small diffs between versions). Content files are the bulk of the data but also the most sensitive.

**Tradeoff:** Plaintext metadata means titles, tags, timestamps, file paths, and agent identifiers are visible to anyone with repo access. This is acceptable for the `team` tier (all team members have repo access anyway) but may not be for `private` tier content where even the existence of an artifact should be hidden. For `private` tier: encrypt `metadata.json` too, accept the compression penalty.

### Decryption in the SDK

The SDK attempts decryption transparently when reading encrypted artifacts. If the current user's key is not in the recipient list, the SDK returns the metadata (if plaintext) with a flag indicating the content is encrypted and inaccessible. The SQLite materialized view stores metadata for all artifacts but only stores decrypted content for artifacts the current user can access.

---

## Configuration Hierarchy

Privacy settings merge with the following precedence (highest first):

1. Per-artifact override (set via SDK at write time)
2. `~/.config/opax/privacy.yaml` (personal overrides)
3. `.opax/privacy.yaml` (team-shared, committed)
4. SDK defaults (scrub mode: redact, default tiers as above)

---

## Integration Points

**MCP Server:** The `save` tool runs content through the scrubbing pipeline before writing. If scrubbing mode is `reject` and secrets are detected, the tool returns an error message explaining what was found. Agents can then clean the content and retry.

**Session capture:** The `post-commit` hook captures session metadata. If transcript capture is enabled (via MCP or agent wrapping), the transcript is scrubbed before archiving.

**Workflows plugin:** Workflow logs (stage outcomes, gate approvals) are classified at the `team` tier by default. Executor stdout/stderr is scrubbed before being written to action logs.

**Studio:** The web UI respects encryption tiers. Encrypted artifacts show metadata but display "[Encrypted — requires key]" for content the current user can't decrypt.
