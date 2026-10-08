package resolver

import (
	"context"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

type mockProvider struct {
	listings map[string]*domain.Listing
	versions map[string][]ListingVersionMetadata
}

func (m *mockProvider) GetListing(ctx context.Context, listingID string) (*domain.Listing, error) {
	if l, ok := m.listings[listingID]; ok {
		return l, nil
	}
	return nil, domain.ErrNotFound("listing", listingID)
}

func (m *mockProvider) GetListingVersions(ctx context.Context, listingID string) ([]ListingVersionMetadata, error) {
	if v, ok := m.versions[listingID]; ok {
		return v, nil
	}
	return nil, domain.ErrNotFound("listing_versions", listingID)
}

func TestSemVerComparison(t *testing.T) {
	tests := []struct {
		v1, v2   string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.3", "v1.2.3", 0},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-beta.2", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"1.0.0-beta.11", "1.0.0-rc.1", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
	}

	for _, tt := range tests {
		ver1, err1 := ParseVersion(tt.v1)
		if err1 != nil {
			t.Fatalf("failed to parse %s: %v", tt.v1, err1)
		}
		ver2, err2 := ParseVersion(tt.v2)
		if err2 != nil {
			t.Fatalf("failed to parse %s: %v", tt.v2, err2)
		}

		res := ver1.Compare(ver2)
		if res != tt.expected {
			t.Errorf("Compare(%s, %s) = %d; want %d", tt.v1, tt.v2, res, tt.expected)
		}
	}
}

func TestParseVersionRejectsMalformedSemVer(t *testing.T) {
	for _, version := range []string{
		"1", "1.2", "1..3", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-alpha..1", "1.2.3-01", "1.2.3+", "1.2.3+build..1",
		"1.2.3+build+again", "1.2.3-α", "v1.2.3junk",
	} {
		t.Run(version, func(t *testing.T) {
			if _, err := ParseVersion(version); err == nil {
				t.Fatalf("ParseVersion(%q) unexpectedly succeeded", version)
			}
		})
	}
}

func TestSemVerComparisonHandlesLargePrereleaseNumbers(t *testing.T) {
	a, err := ParseVersion("1.0.0-alpha.999999999999999999999999")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseVersion("1.0.0-alpha.1000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Compare(b); got >= 0 {
		t.Fatalf("large numeric prerelease comparison = %d, want < 0", got)
	}
}

func TestConstraintMatching(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		matches    bool
	}{
		{"*", "1.2.3", true},
		{"latest", "2.0.0", true},
		{">=1.0.0 <2.0.0", "1.5.0", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{">=1.0.0 <2.0.0", "0.9.0", false},
		{"^1.2.3", "1.2.3", true},
		{"^1.2.3", "1.9.0", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.2", false},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"=1.0.0", "1.0.0", true},
		{"!=1.0.0", "1.0.0", false},
	}

	for _, tt := range tests {
		c, err := ParseConstraint(tt.constraint)
		if err != nil {
			t.Fatalf("failed to parse constraint %q: %v", tt.constraint, err)
		}
		v, err := ParseVersion(tt.version)
		if err != nil {
			t.Fatalf("failed to parse version %q: %v", tt.version, err)
		}
		if got := c.Matches(v); got != tt.matches {
			t.Errorf("Constraint(%q).Matches(%q) = %v; want %v", tt.constraint, tt.version, got, tt.matches)
		}
	}
}

func TestResolver_DiamondDependency(t *testing.T) {
	ctx := context.Background()

	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			"root": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-a", Constraint: "^1.0.0"},
						{ListingID: "pkg-b", Constraint: "^1.0.0"},
					},
				},
			},
			"pkg-a": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-c", Constraint: ">=1.0.0 <2.0.0"},
					},
				},
			},
			"pkg-b": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-c", Constraint: ">=1.5.0 <3.0.0"},
					},
				},
			},
			"pkg-c": {
				{Version: "1.0.0"},
				{Version: "1.2.0"},
				{Version: "1.6.0"},
				{Version: "1.8.0"},
				{Version: "2.1.0"},
			},
		},
	}

	resolver := NewResolver(provider)
	res, err := resolver.Resolve(ctx, "root", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected resolution failure: %v", err)
	}

	// For pkg-c, constraint intersection is (>=1.0.0 <2.0.0) AND (>=1.5.0 <3.0.0) -> (>=1.5.0 <2.0.0)
	// Among 1.0.0, 1.2.0, 1.6.0, 1.8.0, 2.1.0 -> 1.8.0 is the highest matching
	if res.SelectedVersions["pkg-c"] != "1.8.0" {
		t.Errorf("expected pkg-c version 1.8.0, got %s", res.SelectedVersions["pkg-c"])
	}

	// Verify topological order: pkg-c must appear before pkg-a and pkg-b, and both before root
	pos := make(map[string]int)
	for i, id := range res.TopologicalOrder {
		pos[id] = i
	}

	if pos["pkg-c"] >= pos["pkg-a"] || pos["pkg-c"] >= pos["pkg-b"] {
		t.Errorf("pkg-c must be installed before pkg-a and pkg-b. Order: %v", res.TopologicalOrder)
	}
	if pos["pkg-a"] >= pos["root"] || pos["pkg-b"] >= pos["root"] {
		t.Errorf("pkg-a and pkg-b must be installed before root. Order: %v", res.TopologicalOrder)
	}
}

