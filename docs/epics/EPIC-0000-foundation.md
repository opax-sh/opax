# EPIC-0000: Project Foundation

**Status:** Completed
**Version:** 1.0.0
**Date:** March 17, 2026
**Dependencies:** None (root of dependency graph)
**Dependents:** E1 (Git Plumbing), E2 (CAS), E3 (Hygiene Pipeline) — all Phase 0 epics

---

## Goal

Types, config, dependencies, and administrative coordination primitives. Nothing user-visible but every downstream epic (E1–E12) depends on this. After E0, the project compiles, tests pass, and the foundational abstractions are in place for git plumbing, storage, hygiene (scrubbing), and capture work to begin.

---

## FEAT-0001 — Add Dependencies

### Description

Add all Phase 0 dependencies to `go.mod`. These are chosen to maintain the single-binary, zero-runtime-deps, pure-Go constraint.

### Dependencies to Add

| Module | Purpose | Constraint |
|---|---|---|
| `github.com/go-git/go-git/v5` | Git plumbing operations (hash-object, mktree, commit-tree, update-ref) without touching working tree | Pure Go. Fallback: shell out to git if tree manipulation is too awkward (E1.3 risk) |
| `modernc.org/sqlite` | Embedded SQLite for materialized view at `.git/opax/opax.db` | Pure Go, no CGo. Must support FTS5 (verify early — E5.3 risk). Not `mattn/go-sqlite3` |
| `github.com/oklog/ulid/v2` | ULID generation for record IDs (`ses_`, `sav_`, plugin prefixes) | Monotonic, lexicographically sortable, embeds timestamp |
| `gopkg.in/yaml.v3` | YAML parsing for `config.yaml` | Strict mode, supports anchors/aliases |
| `github.com/mark3labs/mcp-go` | MCP Go SDK for stdio MCP server (E10) | If immature, thin custom JSON-RPC is feasible (E10 risk) |

### Acceptance Criteria

- [x] `go mod tidy` succeeds with no errors
- [x] `make build` produces `bin/opax`
- [x] `make test` passes (existing tests + new dependency smoke tests)
- [x] `make lint` (`go vet ./...`) reports no issues
- [x] No CGo dependencies introduced — `CGO_ENABLED=0 go build` succeeds
- [x] Each dependency has a smoke test verifying basic functionality (ULID generation, YAML parse, SQLite open/close)

---

## FEAT-0002 — Core Domain Types (`internal/types/`)

### Description

Define the foundational types that every downstream package imports. These are derived directly from the JSON schemas in data-spec.md and the hygiene metadata structure in hygiene.md. All types are pure data — no methods with side effects, no database access, no git operations.

### New File

`internal/types/types.go`

### Record ID Types

IDs follow the pattern `{type_prefix}_{ULID}`. ULIDs are lexicographically sortable with embedded timestamps.

| Type | Prefix | Example | Usage |
|---|---|---|---|
| `SessionID` | `ses_` | `ses_01JQXYZ1234567890ABCDEF` | Session archive records |
| `SaveID` | `sav_` | `sav_01JQXYZ1234567890ABCDEF` | Commit-anchored saves |

Each ID type should be a named `string` type with:
- A constructor that generates a new ID with ULID (using `crypto/rand` source)
- A `Validate() error` method that checks prefix and ULID format
- A `String() string` method
- A `Timestamp() time.Time` method extracting the embedded ULID timestamp

### Plugin ID Prefixes

Plugins register their own ID prefixes at load time (e.g., `wrk_` for workflows, `act_` for actions). The types package provides:
- A `PrefixRegistry` that tracks registered prefixes
- Registration function with collision detection (error if prefix already claimed)
- First-party prefixes (`ses_`, `sav_`) are pre-registered and cannot be overridden

### ULID Generation Helper

Wraps `oklog/ulid` with:
- `crypto/rand` as entropy source (not `math/rand`)
- Monotonic factory for generating IDs within the same millisecond
- Exported `NewULID() ulid.ULID` function

### Enums

```go
// AgentPlatform identifies which agent produced a session
type AgentPlatform string

const (
    AgentClaudeCode AgentPlatform = "claude-code"
    AgentCodex      AgentPlatform = "codex"
    AgentAider      AgentPlatform = "aider"
    AgentGoose      AgentPlatform = "goose"
    AgentUnknown    AgentPlatform = "unknown"
)

// ScrubMode determines how detected secrets are handled
type ScrubMode string

const (
    ScrubRedact ScrubMode = "redact"
    ScrubReject ScrubMode = "reject"
    ScrubWarn   ScrubMode = "warn"
)

// Attribution describes how a session was linked to a save
type Attribution string

const (
    AttrFileOverlap Attribution = "file_overlap"
    AttrTemporal    Attribution = "temporal"
)
```

