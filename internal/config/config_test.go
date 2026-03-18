package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opax-sh/opax/internal/config"
	"github.com/opax-sh/opax/internal/types"
)

func TestDefault(t *testing.T) {
	cfg := config.Default()

	// Privacy section
	if cfg.Privacy.Version != 1 {
		t.Errorf("Privacy.Version = %d, want 1", cfg.Privacy.Version)
	}
	if cfg.Privacy.Scrubbing.Mode != types.ScrubRedact {
		t.Errorf("Scrubbing.Mode = %q, want %q", cfg.Privacy.Scrubbing.Mode, types.ScrubRedact)
	}

	wantDetectors := []string{
		"aws_keys", "github_tokens", "jwt_tokens",
		"private_keys", "connection_strings", "generic_api_keys",
	}
	if len(cfg.Privacy.Scrubbing.BuiltinDetectors) != len(wantDetectors) {
		t.Fatalf("BuiltinDetectors len = %d, want %d",
			len(cfg.Privacy.Scrubbing.BuiltinDetectors), len(wantDetectors))
	}
	for i, d := range wantDetectors {
		if cfg.Privacy.Scrubbing.BuiltinDetectors[i] != d {
			t.Errorf("BuiltinDetectors[%d] = %q, want %q", i, cfg.Privacy.Scrubbing.BuiltinDetectors[i], d)
		}
	}

	if len(cfg.Privacy.Scrubbing.CustomPatterns) != 0 {
		t.Errorf("CustomPatterns len = %d, want 0", len(cfg.Privacy.Scrubbing.CustomPatterns))
	}

	wantSourceFiles := []string{".env", ".env.local"}
	if len(cfg.Privacy.Scrubbing.SourceFiles) != len(wantSourceFiles) {
		t.Fatalf("SourceFiles len = %d, want %d",
			len(cfg.Privacy.Scrubbing.SourceFiles), len(wantSourceFiles))
	}
	for i, f := range wantSourceFiles {
		if cfg.Privacy.Scrubbing.SourceFiles[i] != f {
			t.Errorf("SourceFiles[%d] = %q, want %q", i, cfg.Privacy.Scrubbing.SourceFiles[i], f)
		}
	}

	if !cfg.Privacy.Scrubbing.Entropy.Enabled {
		t.Error("Entropy.Enabled = false, want true")
	}
	if cfg.Privacy.Scrubbing.Entropy.Threshold != 4.5 {
		t.Errorf("Entropy.Threshold = %f, want 4.5", cfg.Privacy.Scrubbing.Entropy.Threshold)
	}
	if cfg.Privacy.Scrubbing.Entropy.MinLength != 20 {
		t.Errorf("Entropy.MinLength = %d, want 20", cfg.Privacy.Scrubbing.Entropy.MinLength)
	}

	if len(cfg.Privacy.Scrubbing.Allowlist) != 0 {
		t.Errorf("Allowlist len = %d, want 0", len(cfg.Privacy.Scrubbing.Allowlist))
	}

	// Default tiers
	if cfg.Privacy.DefaultTiers.Session != types.TierTeam {
		t.Errorf("DefaultTiers.Session = %q, want %q", cfg.Privacy.DefaultTiers.Session, types.TierTeam)
	}
	if cfg.Privacy.DefaultTiers.Workflow != types.TierTeam {
		t.Errorf("DefaultTiers.Workflow = %q, want %q", cfg.Privacy.DefaultTiers.Workflow, types.TierTeam)
	}
	if cfg.Privacy.DefaultTiers.Action != types.TierTeam {
		t.Errorf("DefaultTiers.Action = %q, want %q", cfg.Privacy.DefaultTiers.Action, types.TierTeam)
	}

	// Storage section
	if cfg.Storage.Retention.Hot != "30d" {
		t.Errorf("Retention.Hot = %q, want %q", cfg.Storage.Retention.Hot, "30d")
	}
	if cfg.Storage.Retention.Warm != "90d" {
		t.Errorf("Retention.Warm = %q, want %q", cfg.Storage.Retention.Warm, "90d")
	}
	if cfg.Storage.Retention.ComplianceFloor != "" {
		t.Errorf("Retention.ComplianceFloor = %q, want %q", cfg.Storage.Retention.ComplianceFloor, "")
	}

	// Capture section
	if len(cfg.Capture.EnabledSources) != 0 {
		t.Errorf("EnabledSources len = %d, want 0", len(cfg.Capture.EnabledSources))
	}
	if cfg.Capture.LastCapture == nil {
		t.Error("LastCapture is nil, want empty map")
	}
	if len(cfg.Capture.LastCapture) != 0 {
		t.Errorf("LastCapture len = %d, want 0", len(cfg.Capture.LastCapture))
	}

	// Trailers section
	if !cfg.Trailers.Enabled {
		t.Error("Trailers.Enabled = false, want true")
	}
	if cfg.Trailers.Prefix != "Opax-" {
		t.Errorf("Trailers.Prefix = %q, want %q", cfg.Trailers.Prefix, "Opax-")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "days", input: "30d", want: 30 * 24 * time.Hour},
		{name: "single day", input: "1d", want: 24 * time.Hour},
		{name: "weeks", input: "12w", want: 12 * 7 * 24 * time.Hour},
		{name: "months", input: "3m", want: 3 * 30 * 24 * time.Hour},
		{name: "years", input: "1y", want: 365 * 24 * time.Hour},
		{name: "three years", input: "3y", want: 3 * 365 * 24 * time.Hour},
		{name: "empty string", input: "", wantErr: true},
		{name: "no unit", input: "30", wantErr: true},
		{name: "no number", input: "d", wantErr: true},
		{name: "invalid unit", input: "30x", wantErr: true},
		{name: "negative", input: "-5d", wantErr: true},
		{name: "zero", input: "0d", wantErr: true},
		{name: "float", input: "1.5d", wantErr: true},
		{name: "invalid string", input: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseDuration(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseDuration(%q) error = %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    types.ScrubMode
		wantErr bool
	}{
		{"redact", types.ScrubRedact, false},
		{"reject", types.ScrubReject, false},
		{"warn", types.ScrubWarn, false},
		{"invalid", types.ScrubMode("nope"), true},
		{"empty", types.ScrubMode(""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Privacy.Scrubbing.Mode = tt.mode
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() mode=%q error = %v, wantErr %v", tt.mode, err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "scrubbing.mode") {
				t.Errorf("error %q should mention scrubbing.mode", err)
			}
		})
	}
}

