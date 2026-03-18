# FEAT-0002 — Core Domain Types

**Epic:** [EPIC-0000 — Project Foundation](../epics/EPIC-0000-foundation.md)
**Status:** Not started
**Dependencies:** FEAT-0001 (needs `oklog/ulid`)
**Dependents:** E1 (Git Plumbing), E2 (CAS), E3 (Privacy Pipeline), E4 (Write Path), E5 (SQLite), E7 (Capture), E8 (Memory Plugin) — virtually everything imports `internal/types`

---

## Problem

Every downstream package needs shared type definitions for record IDs, metadata structs, privacy classification, and enums. Without a single canonical source, packages would define their own incompatible representations, leading to conversion boilerplate, JSON serialization mismatches, and subtle bugs.

These types are the contract between the git data layer and the rest of the system. Their JSON serialization must match the schemas defined in data-spec.md exactly: field names, nesting, and omitempty behavior.

---

## Design

### Package

`internal/types/` — pure data types with no side effects, no I/O, no dependencies on other `internal/` packages. Only depends on `oklog/ulid` and stdlib.

### Files


| File                           | Contents                                               |
| ------------------------------ | ------------------------------------------------------ |
| `internal/types/types.go`      | All type definitions, constructors, validation methods |
| `internal/types/types_test.go` | Table-driven tests                                     |


---

## Specification

### 1. ULID Generation Helper

Wraps `oklog/ulid` with a cryptographically secure entropy source.

```go
// NewULID generates a new ULID using crypto/rand entropy.
// Uses a monotonic reader to ensure ordering within the same millisecond.
func NewULID() ulid.ULID
```

**Requirements:**

- Entropy source: `crypto/rand` (not `math/rand` — IDs must be unpredictable)
- Monotonic: two calls in the same millisecond produce lexicographically ordered results
- Thread-safe: safe for concurrent use from multiple goroutines

### 2. Record ID Types

Named `string` types with prefix enforcement. IDs are the primary key for all Opax records.

**Format:** `{prefix}_{ULID}` (e.g., `ses_01JQXYZ1234567890ABCDEF`)

#### SessionID

```go
type SessionID string

// NewSessionID generates a new session ID with the "ses_" prefix.
func NewSessionID() SessionID

// Validate checks that the ID has the correct prefix and a valid ULID suffix.
func (id SessionID) Validate() error

// String returns the string representation.
func (id SessionID) String() string

// Timestamp extracts the embedded ULID timestamp.
func (id SessionID) Timestamp() time.Time
```

#### SaveID

```go
type SaveID string

// NewSaveID generates a new save ID with the "sav_" prefix.
func NewSaveID() SaveID

// Validate, String, Timestamp — same contract as SessionID.
```

**Validation rules:**

- Must start with the correct prefix (`ses_` or `sav_`)
- Suffix after prefix must be a valid 26-character Crockford Base32 ULID
- Empty string is invalid

### 3. Plugin Prefix Registry

Plugins define their own ID prefixes (e.g., `wrk_` for workflows, `act_` for actions). The registry prevents collisions.

```go
// PrefixRegistry tracks registered ID prefixes.
type PrefixRegistry struct { /* unexported fields */ }

// NewPrefixRegistry creates a registry with first-party prefixes pre-registered.
// Pre-registered: "ses_", "sav_"
func NewPrefixRegistry() *PrefixRegistry

// Register claims a prefix for a plugin. Returns an error if the prefix
// is already registered (collision detection). The error message names
// both the existing owner and the new registrant for easy debugging.
// Prefix must end with "_" and be 3-5 characters (e.g., "wrk_", "act_").
func (r *PrefixRegistry) Register(prefix, owner string) error

// IsRegistered checks if a prefix is already claimed.
func (r *PrefixRegistry) IsRegistered(prefix string) bool
```

**Validation rules for prefixes:**

- Must end with `_`
- Length 3–5 characters (including the trailing `_`)
- Lowercase alphanumeric only before the `_`
- First-party prefixes (`ses_`, `sav_`) cannot be re-registered

### 4. Enums

Each enum is a named `string` type with defined constants and a `Valid() bool` method.

#### PrivacyTier

Controls access classification. Values from privacy.md:


| Constant      | Value       | Description                        |
| ------------- | ----------- | ---------------------------------- |
| `TierPublic`  | `"public"`  | Visible to anyone with repo access |
| `TierTeam`    | `"team"`    | Visible to team members (default)  |
| `TierPrivate` | `"private"` | Visible only to the session owner  |


#### ScrubMode

Determines how detected secrets are handled. Values from privacy.md:


