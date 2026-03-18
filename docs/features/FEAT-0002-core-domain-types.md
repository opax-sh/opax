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


---

## Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create `internal/types` package with all shared domain types: ULID helper, ID types with prefix enforcement, prefix collision registry, privacy/scrub/attribution enums, and JSON-serializable record structs.

**Architecture:** Single package `internal/types` — pure data with no I/O, no side effects, no dependencies on other `internal/` packages. All JSON field names match `data-spec.md` exactly. Built incrementally with TDD: one component at a time, test first.

**Tech Stack:** Go stdlib (`encoding/json`, `crypto/rand`, `sync`, `strings`, `fmt`, `errors`, `time`), `github.com/oklog/ulid/v2`

---

### File Map

| File | Contents |
|------|----------|
| `internal/types/types.go` | All type definitions, constructors, validation methods |
| `internal/types/types_test.go` | Table-driven tests (stdlib `testing` only) |

---

### Task 1: Package scaffold and ULID helper

**Files:**
- Create: `internal/types/types.go`
- Create: `internal/types/types_test.go`

> **Note on global state:** `CLAUDE.md` prohibits package-level state, but `NewULID` requires a shared monotonic entropy source to satisfy two requirements simultaneously: (a) ordering within the same millisecond, (b) thread-safety. This is a documented, deliberate exception — a mutex-protected entropy reader, not a configurable singleton. No other global state exists in the package.

- [ ] **Step 1.1: Write failing ULID tests**

Create `internal/types/types_test.go`:

```go
package types_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/opax-sh/opax/internal/types"
)

func TestNewULIDFormat(t *testing.T) {
	id := types.NewULID()
	s := id.String()
	if len(s) != 26 {
		t.Errorf("NewULID() length = %d, want 26", len(s))
	}
	// Crockford Base32: no I, L, O, U
	for _, c := range s {
		if strings.ContainsRune("ILOU", c) {
			t.Errorf("NewULID() contains invalid Crockford char %q in %s", c, s)
		}
	}
}

func TestNewULIDTimestamp(t *testing.T) {
	before := time.Now()
	id := types.NewULID()
	after := time.Now()
	ts := ulid.Time(id.Time())
	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("NewULID() timestamp %v outside expected range [%v, %v]", ts, before, after)
	}
}

func TestULIDMonotonic(t *testing.T) {
	const n = 100
	ids := make([]string, n)
	for i := range ids {
		ids[i] = types.NewULID().String()
	}
	for i := 1; i < n; i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("ULIDs not ordered at index %d: %s <= %s", i, ids[i], ids[i-1])
		}
	}
}
```

- [ ] **Step 1.2: Run tests to confirm they fail**

```bash
go test ./internal/types/...
```

Expected: `cannot find package "github.com/opax-sh/opax/internal/types"`

- [ ] **Step 1.3: Create types.go with package declaration and ULID helper**

Create `internal/types/types.go`:

```go
// Package types defines the canonical Go types for Opax records.
// Pure data: no I/O, no side effects, no dependencies on other internal packages.
// JSON serialization matches data-spec.md field-for-field.
package types

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// entropyMu + entropy: deliberate package-level state.
// ulid.Monotonic requires a shared reader to guarantee lexicographic
// ordering across calls within the same millisecond. The mutex makes it
// safe for concurrent use. This is the only package-level state in types.
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewULID generates a new ULID using crypto/rand entropy.
// Uses a monotonic reader to ensure ordering within the same millisecond.
// Safe for concurrent use from multiple goroutines.
func NewULID() ulid.ULID {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
}
```

- [ ] **Step 1.4: Tidy modules**

```bash
go mod tidy
```

Expected: no errors (oklog/ulid/v2 is already in go.mod from FEAT-0001, but this ensures go.sum is up to date)

- [ ] **Step 1.5: Run tests to confirm they pass**

```bash
go test ./internal/types/... -run "TestNewULID|TestULIDMonotonic" -v
```

Expected: all three ULID tests PASS

- [ ] **Step 1.6: Commit**

```bash
git add internal/types/types.go internal/types/types_test.go
git commit -m "feat(types): add package scaffold and ULID helper"
```

---

### Task 2: SessionID and SaveID types

**Files:**
- Modify: `internal/types/types.go` (append ID types and helpers)
- Modify: `internal/types/types_test.go` (append ID tests)

- [ ] **Step 2.1: Write failing tests for ID types**

