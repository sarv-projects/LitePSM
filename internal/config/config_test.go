package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolvePlatformPaths(t *testing.T) {
	paths, err := ResolvePlatformPaths()
	if err != nil {
		t.Fatalf("failed to resolve platform paths: %v", err)
	}

	if paths.DataRoot == "" {
		t.Fatal("DataRoot must not be empty")
	}
	if paths.ConfigRoot == "" {
		t.Fatal("ConfigRoot must not be empty")
	}
	if paths.RuntimeRoot == "" {
		t.Fatal("RuntimeRoot must not be empty")
	}
	if paths.IPCEndpoint() == "" {
		t.Fatal("IPCEndpoint must not be empty")
	}

	if paths.StateDBPath() == "" || paths.DaemonLockPath() == "" || paths.CASPath() == "" {
		t.Fatal("sub-paths must not be empty")
	}
}

func TestEnsureDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "litepsm-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	paths := &PlatformPaths{
		DataRoot:    filepath.Join(tmpDir, "data"),
		ConfigRoot:  filepath.Join(tmpDir, "config"),
		RuntimeRoot: filepath.Join(tmpDir, "run"),
	}

	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	checkDirs := []string{
		paths.DataRoot,
		paths.ConfigRoot,
		paths.RuntimeRoot,
		paths.CASPath(),
		paths.BackupsPath(),
		paths.StagingPath(),
	}

	for _, d := range checkDirs {
		stat, err := os.Stat(d)
		if err != nil || !stat.IsDir() {
			t.Fatalf("expected directory %s to exist as a directory, got stat err: %v", d, err)
		}
	}
}

func TestConfigLoadingAndOverrides(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "litepsm-test-conf-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set env overrides
	os.Setenv("LITEPSM_DATA_ROOT", filepath.Join(tmpDir, "data"))
	os.Setenv("LITEPSM_CONFIG_ROOT", filepath.Join(tmpDir, "config"))
	os.Setenv("LITEPSM_REGISTRY_URL", "https://custom.registry.io")
	os.Setenv("LITEPSM_LOG_LEVEL", "debug")
	os.Setenv("LITEPSM_NETWORK_TIMEOUT_SEC", "45")
	defer func() {
		os.Unsetenv("LITEPSM_DATA_ROOT")
		os.Unsetenv("LITEPSM_CONFIG_ROOT")
		os.Unsetenv("LITEPSM_REGISTRY_URL")
		os.Unsetenv("LITEPSM_LOG_LEVEL")
		os.Unsetenv("LITEPSM_NETWORK_TIMEOUT_SEC")
	}()

	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Catalog.RegistryURL != "https://custom.registry.io" {
		t.Fatalf("expected custom registry url, got %s", cfg.Catalog.RegistryURL)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Fatalf("expected log level debug, got %s", cfg.Daemon.LogLevel)
	}
	if cfg.Network.Timeout != 45*time.Second {
		t.Fatalf("expected network timeout 45s, got %v", cfg.Network.Timeout)
	}

	redacted := RedactConfig(cfg)
	if redacted == nil {
		t.Fatal("redacted config should not be nil")
	}
}
