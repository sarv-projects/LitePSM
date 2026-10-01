package agent

import (
	"strings"
	"testing"
)

const sampleRegistry = `{
  "version": "1.0.0",
  "agents": [
    {"id":"npx-agent","name":"NPX Agent","version":"1.2.3","description":"d",
     "distribution":{"npx":{"package":"@scope/npx-agent@1.2.3","args":["--acp"],"env":{"K":"V"}}}},
    {"id":"uvx-agent","name":"UVX Agent","version":"2.0.0",
     "distribution":{"uvx":{"package":"uvx-agent==2.0.0","args":["--stdio"]}}},
    {"id":"bin-agent","name":"Binary Agent","version":"3.1.0",
     "distribution":{"binary":{"linux-x86_64":{"archive":"https://example.com/a.tar.gz","cmd":"./bin-agent","args":["acp"],"sha256":"deadbeef"}}}}
  ]
}`

func TestParseRegistry(t *testing.T) {
	reg, err := ParseRegistry([]byte(sampleRegistry))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(reg.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(reg.Agents))
	}
	if reg.Agents[0].ID != "npx-agent" || reg.Agents[0].Distribution.Npx == nil {
		t.Fatalf("unexpected first agent: %+v", reg.Agents[0])
	}
}

func TestParseRegistryRejectsEmpty(t *testing.T) {
	if _, err := ParseRegistry([]byte(`{"version":"1","agents":[]}`)); err == nil {
		t.Fatal("expected error for empty agent list")
	}
	if _, err := ParseRegistry(nil); err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestMatchTarget(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         Target
		ok           bool
	}{
		{"linux", "amd64", TargetLinuxAMD64, true},
		{"linux", "arm64", TargetLinuxARM64, true},
		{"darwin", "arm64", TargetDarwinARM64, true},
		{"darwin", "amd64", TargetDarwinAMD64, true},
		{"windows", "amd64", TargetWindowsAMD64, true},
		{"windows", "arm64", TargetWindowsARM64, true},
		{"plan9", "amd64", "", false},
	}
	for _, c := range cases {
		got, ok := MatchTarget(c.goos, c.goarch)
		if got != c.want || ok != c.ok {
			t.Fatalf("MatchTarget(%s,%s) = (%q,%v), want (%q,%v)", c.goos, c.goarch, got, ok, c.want, c.ok)
		}
	}
}

func TestACPResolve(t *testing.T) {
	reg, err := ParseRegistry([]byte(sampleRegistry))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	a := NewACPAdapter()

	npx := reg.Agents[0]
	spec, err := a.Resolve(npx, TargetLinuxAMD64, "/tmp/bin")
	if err != nil {
		t.Fatalf("npx resolve failed: %v", err)
	}
	if spec.Strategy != "npx" || !strings.HasPrefix(spec.Executable, "npx") {
		t.Fatalf("unexpected npx spec: %+v", spec)
	}
	if len(spec.Args) < 3 || spec.Args[1] != "@scope/npx-agent@1.2.3" || spec.Args[2] != "--acp" {
		t.Fatalf("unexpected npx args: %v", spec.Args)
	}
	if spec.Env["K"] != "V" {
		t.Fatalf("expected env to pass through, got %v", spec.Env)
	}

	uvx := reg.Agents[1]
	spec, err = a.Resolve(uvx, TargetLinuxAMD64, "/tmp/bin")
	if err != nil {
		t.Fatalf("uvx resolve failed: %v", err)
	}
	if spec.Strategy != "uvx" || spec.Executable != "uvx" || spec.Args[0] != "uvx-agent==2.0.0" {
		t.Fatalf("unexpected uvx spec: %+v", spec)
	}

	bin := reg.Agents[2]
	spec, err = a.Resolve(bin, TargetLinuxAMD64, "/tmp/bin")
	if err != nil {
		t.Fatalf("binary resolve failed: %v", err)
	}
	if spec.Strategy != "binary" || !strings.HasSuffix(spec.Executable, "bin-agent") {
		t.Fatalf("unexpected binary spec: %+v", spec)
	}
	if spec.SHA256 != "deadbeef" || spec.Archive != "https://example.com/a.tar.gz" {
		t.Fatalf("expected archive digest metadata, got %+v", spec)
	}

	if _, err := a.Resolve(bin, TargetWindowsAMD64, "/tmp/bin"); err == nil {
		t.Fatal("expected error resolving an unavailable target")
	}
}
