// Package types defines the canonical Go types for Opax records.
// Pure data: no I/O, no side effects, no dependencies on other internal packages.
// JSON serialization matches data-spec.md field-for-field.
package types

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
// Returns the zero time.Time if the ID is too short or the suffix is not a valid ULID.
// Callers should invoke Validate() before calling Timestamp() if they need to distinguish
// an invalid ID from a valid ID with a zero timestamp.
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
		return fmt.Errorf("types: prefix %q already registered by %q, cannot register for %q", prefix, existing, owner)
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