| Constant      | Value      | Description                                |
| ------------- | ---------- | ------------------------------------------ |
| `ScrubRedact` | `"redact"` | Replace with `[REDACTED:{type}]` (default) |
| `ScrubReject` | `"reject"` | Refuse to store content                    |
| `ScrubWarn`   | `"warn"`   | Store but log a warning                    |


#### AttrReason

Describes how a session was linked to a save. Values from data-spec.md:


| Constant          | Value            | Description                                             |
| ----------------- | ---------------- | ------------------------------------------------------- |
| `AttrFileOverlap` | `"file_overlap"` | Session's files_touched overlaps save's files_in_commit |
| `AttrTemporal`    | `"temporal"`     | Session active on same branch near commit time          |


### 5. Record Structs

These structs define the canonical Go representation of Opax records. Their JSON serialization must match data-spec.md schemas field-for-field. Struct names are domain nouns — the package qualifier provides context (e.g., `types.Session`, `types.Save`).

#### Privacy

Present on every artifact. Source: privacy.md `PrivacyMetadata` interface.

```go
type Privacy struct {
    Tier           PrivacyTier `json:"tier"`
    Scrubbed       bool        `json:"scrubbed"`
    ScrubVersion   string      `json:"scrub_version,omitempty"`
    ScrubDetectors []string    `json:"scrub_detectors,omitempty"`
}
```

**Default values (for new records in Phase 0):**

- `Tier`: `"team"`
- `Scrubbed`: `false`

#### Session

Mirrors `sessions/{shard}/{id}/metadata.json`. Source: data-spec.md section 2.2.

```go
type Session struct {
    ID              SessionID `json:"id"`
    Version         int       `json:"version"`
    Provider        string    `json:"provider"`
    Model           string    `json:"model,omitempty"`
    Branch          string    `json:"branch,omitempty"`
    StartedAt       time.Time `json:"started_at"`
    EndedAt         time.Time `json:"ended_at,omitempty"`
    ExitCode        *int      `json:"exit_code,omitempty"`
    FilesChanged    int       `json:"files_changed,omitempty"`
    LinesAdded      int       `json:"lines_added,omitempty"`
    LinesRemoved    int       `json:"lines_removed,omitempty"`
    FilesTouched    []string  `json:"files_touched,omitempty"`
    ContentHash     string    `json:"content_hash,omitempty"`
    Privacy         Privacy   `json:"privacy"`
    Tags            []string  `json:"tags,omitempty"`
}
```

**Design notes:**

- `Provider` and `Model` follow the Vercel AI SDK convention: provider is the company (`"anthropic"`, `"openai"`, `"google"`), model is the specific model ID (`"claude-opus-4-6"`, `"o3-pro"`). Both are free-form strings — no enum. The combined identifier (`anthropic/claude-opus-4-6`) is derived, not stored
- `ExitCode` is `*int` (pointer) so that `0` is distinguishable from "not set" in JSON (`omitempty` on `int` would drop `0`)
- `Version` starts at `1` for all new records
- `FilesTouched` is extracted from agent tool calls — the field name matches data-spec.md's `files_touched`
- Time fields serialize as RFC 3339 (`time.Time` default JSON format)

#### Attribution

Links a session to a save with an attribution reason.

```go
type Attribution struct {
    SessionID SessionID  `json:"session_id"`
    Reason    AttrReason `json:"reason"`
}
```

#### Save

Mirrors `saves/{shard}/{id}/metadata.json`. Source: data-spec.md section 2.4.

```go
type Save struct {
    ID            SaveID        `json:"id"`
    Version       int           `json:"version"`
    CommitHash    string        `json:"commit_hash"`
    Sessions      []Attribution `json:"sessions,omitempty"`
    Branch        string        `json:"branch,omitempty"`
    CreatedAt     time.Time     `json:"created_at"`
    FilesInCommit []string      `json:"files_in_commit,omitempty"`
    ContentHash   string        `json:"content_hash,omitempty"`
    Privacy       Privacy       `json:"privacy"`
}
```

#### Note

Generic note content for any namespace. Source: data-spec.md section 3.2.

```go
type Note struct {
    CommitHash string          `json:"commit_hash"`
    Namespace  string          `json:"namespace"`
    Content    json.RawMessage `json:"content"`
    Version    int             `json:"version"`
}
```

**Design note:** `Content` is `json.RawMessage` because note content varies by namespace. The types package doesn't interpret note content — that's the responsibility of the plugin that owns the namespace.

---

## Edge Cases

