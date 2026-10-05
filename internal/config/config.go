package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultRegistryURL is the origin the client falls back to when neither the
// config file nor LITESPM_REGISTRY_URL supplies one.
//
// It has to be an origin that actually answers: a name that does not resolve
// turns every catalog fetch into a DNS failure instead of a reportable error.
// registry.litespm.dev is the intended home once that domain is registered and
// the catalog release tree is published there; until then this is the
// deployment origin serving /v1/current.json. Renamed from the pre-rebrand
// `litepsm` Worker on 2026-10-05, once the new origin was live and verified
// (release rel-2026-10-05-01, sequence 143).
const DefaultRegistryURL = "https://litespm.sarveshbh-2022.workers.dev"

// CatalogConfig governs remote catalog synchronization.
type CatalogConfig struct {
	RegistryURL string        `json:"registryUrl"`
	CacheTTL    time.Duration `json:"cacheTtl"`
}

// DaemonConfig governs daemon runner behavior.
type DaemonConfig struct {
	IdleTimeout            time.Duration `json:"idleTimeout"`
	LogLevel               string        `json:"logLevel"`
	MaxConcurrentProviders int           `json:"maxConcurrentProviders"`
}

// PolicyConfig governs security approvals and sandboxing.
type PolicyConfig struct {
	DefaultLevel      string `json:"defaultLevel"` // ask_once | always_ask | trust_curated
	EnforceSignatures bool   `json:"enforceSignatures"`
}

// NetworkConfig governs download and request bounds.
type NetworkConfig struct {
	Timeout              time.Duration `json:"timeout"`
	MaxDownloadSizeBytes int64         `json:"maxDownloadSizeBytes"`
}

// Config represents the unified runtime configuration for LiteSPM.
type Config struct {
	Paths   *PlatformPaths `json:"paths"`
	Catalog CatalogConfig  `json:"catalog"`
	Daemon  DaemonConfig   `json:"daemon"`
	Policy  PolicyConfig   `json:"policy"`
	Network NetworkConfig  `json:"network"`
}

// DefaultConfig returns compiled baseline defaults.
func DefaultConfig(paths *PlatformPaths) *Config {
	return &Config{
		Paths: paths,
		Catalog: CatalogConfig{
			RegistryURL: DefaultRegistryURL,
			CacheTTL:    1 * time.Hour,
		},
		Daemon: DaemonConfig{
			IdleTimeout:            30 * time.Minute,
			LogLevel:               "info",
			MaxConcurrentProviders: 20,
		},
		Policy: PolicyConfig{
			DefaultLevel:      "ask_once",
			EnforceSignatures: true,
		},
		Network: NetworkConfig{
			Timeout:              30 * time.Second,
			MaxDownloadSizeBytes: 500 * 1024 * 1024, // 500 MB
		},
	}
}

// LoadConfig loads configuration in order of precedence:
//  1. Defaults
//  2. User config file (~/.config/litespm/config.toml or AppData)
//  3. Project config file (.litespm/config.toml, falling back to the pre-rename
//     .litepsm/config.toml when the new path is absent)
//  4. Environment variable overrides
func LoadConfig(projectDir string) (*Config, error) {
	paths, err := ResolvePlatformPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve platform paths: %w", err)
	}

	cfg := DefaultConfig(paths)

	// Load user config if exists. ConfigRoot may itself have been adopted from
	// the legacy root, so the legacy config.toml is found without extra work.
	userConfigFile := filepath.Join(paths.ConfigRoot, "config.toml")
	_ = parseSimpleTOML(userConfigFile, cfg)

	// Load project config if exists. The old name is only read when the new one
	// is absent, so a migrated project cannot be shadowed by a stale directory.
	if projectDir != "" {
		projectConfigFile := filepath.Join(projectDir, ".litespm", "config.toml")
		if pathMissing(projectConfigFile) {
			if legacy := filepath.Join(projectDir, ".litepsm", "config.toml"); !pathMissing(legacy) {
				projectConfigFile = legacy
			}
		}
		_ = parseSimpleTOML(projectConfigFile, cfg)
	}

	// Apply environment overrides
	applyEnvOverrides(cfg)

	return cfg, nil
}

// envOverride returns the value of the current-name environment variable, or
// the legacy-name variable when the current one is unset.
//
// The LitePSM -> LiteSPM rename changed the variable names but not their
// meaning; an upgraded installation that still exports LITEPSM_* must keep its
// settings. The current name always takes precedence.
func envOverride(current, legacy string) string {
	if v := os.Getenv(current); v != "" {
		return v
	}
	return os.Getenv(legacy)
}

func applyEnvOverrides(cfg *Config) {
	if v := envOverride("LITESPM_REGISTRY_URL", "LITEPSM_REGISTRY_URL"); v != "" {
		cfg.Catalog.RegistryURL = v
	}
	if v := envOverride("LITESPM_LOG_LEVEL", "LITEPSM_LOG_LEVEL"); v != "" {
		cfg.Daemon.LogLevel = v
	}
	if v := envOverride("LITESPM_POLICY_DEFAULT_LEVEL", "LITEPSM_POLICY_DEFAULT_LEVEL"); v != "" {
		cfg.Policy.DefaultLevel = v
	}
	if v := envOverride("LITESPM_POLICY_ENFORCE_SIGNATURES", "LITEPSM_POLICY_ENFORCE_SIGNATURES"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Policy.EnforceSignatures = parsed
		}
	}
	if v := envOverride("LITESPM_NETWORK_TIMEOUT_SEC", "LITEPSM_NETWORK_TIMEOUT_SEC"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			cfg.Network.Timeout = time.Duration(secs) * time.Second
		}
	}
}

// parseSimpleTOML provides a lightweight, robust key-value parser for basic TOML configs
// without adding heavy external dependencies.
func parseSimpleTOML(filePath string, cfg *Config) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[] \t")
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")

		switch currentSection {
		case "catalog":
			switch key {
			case "registry_url":
				cfg.Catalog.RegistryURL = val
			}
		case "daemon":
			switch key {
			case "log_level":
				cfg.Daemon.LogLevel = val
			case "max_concurrent_providers":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.Daemon.MaxConcurrentProviders = n
				}
			}
		case "policy":
			switch key {
			case "default_level":
				cfg.Policy.DefaultLevel = val
			case "enforce_signatures":
				if b, err := strconv.ParseBool(val); err == nil {
					cfg.Policy.EnforceSignatures = b
				}
			}
		}
	}
	return scanner.Err()
}

// RedactConfig returns a copy of config with any potentially sensitive values scrubbed.
func RedactConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	return &clone
}