Append to `internal/types/types_test.go`:

```go
func TestNewSessionID(t *testing.T) {
	id := types.NewSessionID()
	s := id.String()
	if !strings.HasPrefix(s, "ses_") {
		t.Errorf("NewSessionID() = %q, want prefix \"ses_\"", s)
	}
	suffix := s[4:]
	if len(suffix) != 26 {
		t.Errorf("NewSessionID() suffix length = %d, want 26", len(suffix))
	}
	if _, err := ulid.ParseStrict(suffix); err != nil {
		t.Errorf("NewSessionID() suffix not a valid ULID: %v", err)
	}
}

func TestNewSaveID(t *testing.T) {
	id := types.NewSaveID()
	s := id.String()
	if !strings.HasPrefix(s, "sav_") {
		t.Errorf("NewSaveID() = %q, want prefix \"sav_\"", s)
	}
	suffix := s[4:]
	if len(suffix) != 26 {
		t.Errorf("NewSaveID() suffix length = %d, want 26", len(suffix))
	}
	if _, err := ulid.ParseStrict(suffix); err != nil {
		t.Errorf("NewSaveID() suffix not a valid ULID: %v", err)
	}
}

func TestSessionIDValidate(t *testing.T) {
	valid := types.NewSessionID()
	tests := []struct {
		name    string
		id      types.SessionID
		wantErr bool
	}{
		{"valid", valid, false},
		{"empty", "", true},
		{"wrong prefix sav", types.SessionID("sav_" + types.NewULID().String()), true},
		{"no prefix", types.SessionID(types.NewULID().String()), true},
		{"invalid ulid chars", types.SessionID("ses_IIIIIIIIIIIIIIIIIIIIIIIIII"), true},
		{"too short suffix", types.SessionID("ses_SHORT"), true},
		{"just prefix", types.SessionID("ses_"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.id.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSessionIDTimestamp(t *testing.T) {
	before := time.Now()
	id := types.NewSessionID()
	after := time.Now()
	ts := id.Timestamp()
	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("Timestamp() = %v, want within [%v, %v]", ts, before, after)
	}
}
```

- [ ] **Step 2.2: Run tests to confirm they fail**

```bash
go test ./internal/types/... -run "TestNewSessionID|TestNewSaveID|TestSessionID"
```

Expected: FAIL — `types.SessionID undefined` (or similar)

- [ ] **Step 2.3: Implement ID types**

Append to `internal/types/types.go`:

```go
import (
	"errors"
	"fmt"
	"strings"
	// existing imports above
)

// SessionID is the primary key for session records. Format: "ses_{ULID}".
type SessionID string

// SaveID is the primary key for save records. Format: "sav_{ULID}".
type SaveID string

const (
	sessionPrefix = "ses_"
	savePrefix    = "sav_"
)

// NewSessionID generates a new session ID with the "ses_" prefix.
func NewSessionID() SessionID {
	return SessionID(sessionPrefix + NewULID().String())
}

// NewSaveID generates a new save ID with the "sav_" prefix.
func NewSaveID() SaveID {
	return SaveID(savePrefix + NewULID().String())
}

func (id SessionID) Validate() error { return validateID(string(id), sessionPrefix) }
func (id SessionID) String() string  { return string(id) }
func (id SessionID) Timestamp() time.Time {
	return extractTimestamp(string(id), len(sessionPrefix))
}

func (id SaveID) Validate() error { return validateID(string(id), savePrefix) }
func (id SaveID) String() string  { return string(id) }
func (id SaveID) Timestamp() time.Time {
	return extractTimestamp(string(id), len(savePrefix))
}

// validateID checks that id starts with prefix and has a valid 26-char ULID suffix.
func validateID(id, prefix string) error {
	if id == "" {
		return errors.New("types: ID is empty")
	}
	if !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("types: ID %q has wrong prefix, want %q", id, prefix)
	}
	suffix := id[len(prefix):]
	if _, err := ulid.ParseStrict(suffix); err != nil {
		return fmt.Errorf("types: ID %q has invalid ULID suffix: %w", id, err)
	}
	return nil
}

// extractTimestamp extracts the millisecond-precision timestamp from an ID suffix.
func extractTimestamp(id string, prefixLen int) time.Time {
	if len(id) <= prefixLen {
		return time.Time{}
	}
	parsed, err := ulid.ParseStrict(id[prefixLen:])
	if err != nil {
		return time.Time{}
	}
	return ulid.Time(parsed.Time())
}
```

