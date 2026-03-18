package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/opax-sh/opax/internal/types"
)

// OpaxConfig is the top-level configuration for Opax.
type OpaxConfig struct {
	Privacy  PrivacyConfig  `yaml:"privacy"`
	Storage  StorageConfig  `yaml:"storage"`
	Capture  CaptureConfig  `yaml:"capture"`
	Trailers TrailersConfig `yaml:"trailers"`
}

// PrivacyConfig controls scrubbing behavior and default privacy tiers.
type PrivacyConfig struct {
	Version      int                `yaml:"version"`
	Scrubbing    ScrubbingConfig    `yaml:"scrubbing"`
	DefaultTiers DefaultTiersConfig `yaml:"default_tiers"`
}

// ScrubbingConfig controls how secrets are detected and handled.
type ScrubbingConfig struct {
	Mode             types.ScrubMode `yaml:"mode"`
	BuiltinDetectors []string        `yaml:"builtin_detectors"`
	CustomPatterns   []PatternConfig `yaml:"custom_patterns"`
	SourceFiles      []string        `yaml:"source_files"`
	Entropy          EntropyConfig   `yaml:"entropy"`
	Allowlist        []string        `yaml:"allowlist"`
}

// PatternConfig defines a custom regex pattern for secret detection.
type PatternConfig struct {
	Name        string `yaml:"name"`
	Pattern     string `yaml:"pattern"`
	Description string `yaml:"description"`
}

// EntropyConfig controls high-entropy string detection.
type EntropyConfig struct {
	Enabled   bool    `yaml:"enabled"`
	Threshold float64 `yaml:"threshold"`
	MinLength int     `yaml:"min_length"`
}

// DefaultTiersConfig sets the default privacy tier for each record type.
type DefaultTiersConfig struct {
	Session  types.PrivacyTier `yaml:"session"`
	Workflow types.PrivacyTier `yaml:"workflow"`
	Action   types.PrivacyTier `yaml:"action"`
}

// StorageConfig controls retention and storage behavior.
type StorageConfig struct {
	Retention RetentionConfig `yaml:"retention"`
}

// RetentionConfig defines how long data is kept at each tier.
// Values are duration strings: "30d", "90d", "3y".
type RetentionConfig struct {
	Hot             string `yaml:"hot"`
	Warm            string `yaml:"warm"`
	ComplianceFloor string `yaml:"compliance_floor"`
}

// CaptureConfig controls which agent platforms are captured from.
type CaptureConfig struct {
	EnabledSources []string          `yaml:"enabled_sources"`
	LastCapture    map[string]string `yaml:"last_capture"`
}

// TrailersConfig controls git commit trailer behavior.
type TrailersConfig struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
}

// ParseDuration parses a human-readable duration string.
// Supported formats: "30d", "12w", "3m", "1y"
// Units: d=day(24h), w=week(7d), m=month(30d), y=year(365d)
func ParseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("config: invalid duration %q", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("config: invalid duration %q: %w", s, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("config: invalid duration %q: must be positive", s)
	}

	var multiplier time.Duration
	switch unit {
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	case 'm':
		multiplier = 30 * 24 * time.Hour
	case 'y':
		multiplier = 365 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("config: invalid duration %q: unknown unit %q", s, string(unit))
	}

	return time.Duration(n) * multiplier, nil
}

// Default returns the SDK default configuration.
// These values apply when no config file exists or when a field is omitted.
func Default() *OpaxConfig {
	return &OpaxConfig{
		Privacy: PrivacyConfig{
			Version: 1,
			Scrubbing: ScrubbingConfig{
				Mode: types.ScrubRedact,
				BuiltinDetectors: []string{
					"aws_keys",
					"github_tokens",
					"jwt_tokens",
					"private_keys",
					"connection_strings",
					"generic_api_keys",
				},
				CustomPatterns: []PatternConfig{},
				SourceFiles:    []string{".env", ".env.local"},
				Entropy: EntropyConfig{
					Enabled:   true,
					Threshold: 4.5,
					MinLength: 20,
				},
				Allowlist: []string{},
			},
			DefaultTiers: DefaultTiersConfig{
				Session:  types.TierTeam,
				Workflow: types.TierTeam,
				Action:   types.TierTeam,
			},
		},
		Storage: StorageConfig{
			Retention: RetentionConfig{
				Hot:             "30d",
				Warm:            "90d",
				ComplianceFloor: "",
			},
		},
		Capture: CaptureConfig{
			EnabledSources: []string{},
			LastCapture:    map[string]string{},
		},
		Trailers: TrailersConfig{
			Enabled: true,
			Prefix:  "Opax-",
		},
	}
}
