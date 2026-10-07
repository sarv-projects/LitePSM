package inventory

import "testing"

func TestObserveAndSnapshot(t *testing.T) {
	c := New()
	if err := c.Observe(ObservedNode{CapabilityID: "a", HostID: "codex", FilePath: "/x", Locator: "mcpServers.a", ImageHash: HashImage([]byte("{}"))}); err != nil {
		t.Fatal(err)
	}
	if err := c.Observe(ObservedNode{CapabilityID: "", FilePath: "/x", Locator: "l"}); err == nil {
		t.Error("empty capability must fail")
	}
	snap := c.Snapshot()
	if len(snap.Nodes) != 1 {
		t.Errorf("nodes = %d", len(snap.Nodes))
	}
}

func TestHashStable(t *testing.T) {
	if HashJSON(map[string]any{"b": 1, "a": 2}) != HashJSON(map[string]any{"a": 2, "b": 1}) {
		t.Error("map hash must be order-independent")
	}
}
