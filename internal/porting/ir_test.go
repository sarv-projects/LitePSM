package porting

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
)

// TestNormalizeMCPPDropsLiteralEnvValues is the invariant-3 unit proof: a
// literal found in a source config never reaches the IR — not as Env, not as
// an EnvNames entry — while its NAME is reported so the target can be told
// to provide it.
func TestNormalizeMCPPDropsLiteralEnvValues(t *testing.T) {
	entry, needs := NormalizeMCP(SourceMCP{
		Name:    "github",
		Command: "  gh  ",
		Args:    []string{"mcp"},
		Env:     map[string]string{"GITHUB_TOKEN": "ghp_literal_secret_value"},
	})

	if entry.Env != nil {
		t.Fatalf("IR carried literal env content: %#v", entry.Env)
	}
	if len(entry.EnvNames) != 0 {
		t.Errorf("EnvNames = %v, want empty: a literal is not a reference", entry.EnvNames)
	}
	if !reflect.DeepEqual(needs, []string{"GITHUB_TOKEN"}) {
		t.Errorf("Needs = %v, want [GITHUB_TOKEN]", needs)
	}
	if entry.Command != "gh" {
		t.Errorf("Command = %q, want trimmed %q", entry.Command, "gh")
	}
	// The value must not be recoverable from anything the IR renders.
	rendered := Fingerprint(entry) + strings.Join(entry.Args, " ") + strings.Join(entry.EnvNames, " ")
	if strings.Contains(rendered, "ghp_literal_secret_value") {
		t.Errorf("literal value leaked into IR output: %s", rendered)
	}
}

func TestNormalizeMCPKeepsReferenceNames(t *testing.T) {
	entry, needs := NormalizeMCP(SourceMCP{
		Name:     "demo",
		Command:  "npx",
		Env:      map[string]string{"A": "${A}"},
		EnvNames: []string{"B"},
	})
	if !reflect.DeepEqual(entry.EnvNames, []string{"A", "B"}) {
		t.Errorf("EnvNames = %v, want [A B]", entry.EnvNames)
	}
	if !reflect.DeepEqual(needs, []string{"A", "B"}) {
		t.Errorf("Needs = %v, want [A B]", needs)
	}
	if entry.Env != nil {
		t.Errorf("IR must never carry Env, got %#v", entry.Env)
	}
}

// TestFingerprintCanonical pins the identity function used by conflict
// classification and by the L1 read-back: order-insensitive over args and
// env names, sensitive to their content, and stable across nil/empty.
func TestFingerprintCanonical(t *testing.T) {
	base := host.ServerEntry{Name: "x", Command: "cmd", Args: []string{"a", "b"}, EnvNames: []string{"N1", "N2"}}

	reordered := host.ServerEntry{Name: "x", Command: "cmd", Args: []string{"b", "a"}, EnvNames: []string{"N2", "N1"}}
	if Fingerprint(base) == Fingerprint(reordered) {
		t.Error("argument order must be part of the fingerprint")
	}

	sameContent := host.ServerEntry{Name: "x", Command: "cmd", Args: []string{"a", "b"}, EnvNames: []string{"N2", "N1"}}
	if Fingerprint(base) != Fingerprint(sameContent) {
		t.Error("env-name order must NOT be part of the fingerprint")
	}

	nilArgs := host.ServerEntry{Name: "x", Command: "cmd", EnvNames: []string{"N1"}}
	emptyArgs := host.ServerEntry{Name: "x", Command: "cmd", Args: []string{}, EnvNames: []string{"N1"}}
	if Fingerprint(nilArgs) != Fingerprint(emptyArgs) {
		t.Error("nil and empty slices must fingerprint identically")
	}

	otherEnv := host.ServerEntry{Name: "x", Command: "cmd", Args: []string{"a", "b"}, EnvNames: []string{"N1"}}
	if Fingerprint(base) == Fingerprint(otherEnv) {
		t.Error("the env-name set must be part of the fingerprint")
	}
}

// TestIRFromHostEntryClassifiesBothSidesTheSame way: an entry read back from
// a target config fingerprints equal to the source IR when their portable
// facts match — including the case where both sides hold the same literal
// under the same name (neither carries it, so neither can claim a difference
// the copy would not actually bridge).
func TestIRFromHostEntryClassifiesBothSidesTheSameWay(t *testing.T) {
	srcEntry, _ := NormalizeMCP(SourceMCP{
		Name: "github", Command: "gh", Env: map[string]string{"GITHUB_TOKEN": "ghp_one"},
	})
	readBack := IRFromHostEntry(host.HostServerEntry{
		Name: "github", Command: "gh", Env: map[string]string{"GITHUB_TOKEN": "ghp_two"},
	})
	if Fingerprint(srcEntry) != Fingerprint(readBack) {
		t.Errorf("literal env on either side must not change the portable fingerprint: %s vs %s",
			Fingerprint(srcEntry), Fingerprint(readBack))
	}

	withRef, _ := NormalizeMCP(SourceMCP{
		Name: "github", Command: "gh", Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
	})
	readBackRef := IRFromHostEntry(host.HostServerEntry{
		Name: "github", Command: "gh", Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
	})
	if Fingerprint(withRef) != Fingerprint(readBackRef) {
		t.Error("a reference must classify identically on both sides")
	}
	if Fingerprint(srcEntry) == Fingerprint(withRef) {
		t.Error("a forwarded reference must differ from no forwarding at all")
	}
}

// TestAdoptedListingIDMatchesCatalogGrammar: local-origin ids must parse as
// ordinary listing ids, or the install row could not be read back by the
// tools/list and kind-parsing paths.
func TestAdoptedListingIDMatchesCatalogGrammar(t *testing.T) {
	cases := []struct {
		kind, source, name string
	}{
		{KindMCP, "opencode", "engram"},
		{KindMCP, "OpenCode", "engram"}, // host ids are canonicalized
		{KindSkill, "codex", "demo-skill"},
	}
	for _, tc := range cases {
		id := AdoptedListingID(tc.kind, tc.source, tc.name)
		parsed, err := domain.ParseListingID(id)
		if err != nil {
			t.Errorf("AdoptedListingID(%q,%q,%q) = %q does not parse: %v", tc.kind, tc.source, tc.name, id, err)
			continue
		}
		if parsed.Kind() != domain.ListingKind(tc.kind) {
			t.Errorf("%q parses as kind %q, want %q", id, parsed.Kind(), tc.kind)
		}
	}
	if got := AdoptedListingID(KindMCP, "OpenCode", "engram"); got != "mcp:adopted:opencode:engram" {
		t.Errorf("AdoptedListingID = %q, want mcp:adopted:opencode:engram", got)
	}
}

func TestSupportedKind(t *testing.T) {
	for kind, want := range map[string]bool{
		KindMCP: true, KindSkill: true, KindPlugin: false, "connector": false, "widget": false,
	} {
		if got := SupportedKind(kind); got != want {
			t.Errorf("SupportedKind(%q) = %v, want %v", kind, got, want)
		}
	}
}