> **Compile note:** Move the additional imports (`errors`, `fmt`, `strings`) into the existing import block at the top of the file — do not add a second `import (...)` block. Go allows only one import block per file (or multiple single-line imports; group them in the existing block).

- [ ] **Step 2.4: Run tests to confirm they pass**

```bash
go test ./internal/types/... -run "TestNewSessionID|TestNewSaveID|TestSessionID" -v
```

Expected: all PASS

- [ ] **Step 2.5: Commit**

```bash
git add internal/types/types.go internal/types/types_test.go
git commit -m "feat(types): add SessionID and SaveID with prefix validation"
```

---

### Task 3: PrefixRegistry

**Files:**
- Modify: `internal/types/types.go` (append PrefixRegistry)
- Modify: `internal/types/types_test.go` (append registry tests)

- [ ] **Step 3.1: Write failing tests**

Append to `internal/types/types_test.go`:

```go
func TestPrefixRegistryPreregistered(t *testing.T) {
	r := types.NewPrefixRegistry()
	for _, prefix := range []string{"ses_", "sav_"} {
		if !r.IsRegistered(prefix) {
			t.Errorf("NewPrefixRegistry() did not pre-register %q", prefix)
		}
	}
}

func TestPrefixRegistryCollision(t *testing.T) {
	r := types.NewPrefixRegistry()
	err := r.Register("ses_", "some-plugin")
	if err == nil {
		t.Error("Register(\"ses_\", ...) should return error on collision, got nil")
	}
}

func TestPrefixRegistrySuccess(t *testing.T) {
	r := types.NewPrefixRegistry()
	if err := r.Register("wrk_", "workflows"); err != nil {
		t.Errorf("Register(\"wrk_\", \"workflows\") unexpected error: %v", err)
	}
	if !r.IsRegistered("wrk_") {
		t.Error("IsRegistered(\"wrk_\") should be true after Register")
	}
}

func TestPrefixRegistryValidation(t *testing.T) {
	r := types.NewPrefixRegistry()
	tests := []struct {
		prefix  string
		wantErr bool
	}{
		{"wrk_", false},    // valid 4-char
		{"ab_", false},     // valid 3-char
		{"abcd_", false},   // valid 5-char
		{"no_underscore", true},  // no trailing _
		{"AB_", true},      // uppercase
		{"toolong_", true}, // >5 chars
		{"x", true},        // too short (<3)
		{"x_", true},       // too short (2 chars)
		{"", true},         // empty
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			err := r.Register(tt.prefix, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("Register(%q) error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 3.2: Run tests to confirm they fail**

```bash
go test ./internal/types/... -run "TestPrefixRegistry"
```

Expected: FAIL — `types.PrefixRegistry undefined`

- [ ] **Step 3.3: Implement PrefixRegistry**

Append to `internal/types/types.go`:

```go
// PrefixRegistry tracks registered ID prefixes to prevent collisions between
// first-party types and plugin-defined types.
type PrefixRegistry struct {
	mu     sync.RWMutex
	owners map[string]string
}

// NewPrefixRegistry creates a registry with first-party prefixes pre-registered.
// Pre-registered: "ses_" (sessions), "sav_" (saves) — both owned by "opax".
func NewPrefixRegistry() *PrefixRegistry {
	return &PrefixRegistry{
		owners: map[string]string{
			sessionPrefix: "opax",
			savePrefix:    "opax",
		},
	}
}

// Register claims a prefix for the given owner. Returns an error if the prefix
// is already registered or fails format validation.
// Format rules: 3–5 chars total, lowercase alphanumeric before trailing "_".
func (r *PrefixRegistry) Register(prefix, owner string) error {
	if err := validatePrefixFormat(prefix); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.owners[prefix]; ok {
		return fmt.Errorf("types: prefix %q already registered by %q", prefix, existing)
	}
	r.owners[prefix] = owner
	return nil
}

// IsRegistered reports whether prefix has been claimed.
func (r *PrefixRegistry) IsRegistered(prefix string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.owners[prefix]
	return ok
}

