# FEAT-0003 — Configuration System

**Epic:** [EPIC-0000 — Project Foundation](../epics/EPIC-0000-foundation.md)
**Status:** Not started
**Dependencies:** FEAT-0001 (needs `yaml.v3`), FEAT-0002 (imports `types.ScrubMode`, `types.PrivacyTier`)
**Dependents:** E3 (Privacy Pipeline reads scrubbing config), E4 (Write Path reads storage config), E7 (Capture reads capture config), E9 (`opax init` generates default config), E11 (Hooks read trailers config)

---

## Problem

Every downstream feature needs configuration: the privacy pipeline needs scrub mode and detector lists, the capture engine needs enabled sources, the write path needs trailer settings, and the storage layer needs retention thresholds. Without a centralized config system, each package would invent its own config format, file location, and validation rules.

The config system must support three tiers: SDK defaults (always present, hardcoded), team config (committed to the repo, shared), and personal config (user-local, never committed). This hierarchy lets teams enforce baseline settings while individual developers customize their environment.

---

## Design

### Package

`internal/config/` — depends on `internal/types` (for `ScrubMode`, `PrivacyTier` enums), `gopkg.in/yaml.v3`, and stdlib only.

### Files

| File | Contents |
|---|---|
| `internal/config/config.go` | All types, `Load`, `Default`, `Validate`, merge logic |
| `internal/config/config_test.go` | Table-driven tests |

### Config File Locations

| Tier | Path | Committed | Purpose |
|---|---|---|---|
| SDK defaults | Hardcoded in Go | N/A | Sensible baseline, always present |
| Team | `{repoRoot}/.opax/config.yaml` | Yes | Shared team settings |
| Personal | `~/.config/opax/config.yaml` | No | Individual overrides |

Missing files are silently skipped — not an error. An empty file is valid (all defaults apply).

---

## Specification

### OpaxConfig (Top-Level)

```go
type OpaxConfig struct {
    Privacy  PrivacyConfig  `yaml:"privacy"`
    Storage  StorageConfig  `yaml:"storage"`
    Capture  CaptureConfig  `yaml:"capture"`
    Trailers TrailersConfig `yaml:"trailers"`
}
```

### Privacy Section

Source: privacy.md YAML example and PrivacyMetadata type.

```go
type PrivacyConfig struct {
    Version      int                `yaml:"version"`
    Scrubbing    ScrubbingConfig    `yaml:"scrubbing"`
    DefaultTiers DefaultTiersConfig `yaml:"default_tiers"`
}

type ScrubbingConfig struct {
    Mode             types.ScrubMode `yaml:"mode"`
    BuiltinDetectors []string        `yaml:"builtin_detectors"`
    CustomPatterns   []PatternConfig `yaml:"custom_patterns"`
    SourceFiles      []string        `yaml:"source_files"`
    Entropy          EntropyConfig   `yaml:"entropy"`
    Allowlist        []string        `yaml:"allowlist"`
}

type PatternConfig struct {
    Name        string `yaml:"name"`
    Pattern     string `yaml:"pattern"`
    Description string `yaml:"description"`
}

type EntropyConfig struct {
    Enabled   bool    `yaml:"enabled"`
    Threshold float64 `yaml:"threshold"`
    MinLength int     `yaml:"min_length"`
}

type DefaultTiersConfig struct {
    Session  types.PrivacyTier `yaml:"session"`
    Workflow types.PrivacyTier `yaml:"workflow"`
    Action   types.PrivacyTier `yaml:"action"`
}
```

### Storage Section

Source: storage.md retention tiers.

```go
type StorageConfig struct {
    Retention RetentionConfig `yaml:"retention"`
}

type RetentionConfig struct {
    Hot             string `yaml:"hot"`
    Warm            string `yaml:"warm"`
    ComplianceFloor string `yaml:"compliance_floor"`
}
```

Retention values are duration strings: `"30d"`, `"90d"`, `"3y"`. A `ParseDuration` helper converts these to `time.Duration` or returns an error for invalid formats. Supported units: `d` (days), `w` (weeks), `m` (months, 30 days), `y` (years, 365 days).

### Capture Section

```go
type CaptureConfig struct {
    EnabledSources []string          `yaml:"enabled_sources"`
    LastCapture    map[string]string `yaml:"last_capture"`
}
```