### Metadata Structs

**SessionMetadata** — mirrors `sessions/{shard}/{id}/metadata.json` from data-spec.md:

```go
type SessionMetadata struct {
    ID              SessionID       `json:"id"`
    Version         int             `json:"version"`
    Agent           AgentPlatform   `json:"agent"`
    Model           string          `json:"model,omitempty"`
    Branch          string          `json:"branch,omitempty"`
    StartedAt       time.Time       `json:"started_at"`
    EndedAt         time.Time       `json:"ended_at,omitempty"`
    DurationSeconds int             `json:"duration_seconds,omitempty"`
    ExitCode        *int            `json:"exit_code,omitempty"`
    Commits         []string        `json:"commits,omitempty"`
    FilesChanged    int             `json:"files_changed,omitempty"`
    LinesAdded      int             `json:"lines_added,omitempty"`
    LinesRemoved    int             `json:"lines_removed,omitempty"`
    FilesTouched    []string        `json:"files_touched,omitempty"`
    ContentHash     string          `json:"content_hash,omitempty"`
    Hygiene         HygieneMetadata `json:"hygiene"`
    Tags            []string        `json:"tags,omitempty"`
}
```

**SaveMetadata** — mirrors `saves/{shard}/{id}/metadata.json` from data-spec.md:

```go
type SessionAttribution struct {
    ID          SessionID   `json:"id"`
    Attribution Attribution `json:"attribution"`
}

type SaveMetadata struct {
    ID            SaveID               `json:"id"`
    Version       int                  `json:"version"`
    CommitHash    string               `json:"commit_hash"`
    Sessions      []SessionAttribution `json:"sessions,omitempty"`
    Branch        string               `json:"branch,omitempty"`
    CreatedAt     time.Time            `json:"created_at"`
    FilesInCommit []string             `json:"files_in_commit,omitempty"`
    ContentHash   string               `json:"content_hash,omitempty"`
    Hygiene       HygieneMetadata      `json:"hygiene"`
}
```

**NoteContent** — mirrors the generic note format from data-spec.md:

```go
type NoteContent struct {
    CommitHash string          `json:"commit_hash"`
    Namespace  string          `json:"namespace"`
    Content    json.RawMessage `json:"content"`
    Version    int             `json:"version"`
}
```

**HygieneMetadata** — mirrors hygiene.md and data-spec.md:

```go
type HygieneMetadata struct {
    Scrubbed       bool     `json:"scrubbed"`
    ScrubVersion   string   `json:"scrub_version,omitempty"`
    ScrubDetectors []string `json:"scrub_detectors,omitempty"`
}
```

### Acceptance Criteria

- [x] All types compile and are importable from `internal/types`
- [x] `SessionID` and `SaveID` generate valid prefixed ULIDs
- [x] `Validate()` rejects malformed IDs (wrong prefix, invalid ULID)
- [x] `Timestamp()` correctly extracts the embedded time from a ULID-based ID
- [x] `PrefixRegistry` detects collisions — registering `ses_` twice returns an error
- [x] JSON round-trip: marshal a `SessionMetadata` → unmarshal → deep equal
- [x] JSON output matches the schemas in data-spec.md (field names, nesting)
- [x] `HygieneMetadata` defaults: `scrubbed: false`, empty optional fields omitted in JSON
- [x] All enum types have a `Valid() bool` method
- [x] Table-driven tests, stdlib `testing` only, no testify

---

## FEAT-0003 — Configuration System (`internal/config/`)

### Description

Implement the configuration system that every downstream package reads from. Config is hierarchical (SDK defaults → team file → personal file), strictly validated, and YAML-based.

### New File

`internal/config/config.go`

### OpaxConfig Struct

Top-level config with four sections, matching the YAML structure from hygiene.md:

```go
type OpaxConfig struct {
    Hygiene  HygieneConfig  `yaml:"hygiene"`
    Storage  StorageConfig  `yaml:"storage"`
    Capture  CaptureConfig  `yaml:"capture"`
    Trailers TrailersConfig `yaml:"trailers"`
}
```

### Hygiene Config Section

Derived from the YAML example in hygiene.md:

