package main

import (
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestParseCapabilityFlags(t *testing.T) {
	hosts, limit, capID, err := parseCapabilityFlags([]string{"--host", "codex", "--host", "cursor", "--limit", "5", "--capability", "x/y/z"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "codex" || hosts[1] != "cursor" {
		t.Errorf("hosts = %v", hosts)
	}
	if limit != 5 {
		t.Errorf("limit = %d", limit)
	}
	if capID != "x/y/z" {
		t.Errorf("capability = %q", capID)
	}

	// A bare word is the search query and must not be mistaken for a flag value.
	hosts, _, _, err = parseCapabilityFlags([]string{"echo"})
	if err != nil || len(hosts) != 0 {
		t.Errorf("a bare query must be left for the caller (hosts=%v err=%v)", hosts, err)
	}

	for _, bad := range [][]string{
		{"--host"}, {"--limit"}, {"--capability"}, {"--limit", "0"}, {"--limit", "abc"},
	} {
		if _, _, _, err := parseCapabilityFlags(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

// TestServerEntryNameForMatchesTheInstalledName is the contract that makes
// `capabilities refresh` able to find what `install` wrote: the refresh path
// recomputes the entry name from the listing and matches on it, so the two must
// agree exactly.
func TestServerEntryNameForMatchesTheInstalledName(t *testing.T) {
	listing := mcpListing("mcp:upstash:context7", "Context7")
	name, err := serverEntryNameFor(listing)
	if err != nil {
		t.Fatal(err)
	}
	if name != "context7" {
		t.Fatalf("entry name %q, want context7", name)
	}
	// A name that needs normalizing must still be host-legal, because the entry
	// is written into a config whose schema restricts these keys.
	odd, err := serverEntryNameFor(mcpListing("mcp:x:y", "Weird Name (v2)!"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHostEntryName(odd); err != nil {
		t.Errorf("normalized name %q is not host-legal: %v", odd, err)
	}
	if strings.Contains(odd, " ") {
		t.Errorf("normalized name still has a space: %q", odd)
	}
}

// validateHostEntryName mirrors the restriction hosts document, without pulling
// the host package into this test's import graph.
func validateHostEntryName(name string) error {
	if name == "" {
		return errString("empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return errString("illegal character")
		}
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestCapabilitiesUsageMentionsEveryCommand(t *testing.T) {
	for _, want := range []string{"list", "search", "describe", "refresh", "--capability", "--host"} {
		if !strings.Contains(capabilitiesUsage, want) {
			t.Errorf("usage does not document %q", want)
		}
	}
}

// TestMCPInstallWritesTheComponentRowFksRequire pins why the install writes an
// install_components row: providers.component_id is a foreign key onto it
// (ARCH/12 §13), so a discovery pass after an install fails with a constraint
// error if the row is missing.
func TestMCPInstallWritesTheComponentRowFksRequire(t *testing.T) {
	db := openTestState(t)
	homeWithBridge(t)

	outcome, err := installMCPFromListing(t.Context(), db, t.TempDir(),
		mcpListing("mcp:example:needs-component", "needs-component"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	row, err := db.GetInstallComponent(t.Context(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install component row missing: %v", err)
	}
	if row.ComponentName != "needs-component" || row.Kind != domain.ComponentMCPProvider {
		t.Errorf("unexpected component row: %+v", row)
	}
}