`EnabledSources` lists agent platforms to capture from (e.g., `["claude-code", "codex"]`). `LastCapture` maps source name to an ISO 8601 timestamp of the last successful capture — used by E7 to avoid re-reading already-processed sessions.

### Trailers Section

```go
type TrailersConfig struct {
    Enabled bool   `yaml:"enabled"`
    Prefix  string `yaml:"prefix"`
}
```

`Enabled` defaults to `true`. `Prefix` defaults to `"Opax-"`, matching architecture invariant #8 (`Opax-Save` trailer).

### SDK Defaults

The `Default()` function returns this configuration. These values apply when no config file exists or when a config file omits a field.

```yaml
privacy:
  version: 1
  scrubbing:
    mode: redact
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
  default_tiers:
    session: team
    workflow: team
    action: team

storage:
  retention:
    hot: 30d
    warm: 90d
    compliance_floor: ""

capture:
  enabled_sources: []
  last_capture: {}

trailers:
  enabled: true
  prefix: "Opax-"
```

---

## Config Loading & Merge

### Load Function

```go
// Load reads config from the hierarchy and returns the merged, validated result.
// repoRoot is the path to the git repository root (containing .opax/).
func Load(repoRoot string) (*OpaxConfig, error)
```

**Load order:**
1. Start with `Default()`
2. Read `{repoRoot}/.opax/config.yaml` — merge over defaults (if file exists)
3. Read `~/.config/opax/config.yaml` — merge over result (if file exists)
4. Run `Validate()` on the merged result
5. Return merged config or first validation error

### Merge Strategy

Deep merge at the struct level with these rules:

| Field type | Merge behavior |
|---|---|
| Scalar (`string`, `int`, `float64`, `bool`) | Override: non-zero value in higher-priority file wins |
| Slice (`[]string`, `[]PatternConfig`) | Replace: higher-priority file's slice replaces entirely (not append) |
| Map (`map[string]string`) | Merge: keys from higher-priority file override or add; keys only in lower-priority file are preserved |
| Struct | Recursive: merge each field individually |

**Why slices replace:** If a team config sets `builtin_detectors: [aws_keys, github_tokens]` and a personal config sets `builtin_detectors: [aws_keys]`, the personal config intends to disable `github_tokens`. Appending would defeat this purpose. Replace semantics give the higher-priority file full control over list contents.

### Strict Parsing

Use `yaml.v3`'s `Decoder.KnownFields(true)` to reject unknown keys during decode. This catches typos (`scrub_mode` instead of `mode`) and prevents silent misconfiguration.

Error message format: `config: {filepath}: unknown field "{key}"`.

---

## Validation

### Validate Function

```go
// Validate checks an OpaxConfig for invalid values.
// Returns the first error found.
func Validate(cfg *OpaxConfig) error
```

### Validation Rules

| Rule | Field | Check |
|---|---|---|
| Enum validity | `scrubbing.mode` | Must be `redact`, `reject`, or `warn` |
| Enum validity | `default_tiers.session` | Must be `public`, `team`, or `private` |
| Enum validity | `default_tiers.workflow` | Must be `public`, `team`, or `private` |
| Enum validity | `default_tiers.action` | Must be `public`, `team`, or `private` |
| Required field | `privacy.version` | Must be > 0 |
| Regex compilation | `custom_patterns[*].pattern` | Each must compile via `regexp.Compile` |
| Regex compilation | `allowlist[*]` | Each entry that looks like a regex must compile |
| Pattern name | `custom_patterns[*].name` | Must be non-empty |
| Duration format | `storage.retention.hot` | Must parse as a duration if non-empty |
| Duration format | `storage.retention.warm` | Must parse as a duration if non-empty |
| Duration format | `storage.retention.compliance_floor` | Must parse as a duration if non-empty |
| Entropy range | `scrubbing.entropy.threshold` | Must be > 0 if entropy enabled |
| Length range | `scrubbing.entropy.min_length` | Must be > 0 if entropy enabled |
| Trailer prefix | `trailers.prefix` | Must end with `"-"` if non-empty |

Error message format: `config: validate: {section}.{field}: {reason}`.

### Duration Parsing

```go
// ParseDuration parses a human-readable duration string.
// Supported formats: "30d", "12w", "3m", "1y"
// Units: d=day(24h), w=week(7d), m=month(30d), y=year(365d)
func ParseDuration(s string) (time.Duration, error)
```

