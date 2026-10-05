package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// clearRootEnv removes both the current-name and legacy-name path overrides so
// a test controls root resolution completely, independent of the ambient CI
// environment.
func clearRootEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LITESPM_DATA_ROOT", "LITESPM_CONFIG_ROOT", "LITESPM_RUNTIME_ROOT",
		"LITEPSM_DATA_ROOT", "LITEPSM_CONFIG_ROOT", "LITEPSM_RUNTIME_ROOT",
	} {
		t.Setenv(k, "")
	}
}

// TestResolveRootAdoptsLegacyOnly pins the four existence combinations from the
// rename compatibility rule: neither, legacy-only, both, new-only.
func TestResolveRootAdoptsLegacyOnly(t *testing.T) {
	base := t.TempDir()
	newDefault := filepath.Join(base, "new")
	legacyDefault := filepath.Join(base, "legacy")
	t.Setenv("TEST_ROOT_NEW", "")
	t.Setenv("TEST_ROOT_LEGACY", "")

	// Neither exists: keep the new default.
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != newDefault {
		t.Fatalf("neither present: got %q want %q", got, newDefault)
	}

	// Legacy only: adopt the legacy root.
	if err := os.MkdirAll(legacyDefault, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != legacyDefault {
		t.Fatalf("legacy only: got %q want %q", got, legacyDefault)
	}

	// Both present: the new root wins.
	if err := os.MkdirAll(newDefault, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != newDefault {
		t.Fatalf("both present: got %q want %q", got, newDefault)
	}

	// New only: keep the new root.
	if err := os.RemoveAll(legacyDefault); err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != newDefault {
		t.Fatalf("new only: got %q want %q", got, newDefault)
	}
}

// TestResolveRootEnvPrecedence proves the legacy environment name is a fallback
// and the current name wins when both are set.
func TestResolveRootEnvPrecedence(t *testing.T) {
	base := t.TempDir()
	newDefault := filepath.Join(base, "new")
	legacyDefault := filepath.Join(base, "legacy")
	if err := os.MkdirAll(legacyDefault, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyEnv := filepath.Join(base, "from-legacy-env")
	newEnv := filepath.Join(base, "from-new-env")

	t.Setenv("TEST_ROOT_NEW", "")
	t.Setenv("TEST_ROOT_LEGACY", legacyEnv)
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != legacyEnv {
		t.Fatalf("legacy env fallback: got %q want %q", got, legacyEnv)
	}

	t.Setenv("TEST_ROOT_NEW", newEnv)
	if got := resolveRoot(newDefault, legacyDefault, "TEST_ROOT_NEW", "TEST_ROOT_LEGACY"); got != newEnv {
		t.Fatalf("current env precedence: got %q want %q", got, newEnv)
	}
}

// TestApplyRootOverridesAdoptsLegacyAndKeepsData proves adoption is
// non-destructive: the legacy roots keep their contents and become effective.
func TestApplyRootOverridesAdoptsLegacyAndKeepsData(t *testing.T) {
	clearRootEnv(t)
	base := t.TempDir()

	legacy := &PlatformPaths{
		DataRoot:    filepath.Join(base, "LitePSM"),
		ConfigRoot:  filepath.Join(base, "LitePSM-config"),
		RuntimeRoot: filepath.Join(base, "LitePSM-run"),
	}
	paths := &PlatformPaths{
		DataRoot:    filepath.Join(base, "LiteSPM"),
		ConfigRoot:  filepath.Join(base, "LiteSPM-config"),
		RuntimeRoot: filepath.Join(base, "LiteSPM-run"),
	}
	for _, d := range []string{legacy.DataRoot, legacy.ConfigRoot, legacy.RuntimeRoot} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stateFile := filepath.Join(legacy.DataRoot, "state.db")
	if err := os.WriteFile(stateFile, []byte("legacy-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	applyRootOverrides(paths, legacy)

	if paths.DataRoot != legacy.DataRoot || paths.ConfigRoot != legacy.ConfigRoot || paths.RuntimeRoot != legacy.RuntimeRoot {
		t.Fatalf("legacy roots were not adopted: %+v", paths)
	}
	if runtime.GOOS != "windows" {
		want := filepath.Join(legacy.RuntimeRoot, currentBrand.socketFile)
		if paths.SocketPath != want {
			t.Fatalf("socket path: got %q want %q", paths.SocketPath, want)
		}
	}
	data, err := os.ReadFile(stateFile)
	if err != nil || string(data) != "legacy-state" {
		t.Fatalf("legacy state was disturbed: err=%v data=%q", err, data)
	}
}

// TestApplyRootOverridesEnvBeatsAdoption proves an explicit override is never
// overridden by legacy adoption, while an unset root still adopts.
func TestApplyRootOverridesEnvBeatsAdoption(t *testing.T) {
	clearRootEnv(t)
	base := t.TempDir()

	legacy := &PlatformPaths{
		DataRoot:   filepath.Join(base, "legacy-data"),
		ConfigRoot: filepath.Join(base, "legacy-config"),
	}
	for _, d := range []string{legacy.DataRoot, legacy.ConfigRoot} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	envData := filepath.Join(base, "env-data")
	t.Setenv("LITESPM_DATA_ROOT", envData)
	t.Setenv("LITEPSM_DATA_ROOT", filepath.Join(base, "legacy-env-data"))

	paths := &PlatformPaths{
		DataRoot:    filepath.Join(base, "new-data"),
		ConfigRoot:  filepath.Join(base, "new-config"),
		RuntimeRoot: filepath.Join(base, "new-run"),
	}
	applyRootOverrides(paths, legacy)

	if paths.DataRoot != envData {
		t.Fatalf("explicit override must beat adoption: got %q want %q", paths.DataRoot, envData)
	}
	if paths.ConfigRoot != legacy.ConfigRoot {
		t.Fatalf("unset config root should adopt legacy: got %q want %q", paths.ConfigRoot, legacy.ConfigRoot)
	}
}

// TestEnvOverrideLegacyFallbackAndPrecedence covers all five configuration
// overrides: the legacy LITEPSM_* name applies when the current one is unset,
// and the current LITESPM_* name wins when both are set.
func TestEnvOverrideLegacyFallbackAndPrecedence(t *testing.T) {
	for _, v := range []string{
		"LITESPM_REGISTRY_URL", "LITEPSM_REGISTRY_URL",
		"LITESPM_LOG_LEVEL", "LITEPSM_LOG_LEVEL",
		"LITESPM_POLICY_DEFAULT_LEVEL", "LITEPSM_POLICY_DEFAULT_LEVEL",
		"LITESPM_POLICY_ENFORCE_SIGNATURES", "LITEPSM_POLICY_ENFORCE_SIGNATURES",
		"LITESPM_NETWORK_TIMEOUT_SEC", "LITEPSM_NETWORK_TIMEOUT_SEC",
	} {
		t.Setenv(v, "")
	}

	// Legacy-only settings must still take effect after the rename.
	t.Setenv("LITEPSM_REGISTRY_URL", "https://legacy.example")
	t.Setenv("LITEPSM_LOG_LEVEL", "warn")
	t.Setenv("LITEPSM_POLICY_DEFAULT_LEVEL", "always_ask")
	t.Setenv("LITEPSM_POLICY_ENFORCE_SIGNATURES", "false")
	t.Setenv("LITEPSM_NETWORK_TIMEOUT_SEC", "12")

	cfg := DefaultConfig(nil)
	applyEnvOverrides(cfg)
	if cfg.Catalog.RegistryURL != "https://legacy.example" {
		t.Errorf("registry url fallback: got %q", cfg.Catalog.RegistryURL)
	}
	if cfg.Daemon.LogLevel != "warn" {
		t.Errorf("log level fallback: got %q", cfg.Daemon.LogLevel)
	}
	if cfg.Policy.DefaultLevel != "always_ask" {
		t.Errorf("policy level fallback: got %q", cfg.Policy.DefaultLevel)
	}
	if cfg.Policy.EnforceSignatures {
		t.Error("enforce-signatures fallback was ignored")
	}
	if cfg.Network.Timeout != 12*time.Second {
		t.Errorf("network timeout fallback: got %v", cfg.Network.Timeout)
	}

	// The current name must win when both are present.
	t.Setenv("LITESPM_REGISTRY_URL", "https://current.example")
	t.Setenv("LITESPM_LOG_LEVEL", "debug")
	t.Setenv("LITESPM_POLICY_DEFAULT_LEVEL", "trust_curated")
	t.Setenv("LITESPM_POLICY_ENFORCE_SIGNATURES", "true")
	t.Setenv("LITESPM_NETWORK_TIMEOUT_SEC", "45")

	cfg = DefaultConfig(nil)
	applyEnvOverrides(cfg)
	if cfg.Catalog.RegistryURL != "https://current.example" {
		t.Errorf("registry url precedence: got %q", cfg.Catalog.RegistryURL)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("log level precedence: got %q", cfg.Daemon.LogLevel)
	}
	if cfg.Policy.DefaultLevel != "trust_curated" {
		t.Errorf("policy level precedence: got %q", cfg.Policy.DefaultLevel)
	}
	if !cfg.Policy.EnforceSignatures {
		t.Error("enforce-signatures precedence was ignored")
	}
	if cfg.Network.Timeout != 45*time.Second {
		t.Errorf("network timeout precedence: got %v", cfg.Network.Timeout)
	}
}

func writeProjectTOML(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoadConfigFallsBackToLegacyProjectConfig proves a project that predates
// the rename is still read, and that the current project directory takes over
// once it exists.
func TestLoadConfigFallsBackToLegacyProjectConfig(t *testing.T) {
	clearRootEnv(t)
	t.Setenv("LITESPM_DATA_ROOT", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LITESPM_CONFIG_ROOT", filepath.Join(t.TempDir(), "config"))
	t.Setenv("LITESPM_RUNTIME_ROOT", filepath.Join(t.TempDir(), "run"))

	project := t.TempDir()
	writeProjectTOML(t, filepath.Join(project, ".litepsm", "config.toml"),
		"[catalog]\nregistry_url = \"https://legacy-project.example\"\n")

	cfg, err := LoadConfig(project)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Catalog.RegistryURL != "https://legacy-project.example" {
		t.Fatalf("legacy project config was not read: got %q", cfg.Catalog.RegistryURL)
	}

	writeProjectTOML(t, filepath.Join(project, ".litespm", "config.toml"),
		"[catalog]\nregistry_url = \"https://current-project.example\"\n")

	cfg, err = LoadConfig(project)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Catalog.RegistryURL != "https://current-project.example" {
		t.Fatalf("current project config should win: got %q", cfg.Catalog.RegistryURL)
	}
}
