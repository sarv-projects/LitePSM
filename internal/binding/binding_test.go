package binding

import (
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestManagedIsDefault(t *testing.T) {
	d, err := Resolve(Request{ListingID: "mcp:builtin:mcp-registry:x", HostID: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Mode != ModeManaged || d.EntryStyle != "bridge" || !d.NeedsBridge {
		t.Errorf("default must be managed-behind-bridge: %+v", d)
	}
}

func TestNativeIsExplicitOptIn(t *testing.T) {
	d, err := Resolve(Request{ListingID: "mcp:builtin:mcp-registry:x", HostID: "codex", Mode: ModeNative})
	if err != nil {
		t.Fatal(err)
	}
	if d.EntryStyle != "native" || d.NeedsBridge {
		t.Errorf("native opt-in: %+v", d)
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("bad mode must fail")
	}
}

func TestPluginRouting(t *testing.T) {
	comps := []PluginComponent{
		{Name: "s1", Kind: domain.ComponentSkill},
		{Name: "m1", Kind: domain.ComponentMCPProvider},
		{Name: "c1", Kind: domain.ComponentCommand},
		{Name: "r1", Kind: domain.ComponentRule},
		{Name: "h1", Kind: domain.ComponentHook},
		{Name: "l1", Kind: domain.ComponentLSP},
	}
	sup, rej := RoutePlugin(comps)
	if len(sup) != 5 || len(rej) != 1 {
		t.Errorf("supported=%d rejected=%d", len(sup), len(rej))
	}
	if rej[0].Name != "c1" {
		t.Errorf("rejected = %v", rej)
	}
}
