package trust

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/lockfile"
	"github.com/sarv-projects/litespm/internal/manifest"
)

func testLock(t *testing.T) *lockfile.Lock {
	t.Helper()
	m, err := manifest.Parse([]byte("schemaVersion = 1\n[project]\nname = \"x\"\n[[requires]]\nid = \"mcp:builtin:mcp-registry:pg\"\nconstraint = \"1.0.0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := lockfile.Freeze(m, func(id, _ string) (lockfile.LockedEntry, error) {
		return lockfile.LockedEntry{ID: id, Version: "1.0.0", ArtifactSHA256: "sha256:" + strings.Repeat("c", 64), License: "Apache-2.0"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCosignAbsentIsUnavailable(t *testing.T) {
	v := CosignVerifier{Identity: "x", Issuer: "y", LookPath: func(string) (string, error) {
		return "", errors.New("not found")
	}}
	ver := v.Verify(context.Background(), "ghcr.io/x/y@sha256:z", "")
	if ver.Result != ResultUnavailable {
		t.Errorf("missing cosign must be unavailable, got %v", ver)
	}
}

func TestCosignWithoutIdentityIsUnavailable(t *testing.T) {
	v := CosignVerifier{LookPath: func(s string) (string, error) { return "/usr/bin/" + s, nil }}
	if ver := v.Verify(context.Background(), "img", ""); ver.Result != ResultUnavailable {
		t.Errorf("unconfigured verifier must be unavailable, got %v", ver)
	}
}

func TestSHA256Pin(t *testing.T) {
	blob := []byte("hello")
	pinned := lockfile.DigestBytes(blob)
	if ver := SHA256Verifier(blob, pinned); ver.Result != ResultVerified {
		t.Errorf("matching pin: %v", ver)
	}
	if ver := SHA256Verifier(blob, "sha256:"+strings.Repeat("0", 64)); ver.Result != ResultFailed {
		t.Errorf("mismatch must fail: %v", ver)
	}
	if ver := SHA256Verifier(blob, ""); ver.Result != ResultUnavailable {
		t.Errorf("no pin must be unavailable: %v", ver)
	}
}

func TestSBOMCarriesLockDigest(t *testing.T) {
	l := testLock(t)
	for _, f := range []string{"cyclonedx-json", "spdx-json"} {
		b, err := SBOM(l, f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s is not JSON: %v", f, err)
		}
		if !strings.Contains(string(b), l.LockDigest) {
			t.Errorf("%s does not carry the lock digest", f)
		}
	}
	if _, err := SBOM(l, "bogus"); err == nil {
		t.Error("bad SBOM format must fail")
	}
}

func TestAdvisoryQuarantine(t *testing.T) {
	s := NewAdvisoryStore()
	unsigned := Advisory{AdvisoryID: "adv_1", PackageID: "mcp:x:y:z", Severity: "critical", Summary: "bad", Signed: false}
	if err := s.Add(unsigned); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Quarantined("mcp:x:y:z"); ok {
		t.Error("unsigned advisory must not quarantine")
	}
	signed := Advisory{AdvisoryID: "adv_2", PackageID: "mcp:x:y:z", Severity: "high", Summary: "bad", Signed: true}
	if err := s.Add(signed); err != nil {
		t.Fatal(err)
	}
	if id, ok := s.Quarantined("mcp:x:y:z"); !ok || id != "adv_2" {
		t.Errorf("signed advisory must quarantine: %v %v", id, ok)
	}
	s.RevokeSigner("key-1")
	if !s.SignerRevoked("key-1") {
		t.Error("revocation must stick")
	}
}