func TestResolver_Conflict(t *testing.T) {
	ctx := context.Background()

	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			"root": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-a", Constraint: "*"},
						{ListingID: "pkg-b", Constraint: "*"},
					},
				},
			},
			"pkg-a": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-c", Constraint: "<1.5.0"},
					},
				},
			},
			"pkg-b": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-c", Constraint: ">=2.0.0"},
					},
				},
			},
			"pkg-c": {
				{Version: "1.2.0"},
				{Version: "2.1.0"},
			},
		},
	}

	resolver := NewResolver(provider)
	_, err := resolver.Resolve(ctx, "root", "1.0.0")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}

	if !strings.Contains(err.Error(), "LPSM-RESOLVE-CONFLICT") && !strings.Contains(err.Error(), "unresolvable") {
		t.Errorf("expected LPSM-RESOLVE-CONFLICT error, got: %v", err)
	}
}

func TestResolver_CycleDetection(t *testing.T) {
	ctx := context.Background()

	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			"pkg-a": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-b", Constraint: "*"},
					},
				},
			},
			"pkg-b": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-c", Constraint: "*"},
					},
				},
			},
			"pkg-c": {
				{
					Version: "1.0.0",
					Dependencies: []domain.DependencyConstraint{
						{ListingID: "pkg-a", Constraint: "*"},
					},
				},
			},
		},
	}

	resolver := NewResolver(provider)
	_, err := resolver.Resolve(ctx, "pkg-a", "1.0.0")
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}

	if !strings.Contains(err.Error(), "LPSM-RESOLVE-CYCLE") && !strings.Contains(err.Error(), "cycle detected") {
		t.Errorf("expected LPSM-RESOLVE-CYCLE error, got: %v", err)
	}
}

// TestImplicitVersionIsThePublishedPin pins the sentinel's contract: it parses
// to itself (never to semver), sits below every published version — including
// 0.0.0, so no fabricated-version equivalence exists — and satisfies exactly
// the wildcard and explicit-pin constraints the version-less path relies on.
func TestImplicitVersionIsThePublishedPin(t *testing.T) {
	implicit, err := ParseVersion(ImplicitVersion)
	if err != nil {
		t.Fatalf("ParseVersion(%q) must accept the release's pin: %v", ImplicitVersion, err)
	}
	if !implicit.Implicit || implicit.String() != ImplicitVersion {
		t.Fatalf("parsed pin = %+v, want Implicit with Raw %q", implicit, ImplicitVersion)
	}

	published, err := ParseVersion("0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if implicit.Compare(published) >= 0 {
		t.Errorf("the implicit pin must compare below 0.0.0, got %d", implicit.Compare(published))
	}
	if published.Compare(implicit) <= 0 {
		t.Errorf("0.0.0 must compare above the implicit pin, got %d", published.Compare(implicit))
	}
	if implicit.Compare(implicit) != 0 {
		t.Errorf("the implicit pin must equal itself, got %d", implicit.Compare(implicit))
	}

	// Wildcards and an exact request for the pin match; a real semver range
	// built from 0.0.0 must not (that equivalence is what A2 rejects).
	for constraint, want := range map[string]bool{
		"":          true,
		"*":         true,
		"latest":    true,
		"discovery": true,
		">=0.0.0":   false,
		"^0.0.0":    false,
		"1.0.0":     false,
	} {
		c, err := ParseConstraint(constraint)
		if err != nil {
			t.Fatalf("ParseConstraint(%q): %v", constraint, err)
		}
		if got := c.Matches(implicit); got != want {
			t.Errorf("Constraint(%q).Matches(%q) = %v, want %v", constraint, ImplicitVersion, got, want)
		}
	}
}

