package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opax-sh/opax/internal/types"
	"gopkg.in/yaml.v3"
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

// Validate checks an OpaxConfig for invalid values.
// Returns the first error found.
func Validate(cfg *OpaxConfig) error {
	if cfg.Privacy.Version <= 0 {
		return fmt.Errorf("config: validate: privacy.version: must be > 0")
	}

	if !cfg.Privacy.Scrubbing.Mode.Valid() {
		return fmt.Errorf("config: validate: scrubbing.mode: invalid value %q", cfg.Privacy.Scrubbing.Mode)
	}

	if !cfg.Privacy.DefaultTiers.Session.Valid() {
		return fmt.Errorf("config: validate: default_tiers.session: invalid value %q", cfg.Privacy.DefaultTiers.Session)
	}
	if !cfg.Privacy.DefaultTiers.Workflow.Valid() {
		return fmt.Errorf("config: validate: default_tiers.workflow: invalid value %q", cfg.Privacy.DefaultTiers.Workflow)
	}
	if !cfg.Privacy.DefaultTiers.Action.Valid() {
		return fmt.Errorf("config: validate: default_tiers.action: invalid value %q", cfg.Privacy.DefaultTiers.Action)
	}

	for _, p := range cfg.Privacy.Scrubbing.CustomPatterns {
		if p.Name == "" {
			return fmt.Errorf("config: validate: custom_patterns: pattern name must be non-empty")
		}
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("config: validate: custom_patterns[%s].pattern: %w", p.Name, err)
		}
	}

	for i, entry := range cfg.Privacy.Scrubbing.Allowlist {
		if strings.ContainsAny(entry, "*+?[(\\") {
			if _, err := regexp.Compile(entry); err != nil {
				return fmt.Errorf("config: validate: scrubbing.allowlist[%d]: %w", i, err)
			}
		}
	}

	if cfg.Privacy.Scrubbing.Entropy.Enabled {
		if cfg.Privacy.Scrubbing.Entropy.Threshold <= 0 {
			return fmt.Errorf("config: validate: scrubbing.entropy.threshold: must be > 0 when enabled")
		}
		if cfg.Privacy.Scrubbing.Entropy.MinLength <= 0 {
			return fmt.Errorf("config: validate: scrubbing.entropy.min_length: must be > 0 when enabled")
		}
	}

	for _, pair := range []struct{ name, val string }{
		{"storage.retention.hot", cfg.Storage.Retention.Hot},
		{"storage.retention.warm", cfg.Storage.Retention.Warm},
		{"storage.retention.compliance_floor", cfg.Storage.Retention.ComplianceFloor},
	} {
		if pair.val != "" {
			if _, err := ParseDuration(pair.val); err != nil {
				return fmt.Errorf("config: validate: %s: %w", pair.name, err)
			}
		}
	}

	if cfg.Trailers.Prefix != "" && !strings.HasSuffix(cfg.Trailers.Prefix, "-") {
		return fmt.Errorf("config: validate: trailers.prefix: must end with \"-\"")
	}

	return nil
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

// Load reads config from the hierarchy and returns the merged, validated result.
// repoRoot is the path to the git repository root (containing .opax/).
// Personal config is read from ~/.config/opax/config.yaml.
func Load(repoRoot string) (*OpaxConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("config: resolve home directory: %w", err)
	}
	personalDir := filepath.Join(home, ".config", "opax")
	return LoadWithPersonalDir(repoRoot, personalDir)
}

