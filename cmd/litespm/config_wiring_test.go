package main

// Wiring evidence for the D3 register item: configuration (env, user TOML,
// project TOML) actually reaches the catalog client's timeout and the policy
// engine's defaults — the paths that used to stop at config.Config and go no
// further.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/policy"
)

func TestCatalogTimeoutOfMapsConfig(t *testing.T) {
	if got := catalogTimeoutOf(nil); got != 0 {
		t.Errorf("nil config must select the client default, got %v", got)
	}
	cfg := &config.Config{}
	if got := catalogTimeoutOf(cfg); got != 0 {
		t.Errorf("zero timeout must select the client default, got %v", got)
	}
	cfg.Network.Timeout = 12 * time.Second
	if got := catalogTimeoutOf(cfg); got != 12*time.Second {
		t.Errorf("catalogTimeoutOf = %v, want 12s", got)
	}
}

func TestRegistryURLOfMapsConfig(t *testing.T) {
	if got := registryURLOf(nil); got != config.DefaultRegistryURL {
		t.Errorf("nil config must fall back to the default origin, got %q", got)
	}
	cfg := &config.Config{}
	cfg.Catalog.RegistryURL = "https://registry.example"
	if got := registryURLOf(cfg); got != "https://registry.example" {
		t.Errorf("registryURLOf = %q", got)
	}
}

// A configuration read failure must not weaken the policy gate: the zero
// PolicyConfig would carry EnforceSignatures=false, which is why nil config
// maps to the secure compiled defaults instead.
func TestPolicyDefaultsFromFailsClosedWithoutConfig(t *testing.T) {
	secure := policyDefaultsFrom(nil)
	if secure.DefaultLevel != policy.LevelAskOnce || !secure.EnforceSignatures {
		t.Fatalf("nil config must yield secure defaults, got %+v", secure)
	}

	cfg := &config.Config{Policy: config.PolicyConfig{
		DefaultLevel:      policy.LevelTrustCurated,
		EnforceSignatures: true,
	}}
	got := policyDefaultsFrom(cfg)
	if got.DefaultLevel != policy.LevelTrustCurated || !got.EnforceSignatures {
		t.Fatalf("policyDefaultsFrom = %+v, want the configured values", got)
	}
}

// End to end: a project's .litespm/config.toml read through the same helper
// the CLI uses ends up governing an engine's decisions.
func TestProjectConfigReachesPolicyEngine(t *testing.T) {
	for _, v := range []string{"LITESPM_POLICY_DEFAULT_LEVEL", "LITEPSM_POLICY_DEFAULT_LEVEL"} {
		t.Setenv(v, "")
	}
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".litespm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".litespm", "config.toml"),
		[]byte("[policy]\ndefault_level = \"trust_curated\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	cfg, err := config.LoadCurrentConfig()
	if err != nil {
		t.Fatalf("LoadCurrentConfig: %v", err)
	}
	eng := policy.NewEngineWithDefaults(nil, nil, policyDefaultsFrom(cfg))
	dec := eng.Evaluate(context.Background(), policy.PolicyInput{
		Actor: "user", TargetRef: "pkg:from-project", Operation: "install",
		Effects: []policy.EffectDeclaration{{Effect: policy.EffectPackageInstall, Provenance: domain.ProvenanceCurated}},
	})
	if dec.Decision != policy.DecisionAllow {
		t.Fatalf("project policy level did not reach the engine: %+v", dec)
	}

	// With the level back at the default, the same input asks again.
	engDefault := policy.NewEngineWithDefaults(nil, nil, policyDefaultsFrom(&config.Config{
		Policy: config.PolicyConfig{DefaultLevel: policy.LevelAskOnce, EnforceSignatures: true},
	}))
	if dec := engDefault.Evaluate(context.Background(), policy.PolicyInput{
		Actor: "user", TargetRef: "pkg:from-project", Operation: "install",
		Effects: []policy.EffectDeclaration{{Effect: policy.EffectPackageInstall}},
	}); dec.Decision != policy.DecisionAsk {
		t.Fatalf("ask_once baseline changed: %+v", dec)
	}
}