// TestResolveVersionLessListingSelectsImplicitPin is recommendation A2: a
// listing whose Versions is empty (every skill row in rel-2026-10-07-01 and
// 5,811 of its 5,825 listings) resolves under a wildcard constraint to a
// single-node graph whose SelectedVersion is the release's implicit version —
// deterministic, and never a synthesized string like 0.0.0.
func TestResolveVersionLessListingSelectsImplicitPin(t *testing.T) {
	ctx := context.Background()
	const versionless = "skill:aaron-he-zhu:aaron-marketing-skills"

	for _, tc := range []struct {
		constraint string
		wantRaw    string
	}{
		{"", "*"}, // Resolve normalizes the empty constraint to "*"
		{"latest", "latest"},
		{"*", "*"},
	} {
		t.Run("constraint="+tc.constraint, func(t *testing.T) {
			provider := &mockProvider{
				listings: map[string]*domain.Listing{
					versionless: {ID: versionless, Kind: domain.KindSkill, Versions: []domain.VersionSummary{}},
				},
				versions: map[string][]ListingVersionMetadata{
					// Present but empty: the release publishes no versions.
					versionless: {},
				},
			}

			res, err := NewResolver(provider).Resolve(ctx, versionless, tc.constraint)
			if err != nil {
				t.Fatalf("version-less listing must resolve: %v", err)
			}
			if got := res.SelectedVersions[versionless]; got != ImplicitVersion {
				t.Errorf("SelectedVersion = %q, want the implicit pin %q", got, ImplicitVersion)
			}
			if len(res.Nodes) != 1 {
				t.Fatalf("nodes = %d, want a single-node graph", len(res.Nodes))
			}
			node := res.Nodes[versionless]
			if node.SelectedVersion != ImplicitVersion || !node.Direct || node.Depth != 0 {
				t.Errorf("node = %+v, want direct depth-0 node pinned to %q", node, ImplicitVersion)
			}
			if len(res.TopologicalOrder) != 1 || res.TopologicalOrder[0] != versionless {
				t.Errorf("topological order = %v, want [%s]", res.TopologicalOrder, versionless)
			}
			if got := res.ResolvedRanges[versionless]; got != tc.wantRaw {
				t.Errorf("resolved range = %q, want %q", got, tc.wantRaw)
			}
		})
	}
}

// TestResolveVersionLessListingRefusesRealSemverConstraint keeps the refusal
// honest in the other direction: "no versions" must not become "matches
// anything". A real range against a version-less listing still fails closed.
func TestResolveVersionLessListingRefusesRealSemverConstraint(t *testing.T) {
	ctx := context.Background()
	const versionless = "skill:aaron-he-zhu:aaron-marketing-skills"
	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			versionless: {},
		},
	}

	for _, constraint := range []string{">=1.0.0", "1.0.0", "^0.0.0", "0.0.0"} {
		t.Run(constraint, func(t *testing.T) {
			_, err := NewResolver(provider).Resolve(ctx, versionless, constraint)
			if err == nil {
				t.Fatal("a real semver range must not resolve against a version-less listing")
			}
			if !strings.Contains(err.Error(), "LPSM-RESOLVE-CONFLICT") {
				t.Errorf("want LPSM-RESOLVE-CONFLICT, got: %v", err)
			}
			if !strings.Contains(err.Error(), "publishes no versions") {
				t.Errorf("refusal must name the real cause, got: %v", err)
			}
		})
	}
}

// TestResolveVersionLessListingAcceptsExplicitPin covers the --frozen round
// trip: a lock records the implicit pin for a version-less entry, and the
// frozen install re-resolves that exact string as a request.
func TestResolveVersionLessListingAcceptsExplicitPin(t *testing.T) {
	ctx := context.Background()
	const versionless = "skill:aaron-he-zhu:aaron-marketing-skills"
	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			versionless: {},
		},
	}

	res, err := NewResolver(provider).Resolve(ctx, versionless, ImplicitVersion)
	if err != nil {
		t.Fatalf("an explicit request for the implicit pin must resolve: %v", err)
	}
	if res.SelectedVersions[versionless] != ImplicitVersion {
		t.Errorf("SelectedVersion = %q, want %q", res.SelectedVersions[versionless], ImplicitVersion)
	}
}

// TestResolveMultiVersionListingUnchanged is the regression guard for
// recommendation A2: when a listing does publish versions, resolution behaves
// exactly as before — highest matching semver wins, and the implicit pin is
// never selected for a listing that has real versions (asking for the pin of a
// versioned listing is a conflict, not an alias for "latest").
func TestResolveMultiVersionListingUnchanged(t *testing.T) {
	ctx := context.Background()
	const versioned = "mcp:example:multi"

	provider := &mockProvider{
		versions: map[string][]ListingVersionMetadata{
			versioned: {
				{Version: "1.0.0"},
				{Version: "1.9.0"},
				{Version: "1.10.0"},
				{Version: "2.0.0"},
			},
		},
	}

	for _, tc := range []struct{ constraint, want string }{
		{"", "2.0.0"},
		{"latest", "2.0.0"},
		{"*", "2.0.0"},
		{">=1.0.0 <2.0.0", "1.10.0"}, // semver order, not lexical (1.9.0 < 1.10.0)
	} {
		t.Run(tc.constraint, func(t *testing.T) {
			res, err := NewResolver(provider).Resolve(ctx, versioned, tc.constraint)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got := res.SelectedVersions[versioned]; got != tc.want {
				t.Errorf("SelectedVersion = %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := NewResolver(provider).Resolve(ctx, versioned, ImplicitVersion); err == nil {
		t.Error("the implicit pin must not alias a versioned listing's versions")
	} else if !strings.Contains(err.Error(), "LPSM-RESOLVE-CONFLICT") {
		t.Errorf("want LPSM-RESOLVE-CONFLICT, got: %v", err)
	}
}
