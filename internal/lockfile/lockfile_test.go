package lockfile

import (
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/manifest"
)

func testManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(`schemaVersion = 1
[project]
name = "acme"
defaultScope = "project"
[[requires]]
id = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"
targets = ["claude-code"]
[[requires]]
id = "skill:builtin:agent-skills:review-pr"
constraint = "1.2.0"
[lock]
required = true
`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func stubResolver(id, constraint string) (LockedEntry, error) {
	_ = constraint
	return LockedEntry{
		ID:            id,
		Version:       "1.4.2",
		ArtifactType:  "oci",
		ArtifactLocator: "ghcr.io/example/postgres-mcp@sha256:abc",
		ArtifactSHA256:  "sha256:" + strings.Repeat("a", 64),
		TreeDigest:      "sha256:" + strings.Repeat("b", 64),
		Transport:       "stdio",
	}, nil
}

func TestFreezeDeterministic(t *testing.T) {
	m := testManifest(t)
	a, err := Freeze(m, stubResolver)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	b, err := Freeze(m, stubResolver)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if a.LockDigest != b.LockDigest {
		t.Error("freeze is not deterministic")
	}
	if len(a.Entries) != 2 || a.Entries[0].ID > a.Entries[1].ID {
		t.Error("entries must be sorted by ID")
	}
	// Honesty defaults.
	for _, e := range a.Entries {
		if e.SignatureScheme != SchemeNone || e.SignatureResult != ResultUnavailable {
			t.Errorf("entry %s fabricates signature state: %s/%s", e.ID, e.SignatureScheme, e.SignatureResult)
		}
		if e.License == "" {
			t.Errorf("entry %s has empty license", e.ID)
		}
	}
}

func TestLockRoundTripAndVerify(t *testing.T) {
	m := testManifest(t)
	l, err := Freeze(m, stubResolver)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/litespm.lock"
	if err := l.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loaded.CheckManifest(m); err != nil {
		t.Fatalf("CheckManifest: %v", err)
	}
	if _, ok := loaded.Lookup("mcp:builtin:mcp-registry:postgres"); !ok {
		t.Error("Lookup failed")
	}
}

func TestTamperDetected(t *testing.T) {
	m := testManifest(t)
	l, err := Freeze(m, stubResolver)
	if err != nil {
		t.Fatal(err)
	}
	l.Entries[0].Version = "9.9.9"
	if err := l.Verify(); err == nil {
		t.Error("expected LPSM-LOCK-DRIFT on tampered entry")
	} else if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("wrong error code: %v", err)
	}
	// Stale manifest detection.
	m2 := testManifest(t)
	m2.Requires = append(m2.Requires, m2.Requires[0])
	l2, _ := Freeze(testManifest(t), stubResolver)
	if err := l2.CheckManifest(m2); err == nil {
		t.Error("expected drift when manifest changed")
	}
}

func TestNoTimestampsInLock(t *testing.T) {
	m := testManifest(t)
	l, _ := Freeze(m, stubResolver)
	out := l.MarshalCanonical()
	for _, bad := range []string{"2026", "CreatedAt", "createdAt", "Timestamp"} {
		if strings.Contains(out, bad) {
			t.Errorf("lock output leaks %q", bad)
		}
	}
}