// validatePrefixFormat enforces: 3–5 chars, trailing "_", lowercase alphanumeric body.
func validatePrefixFormat(prefix string) error {
	if len(prefix) < 3 || len(prefix) > 5 {
		return fmt.Errorf("types: prefix %q must be 3–5 characters (got %d)", prefix, len(prefix))
	}
	if prefix[len(prefix)-1] != '_' {
		return fmt.Errorf("types: prefix %q must end with underscore", prefix)
	}
	for _, c := range prefix[:len(prefix)-1] {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return fmt.Errorf("types: prefix %q contains invalid character %q (only lowercase alphanumeric)", prefix, c)
		}
	}
	return nil
}
```

- [ ] **Step 3.4: Run tests to confirm they pass**

```bash
go test ./internal/types/... -run "TestPrefixRegistry" -v
```

Expected: all PASS

- [ ] **Step 3.5: Commit**

```bash
git add internal/types/types.go internal/types/types_test.go
git commit -m "feat(types): add PrefixRegistry with collision detection"
```

---

### Task 4: Enums

**Files:**
- Modify: `internal/types/types.go` (append three enum types)
- Modify: `internal/types/types_test.go` (append enum tests)

- [ ] **Step 4.1: Write failing tests for all three enums**

Append to `internal/types/types_test.go`:

```go
func TestPrivacyTierValid(t *testing.T) {
	tests := []struct {
		tier  types.PrivacyTier
		valid bool
	}{
		{types.TierPublic, true},
		{types.TierTeam, true},
		{types.TierPrivate, true},
		{"", false},
		{"unknown", false},
		{"Public", false}, // case-sensitive
	}
	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			if got := tt.tier.Valid(); got != tt.valid {
				t.Errorf("PrivacyTier(%q).Valid() = %v, want %v", tt.tier, got, tt.valid)
			}
		})
	}
}

func TestScrubModeValid(t *testing.T) {
	tests := []struct {
		mode  types.ScrubMode
		valid bool
	}{
		{types.ScrubRedact, true},
		{types.ScrubReject, true},
		{types.ScrubWarn, true},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := tt.mode.Valid(); got != tt.valid {
				t.Errorf("ScrubMode(%q).Valid() = %v, want %v", tt.mode, got, tt.valid)
			}
		})
	}
}

func TestAttrReasonValid(t *testing.T) {
	tests := []struct {
		reason types.AttrReason
		valid  bool
	}{
		{types.AttrFileOverlap, true},
		{types.AttrTemporal, true},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			if got := tt.reason.Valid(); got != tt.valid {
				t.Errorf("AttrReason(%q).Valid() = %v, want %v", tt.reason, got, tt.valid)
			}
		})
	}
}
```

- [ ] **Step 4.2: Run tests to confirm they fail**

```bash
go test ./internal/types/... -run "TestPrivacyTierValid|TestScrubModeValid|TestAttrReasonValid"
```

Expected: FAIL — undefined constants

- [ ] **Step 4.3: Implement enums**

Append to `internal/types/types.go`:

```go
// PrivacyTier controls access classification for a record.
type PrivacyTier string

const (
	TierPublic  PrivacyTier = "public"  // Visible to anyone with repo access.
	TierTeam    PrivacyTier = "team"    // Visible to team members (default).
	TierPrivate PrivacyTier = "private" // Visible only to the session owner.
)

// Valid reports whether t is a defined PrivacyTier constant.
func (t PrivacyTier) Valid() bool {
	switch t {
	case TierPublic, TierTeam, TierPrivate:
		return true
	}
	return false
}

// ScrubMode determines how detected secrets are handled.
type ScrubMode string

const (
	ScrubRedact ScrubMode = "redact" // Replace with [REDACTED:{type}] (default).
	ScrubReject ScrubMode = "reject" // Refuse to store content.
	ScrubWarn   ScrubMode = "warn"   // Store but log a warning.
)

// Valid reports whether m is a defined ScrubMode constant.
func (m ScrubMode) Valid() bool {
	switch m {
	case ScrubRedact, ScrubReject, ScrubWarn:
		return true
	}
	return false
}

// AttrReason describes how a session was linked to a save.
type AttrReason string

const (
	AttrFileOverlap AttrReason = "file_overlap" // Session files_touched overlaps save files_in_commit.
	AttrTemporal    AttrReason = "temporal"      // Session active on same branch near commit time.
)