func TestValidateTiers(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		tier    types.PrivacyTier
		wantErr bool
	}{
		{"session-public", "session", types.TierPublic, false},
		{"session-team", "session", types.TierTeam, false},
		{"session-private", "session", types.TierPrivate, false},
		{"session-invalid", "session", types.PrivacyTier("nope"), true},
		{"workflow-invalid", "workflow", types.PrivacyTier("nope"), true},
		{"action-invalid", "action", types.PrivacyTier("nope"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			switch tt.field {
			case "session":
				cfg.Privacy.DefaultTiers.Session = tt.tier
			case "workflow":
				cfg.Privacy.DefaultTiers.Workflow = tt.tier
			case "action":
				cfg.Privacy.DefaultTiers.Action = tt.tier
			}
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() %s=%q error = %v, wantErr %v", tt.field, tt.tier, err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "default_tiers."+tt.field) {
				t.Errorf("error %q should mention default_tiers.%s", err, tt.field)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	cfg := config.Default()
	cfg.Privacy.Version = 0
	err := config.Validate(cfg)
	if err == nil {
		t.Error("Validate() version=0 should error")
	}
	if err != nil && !strings.Contains(err.Error(), "privacy.version") {
		t.Errorf("error %q should mention privacy.version", err)
	}
}

func TestValidateCustomPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern config.PatternConfig
		wantErr bool
	}{
		{
			name:    "valid pattern",
			pattern: config.PatternConfig{Name: "test", Pattern: `\d+`, Description: "digits"},
			wantErr: false,
		},
		{
			name:    "invalid regex",
			pattern: config.PatternConfig{Name: "bad", Pattern: `[invalid`, Description: "broken"},
			wantErr: true,
		},
		{
			name:    "empty name",
			pattern: config.PatternConfig{Name: "", Pattern: `\d+`, Description: "digits"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Privacy.Scrubbing.CustomPatterns = []config.PatternConfig{tt.pattern}
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.pattern.Name != "" && err != nil && !strings.Contains(err.Error(), tt.pattern.Name) {
				t.Errorf("error %q should mention pattern name %q", err, tt.pattern.Name)
			}
		})
	}
}

func TestValidateAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		wantErr   bool
	}{
		{"literal strings", []string{"SAFE_TOKEN", "PUBLIC_KEY"}, false},
		{"valid regex", []string{`SAFE_\w+`}, false},
		{"invalid regex", []string{`[bad`}, true},
		{"empty list", []string{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Privacy.Scrubbing.Allowlist = tt.allowlist
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() allowlist=%v error = %v, wantErr %v", tt.allowlist, err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "allowlist") {
				t.Errorf("error %q should mention allowlist", err)
			}
		})
	}
}

func TestValidateRetention(t *testing.T) {
	tests := []struct {
		name    string
		hot     string
		wantErr bool
	}{
		{"valid", "30d", false},
		{"empty is ok", "", false},
		{"invalid", "nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Storage.Retention.Hot = tt.hot
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() hot=%q error = %v, wantErr %v", tt.hot, err, tt.wantErr)
			}
		})
	}
}

func TestValidateTrailerPrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		{"valid", "Opax-", false},
		{"empty is ok", "", false},
		{"no trailing dash", "NoTrailingDash", true},
		{"custom valid", "My-", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Trailers.Prefix = tt.prefix
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() prefix=%q error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEntropy(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		threshold float64
		minLength int
		wantErr   bool
	}{
		{"enabled valid", true, 4.5, 20, false},
		{"disabled zero values ok", false, 0, 0, false},
		{"enabled zero threshold", true, 0, 20, true},
		{"enabled negative threshold", true, -1.0, 20, true},
		{"enabled zero min_length", true, 4.5, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Privacy.Scrubbing.Entropy.Enabled = tt.enabled
			cfg.Privacy.Scrubbing.Entropy.Threshold = tt.threshold
			cfg.Privacy.Scrubbing.Entropy.MinLength = tt.minLength
			err := config.Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Test helpers ---

func writeTeamConfig(t *testing.T, repoRoot, content string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".opax")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePersonalConfig(t *testing.T, personalDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(personalDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Load tests ---

func TestLoadNoFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := config.Default()
	if cfg.Privacy.Version != want.Privacy.Version {
		t.Errorf("Version = %d, want %d", cfg.Privacy.Version, want.Privacy.Version)
	}
	if cfg.Privacy.Scrubbing.Mode != want.Privacy.Scrubbing.Mode {
		t.Errorf("Mode = %q, want %q", cfg.Privacy.Scrubbing.Mode, want.Privacy.Scrubbing.Mode)
	}
}

func TestLoadTeamOnly(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "privacy:\n  scrubbing:\n    mode: reject\n")

	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Privacy.Scrubbing.Mode != types.ScrubReject {
		t.Errorf("Mode = %q, want %q", cfg.Privacy.Scrubbing.Mode, types.ScrubReject)
	}
	if cfg.Privacy.Version != 1 {
		t.Errorf("Version = %d, want 1 (default preserved)", cfg.Privacy.Version)
	}
}

func TestLoadTeamAndPersonal(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "privacy:\n  scrubbing:\n    mode: reject\n    builtin_detectors:\n      - aws_keys\n      - github_tokens\n")

	personalDir := t.TempDir()
	writePersonalConfig(t, personalDir, "privacy:\n  scrubbing:\n    mode: warn\n    builtin_detectors:\n      - aws_keys\n")

	cfg, err := config.LoadWithPersonalDir(dir, personalDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Privacy.Scrubbing.Mode != types.ScrubWarn {
		t.Errorf("Mode = %q, want %q (personal override)", cfg.Privacy.Scrubbing.Mode, types.ScrubWarn)
	}
	if len(cfg.Privacy.Scrubbing.BuiltinDetectors) != 1 {
		t.Fatalf("BuiltinDetectors len = %d, want 1 (slice replace)", len(cfg.Privacy.Scrubbing.BuiltinDetectors))
	}
}

// --- Merge semantics tests ---

func TestMergeScalarOverride(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "privacy:\n  scrubbing:\n    mode: reject\n")

	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Privacy.Scrubbing.Mode != types.ScrubReject {
		t.Errorf("Mode = %q, want %q", cfg.Privacy.Scrubbing.Mode, types.ScrubReject)
	}
	if cfg.Privacy.Version != 1 {
		t.Errorf("Version = %d, want 1 (default)", cfg.Privacy.Version)
	}
}

func TestMergeSliceReplace(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "privacy:\n  scrubbing:\n    builtin_detectors:\n      - aws_keys\n")

	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Privacy.Scrubbing.BuiltinDetectors) != 1 {
		t.Fatalf("BuiltinDetectors len = %d, want 1", len(cfg.Privacy.Scrubbing.BuiltinDetectors))
	}
	if cfg.Privacy.Scrubbing.BuiltinDetectors[0] != "aws_keys" {
		t.Errorf("BuiltinDetectors[0] = %q, want %q", cfg.Privacy.Scrubbing.BuiltinDetectors[0], "aws_keys")
	}
}