```go
type HygieneConfig struct {
    Version   int             `yaml:"version"`
    Scrubbing ScrubbingConfig `yaml:"scrubbing"`
}

type ScrubbingConfig struct {
    Mode             ScrubMode       `yaml:"mode"`             // redact | reject | warn
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
    Threshold float64 `yaml:"threshold"`  // default 4.5
    MinLength int     `yaml:"min_length"` // default 20
}
```

### Storage Config Section

```go
type StorageConfig struct {
    Retention RetentionConfig `yaml:"retention"`
}

type RetentionConfig struct {
    Hot             string `yaml:"hot"`              // e.g., "30d"
    Warm            string `yaml:"warm"`             // e.g., "90d"
    ComplianceFloor string `yaml:"compliance_floor"` // e.g., "3y"
}
```

### Capture Config Section

```go
type CaptureConfig struct {
    EnabledSources []string          `yaml:"enabled_sources"`
    LastCapture    map[string]string `yaml:"last_capture"` // source → ISO timestamp
}
```

### Trailers Config Section

```go
type TrailersConfig struct {
    Enabled bool   `yaml:"enabled"` // default: true
    Prefix  string `yaml:"prefix"`  // default: "Opax-"
}
```

### Config Hierarchy

Load order (later overrides earlier):

1. **SDK defaults** — hardcoded in Go, always present
2. **Team config** — `.opax/config.yaml` (committed to repo, shared)
3. **Personal config** — `~/.config/opax/config.yaml` (never committed)

