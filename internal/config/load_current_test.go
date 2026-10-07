package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolateConfigEnv neutralizes every override that could shadow the project
// file under test, and points the user config root at an empty directory so
// only the project TOML can supply values.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(empty, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(empty, "data"))
	for _, v := range []string{
		"LITESPM_LOG_LEVEL", "LITEPSM_LOG_LEVEL",
		"LITESPM_POLICY_DEFAULT_LEVEL", "LITEPSM_POLICY_DEFAULT_LEVEL",
		"LITESPM_POLICY_ENFORCE_SIGNATURES", "LITEPSM_POLICY_ENFORCE_SIGNATURES",
		"LITESPM_NETWORK_TIMEOUT_SEC", "LITEPSM_NETWORK_TIMEOUT_SEC",
		"LITESPM_REGISTRY_URL", "LITEPSM_REGISTRY_URL",
		"LITESPM_CONFIG_ROOT", "LITEPSM_CONFIG_ROOT",
		"LITESPM_DATA_ROOT", "LITEPSM_DATA_ROOT",
		"LITESPM_RUNTIME_ROOT", "LITEPSM_RUNTIME_ROOT",
	} {
		t.Setenv(v, "")
	}
}

// The register item this covers: every command line entry point passed "" as
// the project directory, so step 3 of the documented precedence order (project
// .litespm/config.toml) could never fire. LoadCurrentConfig is what those call
// sites now use.
func TestLoadCurrentConfigReadsProjectTOML(t *testing.T) {
	isolateConfigEnv(t)

	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".litespm"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectTOML := "[daemon]\nlog_level = \"project-debug\"\n\n[policy]\ndefault_level = \"always_ask\"\n"
	if err := os.WriteFile(filepath.Join(proj, ".litespm", "config.toml"), []byte(projectTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	cfg, err := LoadCurrentConfig()
	if err != nil {
		t.Fatalf("LoadCurrentConfig: %v", err)
	}
	if cfg.Daemon.LogLevel != "project-debug" {
		t.Errorf("project log_level not applied: got %q", cfg.Daemon.LogLevel)
	}
	if cfg.Policy.DefaultLevel != "always_ask" {
		t.Errorf("project policy level not applied: got %q", cfg.Policy.DefaultLevel)
	}
	if cfg.Policy.EnforceSignatures != true {
		t.Error("unset project enforce_signatures must keep the compiled default true")
	}
}

func TestLoadCurrentConfigFallsBackToDefaultsOutsideAProject(t *testing.T) {
	isolateConfigEnv(t)
	t.Chdir(t.TempDir())

	cfg, err := LoadCurrentConfig()
	if err != nil {
		t.Fatalf("LoadCurrentConfig: %v", err)
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("log level outside a project = %q, want the compiled default", cfg.Daemon.LogLevel)
	}
	if cfg.Network.Timeout != 30*time.Second {
		t.Errorf("network timeout = %v, want 30s", cfg.Network.Timeout)
	}
}

// Environment still wins over the project file (precedence step 4).
func TestLoadCurrentConfigEnvBeatsProjectFile(t *testing.T) {
	isolateConfigEnv(t)

	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".litespm"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[policy]\ndefault_level = \"trust_curated\"\n\n[daemon]\nlog_level = \"from-project\"\n"
	if err := os.WriteFile(filepath.Join(proj, ".litespm", "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
	t.Setenv("LITESPM_POLICY_DEFAULT_LEVEL", "always_ask")
	t.Setenv("LITESPM_NETWORK_TIMEOUT_SEC", "45")

	cfg, err := LoadCurrentConfig()
	if err != nil {
		t.Fatalf("LoadCurrentConfig: %v", err)
	}
	if cfg.Policy.DefaultLevel != "always_ask" {
		t.Errorf("env must beat the project file: got %q", cfg.Policy.DefaultLevel)
	}
	if cfg.Daemon.LogLevel != "from-project" {
		t.Errorf("project file must apply where no env override exists: got %q", cfg.Daemon.LogLevel)
	}
	if cfg.Network.Timeout != 45*time.Second {
		t.Errorf("network timeout = %v, want 45s", cfg.Network.Timeout)
	}
}