func TestMergeMapMerge(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "capture:\n  last_capture:\n    claude-code: \"2025-01-01T00:00:00Z\"\n    codex: \"2025-01-02T00:00:00Z\"\n")

	personalDir := t.TempDir()
	writePersonalConfig(t, personalDir, "capture:\n  last_capture:\n    claude-code: \"2025-02-01T00:00:00Z\"\n    cursor: \"2025-02-01T00:00:00Z\"\n")

	cfg, err := config.LoadWithPersonalDir(dir, personalDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Capture.LastCapture["claude-code"] != "2025-02-01T00:00:00Z" {
		t.Errorf("claude-code = %q, want override", cfg.Capture.LastCapture["claude-code"])
	}
	if cfg.Capture.LastCapture["codex"] != "2025-01-02T00:00:00Z" {
		t.Errorf("codex = %q, want base preserved", cfg.Capture.LastCapture["codex"])
	}
	if cfg.Capture.LastCapture["cursor"] != "2025-02-01T00:00:00Z" {
		t.Errorf("cursor = %q, want overlay added", cfg.Capture.LastCapture["cursor"])
	}
}

func TestBooleanFalseOverride(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "trailers:\n  enabled: false\n")

	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Trailers.Enabled {
		t.Error("Trailers.Enabled = true, want false (explicit override of default true)")
	}
}

// --- Strict parsing tests ---

func TestStrictUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "unknown_key: value\n")

	_, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err == nil {
		t.Error("Load() should reject unknown keys")
	}
	if err != nil && !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("error %q should contain file path", err)
	}
}

// --- Edge case tests ---

func TestEmptyConfigFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"document separator", "---\n"},
		{"whitespace only", "   \n\n"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTeamConfig(t, dir, tt.content)

			cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Privacy.Version != 1 {
				t.Errorf("Version = %d, want 1 (default)", cfg.Privacy.Version)
			}
		})
	}
}

func TestPartialConfig(t *testing.T) {
	dir := t.TempDir()
	writeTeamConfig(t, dir, "privacy:\n  scrubbing:\n    mode: reject\n")

	cfg, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Privacy.Scrubbing.Mode != types.ScrubReject {
		t.Errorf("Mode = %q, want reject", cfg.Privacy.Scrubbing.Mode)
	}
	if !cfg.Trailers.Enabled {
		t.Error("Trailers.Enabled should be default true")
	}
	if cfg.Trailers.Prefix != "Opax-" {
		t.Errorf("Trailers.Prefix = %q, want default", cfg.Trailers.Prefix)
	}
}

func TestLoadConfigIsDirectory(t *testing.T) {
	dir := t.TempDir()
	teamDir := filepath.Join(dir, ".opax")
	if err := os.MkdirAll(filepath.Join(teamDir, "config.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err == nil {
		t.Error("Load() should error when config.yaml is a directory")
	}
	if err != nil && !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("error %q should mention file path", err)
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permission test unreliable")
	}
	dir := t.TempDir()
	teamDir := filepath.Join(dir, ".opax")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(teamDir, "config.yaml")
	if err := os.WriteFile(path, []byte("privacy:\n  version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	_, err := config.LoadWithPersonalDir(dir, t.TempDir())
	if err == nil {
		t.Error("Load() should error for unreadable file")
	}
	if err != nil && !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("error %q should mention file path", err)
	}
}