Merge strategy: deep merge at the section level. Arrays replace, not append (e.g., `builtin_detectors` in personal config replaces team config's list entirely). Scalar values override.

### SDK Defaults

```yaml
hygiene:
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
    source_files:
      - .env
      - .env.local
    entropy:
      enabled: true
      threshold: 4.5
      min_length: 20
    allowlist: []

storage:
  retention:
    hot: 30d
    warm: 90d

capture:
  enabled_sources: []

trailers:
  enabled: true
  prefix: "Opax-"
```

### Strict Validation

The config system must reject invalid configuration rather than silently accepting it:

- **Unknown keys** — any YAML key not in the struct definition is an error (use `yaml.v3` strict mode or equivalent)
- **Enum values** — `scrubbing.mode` must be one of `redact`, `reject`, `warn`
- **Pattern compilation** — every entry in `custom_patterns` must have a valid Go regex in its `pattern` field. Compile at load time, reject if invalid
- **Duration parsing** — retention values like `30d`, `90d`, `3y` must parse to valid durations
- **Non-empty required fields** — `hygiene.version` must be > 0; `scrubbing.mode` validated when present

### Public API

```go
// Load reads config from the hierarchy and returns merged, validated config.
func Load(repoRoot string) (*OpaxConfig, error)

// Default returns the SDK default configuration.
func Default() *OpaxConfig

// Validate checks an OpaxConfig for invalid values.
func Validate(cfg *OpaxConfig) error
```

### Acceptance Criteria

- [x] `Default()` returns a fully populated config matching the SDK defaults above
- [x] `Load()` merges team + personal configs over defaults
- [x] Personal config overrides team config; team config overrides defaults
- [x] Unknown YAML keys cause `Load()` to return an error
- [x] Invalid enum values cause `Validate()` to return an error
- [x] Invalid regex in `custom_patterns` causes `Validate()` to return an error
- [x] Missing config files are silently skipped (not an error)
- [x] Empty config file is valid (all defaults apply)
- [x] Error messages include the config file path and the problematic key
- [x] Table-driven tests, stdlib `testing` only

---

## FEAT-0004 — File Lock Utility (`internal/lock/`)

### Description

Implement advisory file locking at `.git/opax.lock` for repository-wide administrative coordination in Phase 0 (for example branch bootstrap and future compaction/archive mutations that span multiple artifacts).

Steady-state record and notes writes do not take this lock. They rely on per-ref compare-and-swap publication with bounded retry.

### New File

`internal/lock/lock.go`

### Design

**Advisory file lock** using `os.OpenFile` with `O_CREATE|O_EXCL` for atomic creation. The lock file contains the PID of the holder for diagnostics and conservative stale lock detection.

### Public API

```go
// Lock represents an acquired file lock.
type Lock struct {
    path string
    file *os.File
}

// Acquire attempts to obtain the lock at the given path.
// Blocks up to timeout, polling at short intervals.
// Returns ErrLockTimeout if the lock cannot be acquired.
// Returns ErrStaleLock if a stale or corrupt lock was detected.
// The lock package does not remove the file automatically.
func Acquire(path string, timeout time.Duration) (*Lock, error)

// Release releases the lock and removes the lock file.
// Safe to call multiple times.
func (l *Lock) Release() error
```

### Lock File Content

The lock file contains a JSON object with the holder's PID and acquisition timestamp:

```json
{"pid": 12345, "acquired_at": "2026-03-17T10:30:00Z"}
```

### Timeout Behavior

- Default timeout: 5 seconds
- Poll interval: 50ms
- On timeout: return `ErrLockTimeout` with details about the current holder (PID from lock file)

### Stale Lock Detection

A lock is considered stale if the PID in the lock file does not correspond to a running process, or if the lock file remains corrupt beyond a short initialization grace window. On detecting a stale lock:

1. Return `ErrStaleLock`
2. Leave the lock file in place
3. Require manual cleanup after verifying no Opax write is active

This handles the case where a previous `opax` process crashed without cleanup.

### Deferred Cleanup Pattern

The intended usage pattern for administrative flows:

```go
lock, err := lock.Acquire(".git/opax.lock", 5*time.Second)
if err != nil {
    return fmt.Errorf("lock: acquire failed: %w", err)
}
defer lock.Release()
```

### Acceptance Criteria

- [x] `Acquire` creates lock file atomically (no race between check and create)
- [x] `Acquire` blocks and retries up to timeout
- [x] `Acquire` returns `ErrLockTimeout` after timeout expires
- [x] `Release` removes the lock file
- [x] `Release` is idempotent — calling twice does not error
- [x] Lock file contains valid JSON with PID and timestamp
- [x] Stale lock detection: if PID in lock file is not running, `ErrStaleLock` is returned and the file remains in place for manual cleanup
- [x] Concurrent acquisition test: two processes competing for the same lock, only one succeeds immediately and the other acquires after release
- [x] Deferred cleanup works correctly (lock released even if function returns early — `defer` semantics)
- [x] Error messages include the lock file path: `fmt.Errorf("lock: ...")`
- [x] Table-driven tests, stdlib `testing` only

---

## Non-Goals (Scope Boundaries)

These are explicitly out of scope for EPIC-0000. Violating these boundaries is scope creep:

- **No git operations** — E1 owns all git plumbing
- **No SQLite operations** — E5 owns the materialized view
- **No hygiene pipeline logic** — E3 owns detection and scrubbing; E0 only defines the `Hygiene` / `HygieneMetadata` struct and config types
- **No CLI commands** — E9 owns command wiring
- **No capture logic** — E7 owns session reading
- **No file I/O beyond config loading and lock files** — CAS is E2, branch writes are E1
- **No MCP server** — E10 owns the server

---

## Files Created

| File | Package | Description |
|---|---|---|
| `internal/types/types.go` | `types` | Record IDs, metadata structs, enums, ULID helper, prefix registry |
| `internal/types/types_test.go` | `types` | Table-driven tests for ID generation, validation, JSON round-trip |
| `internal/config/config.go` | `config` | OpaxConfig struct, Load/Default/Validate, hierarchy merge |
| `internal/config/config_test.go` | `config` | Table-driven tests for defaults, merge, validation, unknown keys |
| `internal/lock/lock.go` | `lock` | Advisory file lock with timeout and stale detection |
| `internal/lock/lock_test.go` | `lock` | Table-driven tests for acquisition, timeout, staleness, concurrency |

---

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| `modernc.org/sqlite` doesn't support FTS5 | Medium | Smoke test FTS5 in FEAT-0001 dependency verification. Fallback: `crawshaw.io/sqlite` with CGo (violates pure-Go constraint, last resort) |
| MCP Go SDK (`mcp-go`) is immature or API-unstable | Low | Protocol is simple JSON-RPC over stdio. Thin custom implementation is feasible. Only needed in E10 |
| `go-git` tree manipulation is awkward for branch writes | High | This is an E1 risk but EPIC-0000 adds the dependency. FEAT-0001 smoke test should verify basic plumbing operations. Fallback: shell out to `git` |
| Config merge semantics cause surprises | Low | Document merge strategy clearly. Arrays replace, scalars override. Test edge cases |

---

## Verification Checklist

- [x] All four features (FEAT-0001–FEAT-0004) have acceptance criteria met
- [x] Types align with data-spec.md JSON schemas — field names match exactly
- [x] Config structure aligns with hygiene.md YAML examples
- [x] Lock semantics match the narrowed Phase 0 contract (`.git/opax.lock` for admin/bootstrap coordination, not steady-state record writes)
- [x] No scope creep into E1+ territory (no git ops, no SQLite, no CLI commands)
- [x] `CGO_ENABLED=0 make build` succeeds
- [x] `make test` passes
- [x] `make lint` clean