// LoadWithPersonalDir reads config with an explicit personal config directory.
// Exported for testing — production code should use Load().
func LoadWithPersonalDir(repoRoot, personalDir string) (*OpaxConfig, error) {
	cfg := Default()

	teamPath := filepath.Join(repoRoot, ".opax", "config.yaml")
	personalPath := filepath.Join(personalDir, "config.yaml")

	for _, path := range []string{teamPath, personalPath} {
		raw, err := readConfigFile(path)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			cfg = mergeRaw(cfg, raw)
		}
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// readConfigFile reads and decodes a YAML config file.
// Returns (nil, nil) if the file does not exist.
func readConfigFile(path string) (*rawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return decodeYAML(data, path)
}

// rawConfig mirrors OpaxConfig but uses pointer fields to distinguish
// "not set" from zero values during YAML decode.
type rawConfig struct {
	Privacy  *rawPrivacy  `yaml:"privacy"`
	Storage  *rawStorage  `yaml:"storage"`
	Capture  *rawCapture  `yaml:"capture"`
	Trailers *rawTrailers `yaml:"trailers"`
}

type rawPrivacy struct {
	Version      *int             `yaml:"version"`
	Scrubbing    *rawScrubbing    `yaml:"scrubbing"`
	DefaultTiers *rawDefaultTiers `yaml:"default_tiers"`
}

type rawScrubbing struct {
	Mode             *types.ScrubMode `yaml:"mode"`
	BuiltinDetectors []string         `yaml:"builtin_detectors"`
	CustomPatterns   []PatternConfig  `yaml:"custom_patterns"`
	SourceFiles      []string         `yaml:"source_files"`
	Entropy          *rawEntropy      `yaml:"entropy"`
	Allowlist        []string         `yaml:"allowlist"`
}

type rawEntropy struct {
	Enabled   *bool    `yaml:"enabled"`
	Threshold *float64 `yaml:"threshold"`
	MinLength *int     `yaml:"min_length"`
}

type rawDefaultTiers struct {
	Session  *types.PrivacyTier `yaml:"session"`
	Workflow *types.PrivacyTier `yaml:"workflow"`
	Action   *types.PrivacyTier `yaml:"action"`
}

type rawStorage struct {
	Retention *rawRetention `yaml:"retention"`
}

type rawRetention struct {
	Hot             *string `yaml:"hot"`
	Warm            *string `yaml:"warm"`
	ComplianceFloor *string `yaml:"compliance_floor"`
}

type rawCapture struct {
	EnabledSources []string          `yaml:"enabled_sources"`
	LastCapture    map[string]string `yaml:"last_capture"`
}

type rawTrailers struct {
	Enabled *bool   `yaml:"enabled"`
	Prefix  *string `yaml:"prefix"`
}

// decodeYAML parses YAML bytes with strict field checking.
// filePath is used in error messages only.
func decodeYAML(data []byte, filePath string) (*rawConfig, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var raw rawConfig
	if err := dec.Decode(&raw); err != nil {
		if err == io.EOF {
			return &rawConfig{}, nil
		}
		return nil, fmt.Errorf("config: %s: %w", filePath, err)
	}
	return &raw, nil
}

// mergeRaw merges a raw (pointer-based) YAML overlay over a concrete base.
// This correctly handles zero-value overrides (e.g., enabled: false).
func mergeRaw(base *OpaxConfig, raw *rawConfig) *OpaxConfig {
	result := *base
	if raw == nil {
		return &result
	}

	if raw.Privacy != nil {
		if raw.Privacy.Version != nil {
			result.Privacy.Version = *raw.Privacy.Version
		}
		if raw.Privacy.Scrubbing != nil {
			s := raw.Privacy.Scrubbing
			if s.Mode != nil {
				result.Privacy.Scrubbing.Mode = *s.Mode
			}
			if s.BuiltinDetectors != nil {
				result.Privacy.Scrubbing.BuiltinDetectors = s.BuiltinDetectors
			}
			if s.CustomPatterns != nil {
				result.Privacy.Scrubbing.CustomPatterns = s.CustomPatterns
			}
			if s.SourceFiles != nil {
				result.Privacy.Scrubbing.SourceFiles = s.SourceFiles
			}
			if s.Entropy != nil {
				if s.Entropy.Enabled != nil {
					result.Privacy.Scrubbing.Entropy.Enabled = *s.Entropy.Enabled
				}
				if s.Entropy.Threshold != nil {
					result.Privacy.Scrubbing.Entropy.Threshold = *s.Entropy.Threshold
				}
				if s.Entropy.MinLength != nil {
					result.Privacy.Scrubbing.Entropy.MinLength = *s.Entropy.MinLength
				}
			}
			if s.Allowlist != nil {
				result.Privacy.Scrubbing.Allowlist = s.Allowlist
			}
		}
		if raw.Privacy.DefaultTiers != nil {
			dt := raw.Privacy.DefaultTiers
			if dt.Session != nil {
				result.Privacy.DefaultTiers.Session = *dt.Session
			}
			if dt.Workflow != nil {
				result.Privacy.DefaultTiers.Workflow = *dt.Workflow
			}
			if dt.Action != nil {
				result.Privacy.DefaultTiers.Action = *dt.Action
			}
		}
	}

	if raw.Storage != nil && raw.Storage.Retention != nil {
		r := raw.Storage.Retention
		if r.Hot != nil {
			result.Storage.Retention.Hot = *r.Hot
		}
		if r.Warm != nil {
			result.Storage.Retention.Warm = *r.Warm
		}
		if r.ComplianceFloor != nil {
			result.Storage.Retention.ComplianceFloor = *r.ComplianceFloor
		}
	}

	if raw.Capture != nil {
		if raw.Capture.EnabledSources != nil {
			result.Capture.EnabledSources = raw.Capture.EnabledSources
		}
		if raw.Capture.LastCapture != nil {
			merged := make(map[string]string)
			for k, v := range result.Capture.LastCapture {
				merged[k] = v
			}
			for k, v := range raw.Capture.LastCapture {
				merged[k] = v
			}
			result.Capture.LastCapture = merged
		}
	}

	if raw.Trailers != nil {
		if raw.Trailers.Enabled != nil {
			result.Trailers.Enabled = *raw.Trailers.Enabled
		}
		if raw.Trailers.Prefix != nil {
			result.Trailers.Prefix = *raw.Trailers.Prefix
		}
	}

	return &result
}