// Valid reports whether r is a defined AttrReason constant.
func (r AttrReason) Valid() bool {
	switch r {
	case AttrFileOverlap, AttrTemporal:
		return true
	}
	return false
}
```

- [ ] **Step 4.4: Run tests to confirm they pass**

```bash
go test ./internal/types/... -run "TestPrivacyTierValid|TestScrubModeValid|TestAttrReasonValid" -v
```

Expected: all PASS

- [ ] **Step 4.5: Commit**

```bash
git add internal/types/types.go internal/types/types_test.go
git commit -m "feat(types): add PrivacyTier, ScrubMode, AttrReason enums"
```

---

### Task 5: Record structs and JSON tests

**Files:**
- Modify: `internal/types/types.go` (append Privacy, Session, Attribution, Save, Note)
- Modify: `internal/types/types_test.go` (append JSON tests)

> **`omitempty` on `time.Time` note:** Go's `encoding/json` does NOT omit zero-value structs for `omitempty` — only primitives, pointers, slices, maps, and strings. `EndedAt time.Time` with `omitempty` will serialize as `"0001-01-01T00:00:00Z"` when unset, not be omitted. The spec includes this tag; implement exactly as written. This is a known limitation and does not affect any acceptance criteria.

- [ ] **Step 5.1: Write failing tests for structs and JSON**

Append to `internal/types/types_test.go`:

```go
import (
	"encoding/json"
	// existing imports
)

