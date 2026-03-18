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
