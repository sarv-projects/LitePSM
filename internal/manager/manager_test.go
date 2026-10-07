package manager

import (
	"context"
	"strings"
	"testing"
)

func TestPlanNPMDisablesScripts(t *testing.T) {
	p, err := PlanInstall("npm", "my-pkg", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 1 {
		t.Fatalf("steps = %d", len(p.Steps))
	}
	joined := strings.Join(p.Steps[0].Args, " ")
	if !strings.Contains(joined, "--ignore-scripts") {
		t.Errorf("npm plan must disable lifecycle scripts: %q", joined)
	}
}

func TestUnknownEcosystemFailsClosed(t *testing.T) {
	if _, err := PlanInstall("wat", "x", ""); err == nil {
		t.Error("unknown ecosystem must fail")
	}
}

func TestExecuteDisabledByDefault(t *testing.T) {
	p, _ := PlanInstall("uv", "my-pkg", "1.0")
	e := &Executor{}
	if err := e.Execute(context.Background(), p); err == nil {
		t.Error("Execute without opt-in must fail")
	}
}

func TestExecuteRunsInOrder(t *testing.T) {
	p, _ := PlanInstall("npm", "my-pkg", "")
	var ran []string
	e := &Executor{EnableExec: true, Runner: func(_ context.Context, s Step) error {
		ran = append(ran, string(s.Tool))
		return nil
	}}
	if err := e.Execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "npm" {
		t.Errorf("ran = %v", ran)
	}
}

func TestSupportedStable(t *testing.T) {
	if len(Supported()) != 7 {
		t.Errorf("Supported = %v", Supported())
	}
}