func TestSessionJSON(t *testing.T) {
	exitCode := 0
	original := types.Session{
		ID:           types.NewSessionID(),
		Version:      1,
		Provider:     "anthropic",
		Model:        "claude-opus-4-6",
		Branch:       "main",
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		ExitCode:     &exitCode,
		FilesChanged: 3,
		LinesAdded:   10,
		LinesRemoved: 5,
		FilesTouched: []string{"main.go", "types.go"},
		ContentHash:  "deadbeef",
		Privacy: types.Privacy{
			Tier:     types.TierTeam,
			Scrubbed: false,
		},
		Tags: []string{"feature"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.Session
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if original.ID != decoded.ID {
		t.Errorf("ID: got %v, want %v", decoded.ID, original.ID)
	}
	if original.Version != decoded.Version {
		t.Errorf("Version: got %v, want %v", decoded.Version, original.Version)
	}
	if original.Provider != decoded.Provider {
		t.Errorf("Provider: got %v, want %v", decoded.Provider, original.Provider)
	}
	if original.Privacy.Tier != decoded.Privacy.Tier {
		t.Errorf("Privacy.Tier: got %v, want %v", decoded.Privacy.Tier, original.Privacy.Tier)
	}
}

func TestSessionFieldNames(t *testing.T) {
	exitCode := 1
	s := types.Session{
		ID:           types.NewSessionID(),
		Version:      1,
		Provider:     "openai",
		StartedAt:    time.Now().UTC(),
		ExitCode:     &exitCode,
		FilesTouched: []string{"foo.go"},
		Privacy: types.Privacy{
			Tier:           types.TierPrivate,
			Scrubbed:       true,
			ScrubVersion:   "v1",
			ScrubDetectors: []string{"regex"},
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := string(data)
	for _, field := range []string{"started_at", "files_touched", "scrub_version", "scrub_detectors"} {
		if !strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("JSON missing field %q in: %s", field, raw)
		}
	}
}

func TestSessionExitCode(t *testing.T) {
	zero := 0
	// nil exit_code should be omitted
	s1 := types.Session{ID: types.NewSessionID(), Version: 1, Provider: "x", StartedAt: time.Now(), Privacy: types.Privacy{Tier: types.TierTeam}}
	data1, _ := json.Marshal(s1)
	if strings.Contains(string(data1), "exit_code") {
		t.Errorf("nil ExitCode should be omitted, got: %s", data1)
	}
	// exit_code: 0 should be present
	s2 := s1
	s2.ExitCode = &zero
	data2, _ := json.Marshal(s2)
	if !strings.Contains(string(data2), `"exit_code":0`) {
		t.Errorf("ExitCode=0 should serialize as \"exit_code\":0, got: %s", data2)
	}
}

func TestSaveJSON(t *testing.T) {
	original := types.Save{
		ID:         types.NewSaveID(),
		Version:    1,
		CommitHash: "abc123def456",
		Sessions: []types.Attribution{
			{SessionID: types.NewSessionID(), Reason: types.AttrFileOverlap},
		},
		Branch:        "main",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		FilesInCommit: []string{"go.mod", "main.go"},
		Privacy:       types.Privacy{Tier: types.TierTeam},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.Save
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if original.ID != decoded.ID {
		t.Errorf("ID: got %v, want %v", decoded.ID, original.ID)
	}
	if original.CommitHash != decoded.CommitHash {
		t.Errorf("CommitHash: got %v, want %v", decoded.CommitHash, original.CommitHash)
	}
	if len(decoded.Sessions) != 1 || decoded.Sessions[0].Reason != types.AttrFileOverlap {
		t.Errorf("Sessions: got %+v, want one AttrFileOverlap entry", decoded.Sessions)
	}
	// spot-check JSON field name
	if !strings.Contains(string(data), `"commit_hash"`) {
		t.Errorf("JSON missing field \"commit_hash\" in: %s", data)
	}
}

func TestNoteJSON(t *testing.T) {
	raw := json.RawMessage(`{"key":"value","nested":{"n":42}}`)
	original := types.Note{
		CommitHash: "deadbeef",
		Namespace:  "workflows",
		Content:    raw,
		Version:    1,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.Note
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(decoded.Content) != string(raw) {
		t.Errorf("Content: got %s, want %s", decoded.Content, raw)
	}
}

func TestPrivacyDefaults(t *testing.T) {
	var p types.Privacy
	if p.Tier != "" {
		t.Errorf("zero Privacy.Tier = %q, want empty string", p.Tier)
	}
	if p.Scrubbed {
		t.Error("zero Privacy.Scrubbed = true, want false")
	}
	if p.Tier.Valid() {
		t.Error("empty PrivacyTier.Valid() = true, want false")
	}
}
```

- [ ] **Step 5.2: Run tests to confirm they fail**

```bash
go test ./internal/types/... -run "TestSession|TestSave|TestNote|TestPrivacyDefaults"
```

Expected: FAIL — `types.Session undefined`, `types.Privacy undefined`, etc.

- [ ] **Step 5.3: Implement record structs**

Append to `internal/types/types.go`. Add `"encoding/json"` to the **existing** import block at the top — do not add a second `import (...)` block:

```go
import "encoding/json"

// Privacy metadata is present on every record artifact.
// Default for new records: Tier = TierTeam, Scrubbed = false.
type Privacy struct {
	Tier           PrivacyTier `json:"tier"`
	Scrubbed       bool        `json:"scrubbed"`
	ScrubVersion   string      `json:"scrub_version,omitempty"`
	ScrubDetectors []string    `json:"scrub_detectors,omitempty"`
}

// Session mirrors sessions/{shard}/{id}/metadata.json from data-spec.md §2.2.
type Session struct {
	ID           SessionID `json:"id"`
	Version      int       `json:"version"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	FilesChanged int       `json:"files_changed,omitempty"`
	LinesAdded   int       `json:"lines_added,omitempty"`
	LinesRemoved int       `json:"lines_removed,omitempty"`
	FilesTouched []string  `json:"files_touched,omitempty"`
	ContentHash  string    `json:"content_hash,omitempty"`
	Privacy      Privacy   `json:"privacy"`
	Tags         []string  `json:"tags,omitempty"`
}

// Attribution links a session to a save with a reason.
type Attribution struct {
	SessionID SessionID  `json:"session_id"`
	Reason    AttrReason `json:"reason"`
}

// Save mirrors saves/{shard}/{id}/metadata.json from data-spec.md §2.4.
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

// Note holds generic note content for any namespace. Content varies by
// namespace; this package does not interpret it — that is the plugin's job.
// Mirrors data-spec.md §3.2.
type Note struct {
	CommitHash string          `json:"commit_hash"`
	Namespace  string          `json:"namespace"`
	Content    json.RawMessage `json:"content"`
	Version    int             `json:"version"`
}
```

- [ ] **Step 5.4: Run full test suite**

```bash
go test ./internal/types/... -v
```

Expected: all tests PASS (20 test functions).

- [ ] **Step 5.5: Run go vet**

```bash
go vet ./internal/types/...
```

Expected: no output (no issues)

- [ ] **Step 5.6: Commit**

```bash
git add internal/types/types.go internal/types/types_test.go
git commit -m "feat(types): add Privacy, Session, Attribution, Save, Note structs"
```

---

### Task 6: Full-suite verification

- [ ] **Step 6.1: Run all tests**

```bash
make test
```

Expected: PASS — all packages (new types package + existing packages)

- [ ] **Step 6.2: Run lint**

```bash
make lint
```

Expected: no output

- [ ] **Step 6.3: Final commit if any cleanup needed, then push**

```bash
git push -u origin feat-0002-core-domain-types
```
