package main

import (
	"os"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
)

func TestParseInstallFlagsEnv(t *testing.T) {
	f, err := parseInstallFlags([]string{"mcp:x:y", "--env", "A", "--env", "B", "--force"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.envNames) != 2 || f.envNames[0] != "A" || f.envNames[1] != "B" {
		t.Errorf("envNames = %v, want [A B]", f.envNames)
	}
	// --env takes a NAME. A value must not be accepted silently, because
	// `--env TOKEN=secret` would put a credential on the command line and in
	// shell history while the config only ever stores a reference.
	if _, err := parseInstallFlags([]string{"mcp:x:y", "--env"}); err == nil {
		t.Error("--env with no value was accepted")
	}
}

// A name that could never resolve is a typo the user must hear about at the
// command line, not a reference sitting in their config forever.
func TestInstallRejectsUnusableEnvNamesBeforeWriting(t *testing.T) {
	db := openTestState(t)
	_, configPath := homeWithBridge(t)
	before, _ := os.ReadFile(configPath)

	for _, bad := range []string{"has space", "2LEADING", "has-dash", "has.dot"} {
		_, err := installMCPFromListing(t.Context(), db, t.TempDir(),
			mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
			nil, false, stdioRuntime(), []string{bad})
		if err == nil {
			t.Errorf("--env %q was accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("--env %q rejected with an error that does not name it: %v", bad, err)
		}
	}
	if _, err := installMCPFromListing(t.Context(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		nil, false, stdioRuntime(), []string{"A", "A"}); err == nil {
		t.Error("a duplicated variable name was accepted; it would collapse to one entry")
	}

	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Error("a rejected --env still modified the host config")
	}
}

// A host with no verified substitution must be refused outright. Writing an entry
// that looks credentialed but cannot be is worse than not writing it.
func TestInstallRefusesEnvForUnverifiedHost(t *testing.T) {
	db := openTestState(t)
	_, configPath := homeWithBridge(t)
	before, _ := os.ReadFile(configPath)

	_, err := installMCPFromListing(t.Context(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"windsurf"}, false, stdioRuntime(), []string{"GITHUB_TOKEN"})
	if err == nil {
		t.Fatal("--env accepted for a host with no documented behaviour")
	}
	if !strings.Contains(err.Error(), "windsurf") {
		t.Errorf("refusal does not name the host: %v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Error("the refused host's config was still modified")
	}
}

// A host whose entry shape is a bare command string has nowhere to put an
// environment at all. Accepting --env there would register an entry that looks
// credentialed and carries no credential, which is the worst possible outcome.
func TestInstallRefusesEnvForShapeThatCannotCarryIt(t *testing.T) {
	db := openTestState(t)
	homeWithBridge(t)

	_, err := installMCPFromListing(t.Context(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"mux"}, false, stdioRuntime(), []string{"GITHUB_TOKEN"})
	if err == nil {
		t.Fatal("--env accepted for a host whose entry shape cannot carry one")
	}
	if !strings.Contains(err.Error(), "mux") {
		t.Errorf("refusal does not name the host: %v", err)
	}
}

// Every host that writes an entry must be classified one way or the other: it can
// carry a variable, or it cannot. A host falling through both would mean --env
// either silently vanishes or is refused without cause.
func TestEnvForwardingClassifiesEveryRegisteredHost(t *testing.T) {
	adapters := host.ListAdapters()
	if len(adapters) == 0 {
		t.Skip("no registered hosts")
	}
	ids := make([]string, 0, len(adapters))
	for _, a := range adapters {
		ids = append(ids, a.Descriptor().HostID)
	}
	var supported []string
	for _, id := range ids {
		if _, ok := host.EnvForwarding(id); ok {
			supported = append(supported, id)
		}
	}
	// The verified set, plus the one host that legitimately cannot.
	want := map[string]bool{
		"claude-code": true, "codex": true, "cline": true, "pi-agent": true,
		"cursor": true, "opencode": true, "github-copilot": true,
		"kiro-cli": true, "gemini-cli": true, "grok-build": true,
	}
	for id := range want {
		if _, ok := host.EnvForwarding(id); !ok {
			t.Errorf("host %q is documented as supporting references but EnvForwarding refuses it", id)
		}
	}
	if _, ok := host.EnvForwarding("mux"); ok {
		t.Error("mux stores a bare command string but claims environment support")
	}
	t.Logf("%d/%d hosts support forwarded variables: %v", len(supported), len(ids), supported)
}
