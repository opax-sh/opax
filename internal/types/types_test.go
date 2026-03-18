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
