package identity

import "testing"

const pgID = "mcp:builtin:mcp-registry:postgres"

func testGraph(t *testing.T) *Graph {
	t.Helper()
	g := New()
	if err := g.AddCanonical(pgID); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAddAndResolve(t *testing.T) {
	g := testGraph(t)
	err := g.AddEdge(pgID, Edge{
		SourceID:   "npm:myorg",
		UpstreamID: "@myorg/pg-mcp",
		EdgeType:   EdgePublisherClaim,
		Evidence:   "publisher README links the MCP registry entry",
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	got, ok := g.CanonicalFor("npm:myorg", "@myorg/pg-mcp")
	if !ok || got != pgID {
		t.Errorf("CanonicalFor = %q, %v", got, ok)
	}
	if _, ok := g.CanonicalFor("npm:myorg", "nope"); ok {
		t.Error("unknown alias must not resolve")
	}
}

func TestEvidenceRequired(t *testing.T) {
	g := testGraph(t)
	err := g.AddEdge(pgID, Edge{SourceID: "npm:myorg", UpstreamID: "x", EdgeType: EdgeHashMatch})
	if err == nil {
		t.Error("hash_match without evidence must fail")
	}
	err = g.AddEdge(pgID, Edge{SourceID: "bad source!!", UpstreamID: "x", EdgeType: EdgeFork, Evidence: "e"})
	if err == nil {
		t.Error("bad source id must fail")
	}
}

func TestVerifiedRendering(t *testing.T) {
	cases := map[EdgeType]bool{
		EdgeVerifiedSameProject: true,
		EdgeHashMatch:           true,
		EdgePublisherClaim:      false,
		EdgeFork:                false,
		EdgeMirror:              false,
	}
	for typ, want := range cases {
		if got := Verified(Edge{EdgeType: typ}); got != want {
			t.Errorf("Verified(%s) = %v, want %v", typ, got, want)
		}
	}
}

func TestBadCanonicalRejected(t *testing.T) {
	g := New()
	if err := g.AddCanonical("not-an-id"); err == nil {
		t.Error("expected rejection of malformed canonical id")
	}
}
