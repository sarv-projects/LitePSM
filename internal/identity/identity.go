// Package identity implements the canonical identity + alias graph (ARCH/32
// §6): one canonical package with many source edges. Aliases collapse at
// presentation time only; no adapter may merge two packages into one row.
//
// Edge discipline (binding):
//   - hash_match requires an actual digest equality, passed as evidence.
//   - verified_same_project requires a source assertion of shared ownership.
//   - publisher_claim / fork / mirror are never rendered as verified.
package identity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// EdgeType classifies how an alias relates to its canonical package.
type EdgeType string

const (
	EdgeVerifiedSameProject EdgeType = "verified_same_project"
	EdgePublisherClaim      EdgeType = "publisher_claim"
	EdgeHashMatch           EdgeType = "hash_match"
	EdgeFork                EdgeType = "fork"
	EdgeMirror              EdgeType = "mirror"
)

// Edge is one alias link.
type Edge struct {
	SourceID   string   `json:"sourceId"`
	UpstreamID string   `json:"upstreamId"`
	URL        string   `json:"url,omitempty"`
	EdgeType   EdgeType `json:"edgeType"`
	Evidence   string   `json:"evidence"`
}

// Node is one canonical package and its alias edges.
type Node struct {
	CanonicalID string `json:"canonicalId"`
	Aliases     []Edge `json:"aliases"`
}

// Graph is the alias graph.
type Graph struct {
	nodes map[string]*Node
	// aliasToCanonical maps "sourceID\x00upstreamID" (lower-cased) to canonical.
	aliasToCanonical map[string]string
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{nodes: map[string]*Node{}, aliasToCanonical: map[string]string{}}
}

func aliasKey(sourceID, upstreamID string) string {
	return strings.ToLower(sourceID) + "\x00" + strings.ToLower(upstreamID)
}

// AddCanonical registers a canonical package id (Listing ID grammar).
func (g *Graph) AddCanonical(id string) error {
	if _, err := domain.ParseListingID(id); err != nil {
		return fmt.Errorf("LPSM-IDENTITY-BAD-ID: %w", err)
	}
	if _, ok := g.nodes[id]; !ok {
		g.nodes[id] = &Node{CanonicalID: id}
	}
	return nil
}

// AddEdge links an alias to a canonical package. Evidence is mandatory; for
// hash_match it must name the equal digests, for verified_same_project it
// must quote the source assertion. fork/mirror are recorded as divergent
// lineage, never as verification.
func (g *Graph) AddEdge(canonicalID string, e Edge) error {
	node, ok := g.nodes[canonicalID]
	if !ok {
		return fmt.Errorf("LPSM-IDENTITY-UNKNOWN-CANONICAL: %q", canonicalID)
	}
	if _, err := domain.ParseSourceID(e.SourceID); err != nil {
		return fmt.Errorf("LPSM-IDENTITY-BAD-SOURCE: %w", err)
	}
	if strings.TrimSpace(e.UpstreamID) == "" {
		return fmt.Errorf("LPSM-IDENTITY-BAD-ALIAS: upstream id is required")
	}
	if strings.TrimSpace(e.Evidence) == "" {
		return fmt.Errorf("LPSM-IDENTITY-NO-EVIDENCE: edge %s requires evidence", e.UpstreamID)
	}
	switch e.EdgeType {
	case EdgeVerifiedSameProject, EdgePublisherClaim, EdgeHashMatch, EdgeFork, EdgeMirror:
	default:
		return fmt.Errorf("LPSM-IDENTITY-BAD-EDGE: %q", e.EdgeType)
	}
	node.Aliases = append(node.Aliases, e)
	sort.Slice(node.Aliases, func(i, j int) bool {
		if node.Aliases[i].SourceID != node.Aliases[j].SourceID {
			return node.Aliases[i].SourceID < node.Aliases[j].SourceID
		}
		return node.Aliases[i].UpstreamID < node.Aliases[j].UpstreamID
	})
	g.aliasToCanonical[aliasKey(e.SourceID, e.UpstreamID)] = canonicalID
	return nil
}

// CanonicalFor resolves an alias to its canonical id. The second return is
// false when the alias is unknown (callers must not invent a mapping).
func (g *Graph) CanonicalFor(sourceID, upstreamID string) (string, bool) {
	c, ok := g.aliasToCanonical[aliasKey(sourceID, upstreamID)]
	return c, ok
}

// Node returns the node for a canonical id.
func (g *Graph) Node(canonicalID string) (*Node, bool) {
	n, ok := g.nodes[canonicalID]
	return n, ok
}

// Verified reports whether the edge may be rendered as verified. Only
// verified_same_project and hash_match qualify; publisher_claim, fork and
// mirror never do.
func Verified(e Edge) bool {
	return e.EdgeType == EdgeVerifiedSameProject || e.EdgeType == EdgeHashMatch
}

// Canonicals returns all canonical ids in sorted order.
func (g *Graph) Canonicals() []string {
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
