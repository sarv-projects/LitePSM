package runtime

import (
	"context"
	"testing"
)

func TestRegistryKinds(t *testing.T) {
	r := NewRegistry()
	if len(r.Kinds()) != 5 {
		t.Errorf("Kinds = %v", r.Kinds())
	}
	for _, k := range []Kind{KindNode, KindPython, KindNative, KindOCI, KindRemote} {
		if _, ok := r.Get(k); !ok {
			t.Errorf("missing adapter %s", k)
		}
	}
}

func TestNativeRefusesUnresolved(t *testing.T) {
	a := NativeAdapter{}
	_, err := a.Plan(context.Background(), Spec{Command: "definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Error("expected fail-closed for unresolvable binary")
	}
}

func TestOCIRequiresPin(t *testing.T) {
	_, err := PlanOCI(Spec{Image: "ghcr.io/example/srv:latest"}, false)
	if err == nil {
		t.Error("tag-only image must be refused without allowUnpinned")
	}
	l, err := PlanOCI(Spec{Image: "ghcr.io/example/srv@sha256:" + repeatA(64)}, false)
	if err != nil {
		t.Fatalf("pinned image: %v", err)
	}
	if len(l.Args) == 0 || l.Args[0] != "run" {
		t.Errorf("expected docker run line, got %v", l.Args)
	}
}

func TestRemoteRefusesHTTP(t *testing.T) {
	a := RemoteAdapter{}
	if _, err := a.Plan(context.Background(), Spec{Endpoint: "http://evil.example.com/mcp"}); err == nil {
		t.Error("non-https remote must be refused")
	}
	if _, err := a.Plan(context.Background(), Spec{Endpoint: "https://example.com/mcp"}); err != nil {
		t.Errorf("https remote: %v", err)
	}
}

func TestNodeRequiresCommand(t *testing.T) {
	if _, err := (NodeAdapter{}).Plan(context.Background(), Spec{}); err == nil {
		t.Error("empty command must fail")
	}
}

func repeatA(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
