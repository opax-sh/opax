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
	tests := []struct {
		prefix  string
		wantErr bool
	}{
		{"wrk_", false},           // valid 4-char
		{"ab_", false},            // valid 3-char
		{"abcd_", false},          // valid 5-char
		{"no_underscore", true},   // no trailing _
		{"AB_", true},             // uppercase
		{"toolong_", true},        // >5 chars
		{"x", true},               // too short (<3)
		{"x_", true},              // too short (2 chars)
		{"", true},                // empty
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			r := types.NewPrefixRegistry()
			err := r.Register(tt.prefix, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("Register(%q) error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
			}
		})
	}
}

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
