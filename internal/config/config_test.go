package config_test

import (
	"testing"

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
