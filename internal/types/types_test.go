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

func TestSaveIDValidate(t *testing.T) {
	valid := types.NewSaveID()
	tests := []struct {
		name    string
		id      types.SaveID
		wantErr bool
	}{
		{"valid", valid, false},
		{"empty", "", true},
		{"wrong prefix ses", types.SaveID("ses_" + types.NewULID().String()), true},
		{"no prefix", types.SaveID(types.NewULID().String()), true},
		{"invalid ulid chars", types.SaveID("sav_IIIIIIIIIIIIIIIIIIIIIIIIII"), true},
		{"too short suffix", types.SaveID("sav_SHORT"), true},
		{"just prefix", types.SaveID("sav_"), true},
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
