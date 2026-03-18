// Package types defines the canonical Go types for Opax records.
// Pure data: no I/O, no side effects, no dependencies on other internal packages.
// JSON serialization matches data-spec.md field-for-field.
package types

import (
	"crypto/rand"
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
