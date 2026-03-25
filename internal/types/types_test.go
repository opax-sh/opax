package types_test

import (
	"encoding/json"
	"reflect"
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
		{"wrk_", false},         // valid 4-char
		{"ab_", false},          // valid 3-char
		{"abcd_", false},        // valid 5-char
		{"no_underscore", true}, // no trailing _
		{"AB_", true},           // uppercase
		{"toolong_", true},      // >5 chars
		{"x", true},             // too short (<3)
		{"x_", true},            // too short (2 chars)
		{"", true},              // empty
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
		Hygiene:      types.Hygiene{Scrubbed: false},
		Tags:         []string{"feature"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.Session
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("Session round-trip mismatch:\n  got  %+v\n  want %+v", decoded, original)
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
		Hygiene: types.Hygiene{
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
	s1 := types.Session{ID: types.NewSessionID(), Version: 1, Provider: "x", StartedAt: time.Now(), Hygiene: types.Hygiene{}}
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
		Hygiene:       types.Hygiene{},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.Save
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("Save round-trip mismatch:\n  got  %+v\n  want %+v", decoded, original)
	}
	// Keep the commit_hash spot-check (it's also in the plan's acceptance criteria):
	if !strings.Contains(string(data), `"commit_hash"`) {
		t.Errorf("JSON missing field \"commit_hash\" in: %s", data)
	}
}

func TestSessionEndedAtOmittedWhenUnset(t *testing.T) {
	s := types.Session{
		ID:        types.NewSessionID(),
		Version:   1,
		Provider:  "x",
		StartedAt: time.Now(),
		Hygiene:   types.Hygiene{},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), `"ended_at"`) {
		t.Errorf("expected ended_at to be omitted when unset, got: %s", data)
	}
}

func TestSessionEndedAtIncludedWhenSet(t *testing.T) {
	endedAt := time.Now().UTC().Truncate(time.Second)
	s := types.Session{
		ID:        types.NewSessionID(),
		Version:   1,
		Provider:  "x",
		StartedAt: endedAt.Add(-time.Minute),
		EndedAt:   &endedAt,
		Hygiene:   types.Hygiene{},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"ended_at":"`) {
		t.Errorf("expected ended_at in JSON when set, got: %s", data)
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

func TestHygieneDefaults(t *testing.T) {
	var h types.Hygiene
	if h.Scrubbed {
		t.Error("zero Hygiene.Scrubbed = true, want false")
	}
	if len(h.ScrubDetectors) != 0 {
		t.Errorf("zero Hygiene.ScrubDetectors = %v, want empty", h.ScrubDetectors)
	}
}