- **Zero-value Privacy** — Go zero values produce `{"tier":"","scrubbed":false}`. Callers constructing new records should use a constructor or explicitly set `Tier` to `TierTeam`. The `Valid()` method on `PrivacyTier` will catch empty strings.
- **ULID timestamp precision** — ULIDs have millisecond precision. `Timestamp()` returns a `time.Time` with millisecond precision, not nanosecond.
- **JSON field name casing** — data-spec.md uses `snake_case` for all JSON fields. Go struct tags must match exactly: `started_at`, not `startedAt`.
- **Empty slices vs null** — Go marshals `nil` slices as `null` and empty slices as `[]`. With `omitempty`, both are omitted. This is correct for our schema (all list fields are optional).
- **ExitCode zero** — `exit_code: 0` is meaningful (successful exit). Using `*int` with `omitempty` means `nil` is omitted but `0` is serialized. This matches the data-spec.md intent.
- **Concurrent ULID generation** — The monotonic reader must be safe for concurrent use. Wrap with a mutex or use `sync.Pool`.

---

## Acceptance Criteria

- All types compile and are importable from `internal/types`
- `NewSessionID()` returns an ID starting with `ses_` followed by a valid 26-char ULID
- `NewSaveID()` returns an ID starting with `sav_` followed by a valid 26-char ULID
- `Validate()` accepts valid IDs and rejects: empty string, wrong prefix, invalid ULID chars, wrong ULID length
- `Timestamp()` returns a time within 1 second of generation time
- Two ULIDs generated in the same millisecond are lexicographically ordered (monotonic)
- `PrefixRegistry` pre-registers `ses_` and `sav_`
- `PrefixRegistry.Register("ses_", "plugin")` returns an error (collision)
- `PrefixRegistry.Register("wrk_", "workflows")` succeeds
- Prefix validation: rejects `"no_underscore"`, `"AB_"` (uppercase), `"toolong__"` (>5 chars), `"x"` (<3 chars)
- All enum `Valid()` methods return `true` for defined constants and `false` for empty string or unknown values
- `Session` JSON round-trip: marshal → unmarshal → deep equal
- `Save` JSON round-trip: marshal → unmarshal → deep equal
- JSON field names match data-spec.md exactly (spot-check: `started_at`, `files_touched`, `commit_hash`, `scrub_version`)
- `Session` with `ExitCode` set to `0` serializes `"exit_code": 0` (not omitted)
- `Session` with `ExitCode` nil omits `exit_code` from JSON
- `Privacy` zero-value has `Tier` as empty string — `Valid()` returns false
- Table-driven tests, stdlib `testing` only, no testify

---

## Test Plan


| Test                              | What it verifies                | Pass condition                                     |
| --------------------------------- | ------------------------------- | -------------------------------------------------- |
| `TestNewSessionID`                | ID generation                   | Starts with `ses_`, suffix is valid ULID           |
| `TestNewSaveID`                   | ID generation                   | Starts with `sav_`, suffix is valid ULID           |
| `TestSessionIDValidate`           | Validation (table-driven)       | Accepts valid IDs, rejects malformed ones          |
| `TestSessionIDTimestamp`          | Timestamp extraction            | Within 1s of `time.Now()` at generation            |
| `TestULIDMonotonic`               | Monotonic ordering              | 100 sequential ULIDs are lexicographically sorted  |
| `TestPrefixRegistryPreregistered` | First-party prefixes            | `ses_` and `sav_` are registered at construction   |
| `TestPrefixRegistryCollision`     | Collision detection             | Re-registering `ses_` returns error                |
| `TestPrefixRegistryValidation`    | Prefix format rules             | Rejects invalid prefix formats                     |
| `TestPrivacyTierValid`            | Enum validation                 | All constants valid, empty and unknown invalid     |
| `TestScrubModeValid`              | Enum validation                 | All constants valid, empty and unknown invalid     |
| `TestAttrReasonValid`             | Enum validation                 | All constants valid, empty and unknown invalid     |
| `TestSessionJSON`                 | JSON round-trip                 | Marshal → unmarshal equals original                |
| `TestSessionFieldNames`           | JSON field naming               | Serialized JSON uses snake_case matching data-spec |
| `TestSessionExitCode`             | Pointer omitempty behavior      | `0` serialized, `nil` omitted                      |
| `TestSaveJSON`                    | JSON round-trip                 | Marshal → unmarshal equals original                |
| `TestNoteJSON`                    | JSON round-trip with RawMessage | Content preserved as raw JSON                      |
| `TestPrivacyDefaults`             | Zero-value behavior             | Empty tier, scrubbed false                         |


