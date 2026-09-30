package resolver

import (
	"context"
	"strings"
	"testing"

	"github.com/sarv-projects/litepsm/internal/domain"
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
