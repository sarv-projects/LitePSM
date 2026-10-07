package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func mcpListing(id, name string) *domain.Listing {
	return &domain.Listing{
		ID:     id,
		Name:   name,
		Kind:   domain.KindMCP,
		Status: domain.ListingStatusActive,
		// Proven by a manifest in this fixture; discovery_only is refused.
		Installability: domain.InstallabilityMetadataVerified,
		Summary:        "an MCP server",
		Source:         domain.SourceReference{SourceID: "example", UpstreamID: name},
		Versions:       []domain.VersionSummary{{Version: "1.0.0"}},
	}
}

func stdioRuntime() *domain.RuntimeDescriptor {
	return &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", "demo-mcp"}}
}

// homeWithBridge writes a host config that already carries the LiteSPM bridge,
// which is the consent signal for a default-target install.
func homeWithBridge(t *testing.T) (home string, configPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	configPath = filepath.Join(home, ".claude.json")
	content := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, configPath
}

func TestInstallMCPFromListingRegistersWithConfiguredHosts(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)

	outcome, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(outcome.Hosts) != 1 || outcome.Hosts[0].HostID != "claude-code" {
		t.Fatalf("expected the one configured host, got %+v", outcome.Hosts)
	}
	if outcome.Entry.Name != "demo-mcp" || outcome.Entry.Command != "npx" {
		t.Errorf("unexpected entry: %+v", outcome.Entry)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"demo-mcp": {`) || !strings.Contains(got, `"-y"`) {
		t.Errorf("entry not written:\n%s", got)
	}
	if !strings.Contains(got, `"mine"`) || !strings.Contains(got, `"litespm"`) {
		t.Errorf("existing entries were disturbed:\n%s", got)
	}

	// The install and the host registration must both be recorded, or the entry
	// in the config is an orphan nothing can ever find again.
	rec, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install record: %v", err)
	}
	if rec.ListingID != "mcp:example:demo-mcp" || rec.Status != domain.InstallActive {
		t.Errorf("unexpected install record: %+v", rec)
	}
}

func TestInstallMCPFromListingRefusesCollisions(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "mine"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("install overwrote an entry the user already had")
	}
	if code := domain.ErrorCode(err); code != "LPSM-NAME-CONFLICT" {
		t.Errorf("expected LPSM-NAME-CONFLICT, got %q (%v)", code, err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(original) {
		t.Errorf("a refused install modified the config:\n%s", after)
	}
}

func TestInstallMCPFromListingRefusesWithoutAnyConfiguredHost(t *testing.T) {
	db := openTestState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	_, err := installMCPFromListing(context.Background(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("install proceeded with no host configured")
	}
	if code := domain.ErrorCode(err); code != "LPSM-INSTALL-TARGET-UNAVAILABLE" {
		t.Errorf("expected LPSM-INSTALL-TARGET-UNAVAILABLE, got %q (%v)", code, err)
	}
}

func TestInstallMCPFromListingRefusesWhenNoCommandIsPublished(t *testing.T) {
	db := openTestState(t)
	homeWithBridge(t)

	_, err := installMCPFromListing(context.Background(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, nil, nil)
	if err == nil {
		t.Fatal("install proceeded with no runtime descriptor")
	}
	if code := domain.ErrorCode(err); code != "LPSM-ARTIFACT-UNAVAILABLE" {
		t.Errorf("expected LPSM-ARTIFACT-UNAVAILABLE, got %q (%v)", code, err)
	}
}

func TestServerEntryNameForNormalizesHostUnsafeNames(t *testing.T) {
	cases := map[string]string{
		"brave-search-mcp-server": "brave-search-mcp-server",
		"Brave Search":            "brave-search",
		"context7":                "context7",
		"My_Server.2":             "my_server-2",
		"--weird--":               "weird",
	}
	for in, want := range cases {
		got, err := serverEntryNameFor(mcpListing("mcp:x:y", in))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// A name that would collide with the bridge must be refused, not installed.
	if _, err := serverEntryNameFor(mcpListing("mcp:x:y", "litespm")); err == nil {
		t.Error("a listing named litespm was accepted")
	}
}

// TestInstallMCPFromListingRefusesDiscoveryOnly pins Phase 0.1/T3: heuristic
// rows (and unset installability) are searchable metadata, never installable.
func TestInstallMCPFromListingRefusesDiscoveryOnly(t *testing.T) {
	for _, class := range []domain.Installability{"", domain.InstallabilityDiscoveryOnly} {
		listing := mcpListing("mcp:example:demo", "demo")
		listing.Installability = class
		_, err := installMCPFromListing(context.Background(), nil, t.TempDir(), listing, "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
		if domain.ErrorCode(err) != "LPSM-NOT-INSTALLABLE" {
			t.Fatalf("class %q: err = %v, want LPSM-NOT-INSTALLABLE", class, err)
		}
	}
}