---

## Edge Cases

- **Empty config file** — a file containing only `---` or whitespace is valid. All defaults apply. No error.
- **Partial config** — a file that sets only `privacy.scrubbing.mode: reject` is valid. Everything else inherits from defaults.
- **Missing `.opax/` directory** — not an error. Team config is simply absent.
- **Unreadable config file** — permission errors bubble up as `config: {filepath}: {os error}`.
- **Config file is a directory** — return a clear error, not a panic.
- **Zero-value booleans** — `trailers.enabled: false` must be distinguishable from "not set." The merge logic handles this by tracking which fields were explicitly set in each file. This may require a separate "was this field present?" tracking mechanism (e.g., pointer fields or a merge-mask approach).
- **Custom pattern with invalid regex** — `Validate` returns an error with the pattern name and the regex compilation error message. The config does not partially load.
- **Allowlist regex vs literal** — entries in `allowlist` can be either exact strings or regex patterns. Convention: if the string contains regex metacharacters (`*`, `+`, `?`, `[`, `(`, `{`, `\`), treat it as a regex; otherwise, treat it as a literal match. Both are compiled at validation time.

---

## Acceptance Criteria

- [ ] `Default()` returns a fully populated config matching the SDK defaults
- [ ] `Load()` returns defaults when no config files exist
- [ ] `Load()` merges team config over defaults correctly
- [ ] `Load()` merges personal config over team config over defaults correctly
- [ ] Scalar override: personal `mode: reject` overrides team `mode: redact`
- [ ] Slice replace: personal `builtin_detectors: [aws_keys]` replaces team's full list
- [ ] Map merge: personal `last_capture` keys merge with team's keys
- [ ] Unknown YAML keys cause `Load()` to return an error with file path and key name
- [ ] Invalid `scrubbing.mode` value causes `Validate()` to return an error
- [ ] Invalid `default_tiers` value causes `Validate()` to return an error
- [ ] Invalid regex in `custom_patterns` causes `Validate()` to return an error including pattern name
- [ ] Missing config files are silently skipped
- [ ] Empty config file is valid (all defaults apply)
- [ ] `ParseDuration("30d")` returns 30 * 24 hours
- [ ] `ParseDuration("3y")` returns 365 * 3 * 24 hours
- [ ] `ParseDuration("invalid")` returns an error
- [ ] `Validate()` rejects `privacy.version: 0`
- [ ] `Validate()` rejects `trailers.prefix: "NoTrailingDash"`
- [ ] Error messages include file path and field name
- [ ] Table-driven tests, stdlib `testing` only

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestDefault` | SDK defaults completeness | All fields populated with expected values |
| `TestLoadNoFiles` | Missing files behavior | Returns defaults, no error |
| `TestLoadTeamOnly` | Single-file merge | Team values override defaults |
| `TestLoadTeamAndPersonal` | Two-file merge | Personal overrides team overrides defaults |
| `TestMergeScalarOverride` | Scalar merge semantics | Higher-priority value wins |
| `TestMergeSliceReplace` | Slice merge semantics | Higher-priority slice replaces entirely |
| `TestMergeMapMerge` | Map merge semantics | Keys merge, higher-priority wins on conflict |
| `TestStrictUnknownKey` | Strict YAML parsing | Unknown key returns error with file path |
| `TestValidateMode` | Enum validation | Invalid mode rejected, valid modes accepted |
| `TestValidateTiers` | Enum validation | Invalid tier rejected, valid tiers accepted |
| `TestValidateCustomPattern` | Regex compilation | Invalid regex rejected with pattern name in error |
| `TestValidateRetention` | Duration format | Valid durations accepted, invalid rejected |
| `TestValidateVersion` | Required field | version: 0 rejected |
| `TestValidateTrailerPrefix` | Format constraint | Must end with "-" |
| `TestValidateEntropy` | Range check | threshold ≤ 0 rejected when enabled |
| `TestParseDuration` | Duration parsing (table-driven) | All units parse correctly, invalid formats error |
| `TestLoadUnreadableFile` | Permission error | Returns error with file path |
| `TestEmptyConfigFile` | Edge case | All defaults apply, no error |
| `TestPartialConfig` | Partial override | Only specified fields override |
| `TestBooleanFalseOverride` | Zero-value bool merge | `enabled: false` is respected, not treated as "unset" |
