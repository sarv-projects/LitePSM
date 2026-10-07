package main

// lock_test.go — ARCH/32 §4 lock verbs: freeze round-trip from a real local
// catalog index, determinism, the --check CI gate, staleness, --frozen drift,
// --sbom and --verify.
//
// The catalog is seeded the offline way: a release tree compiled with
// catalogbuild.CompileRelease and loaded through catalog.NewClient's cache
// load — the same path `litespm catalog sync` leaves behind — so the resolver
// closure sees exactly what a synced machine sees, with no network.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/lockfile"
	"github.com/sarv-projects/litespm/internal/manifest"
	"github.com/sarv-projects/litespm/internal/resolver"
)

const lockTestManifest = `schemaVersion = 1

[project]
name = "lock-fixture"
defaultScope = "project"

[[requires]]
id = "mcp:example:demo-mcp"
constraint = "1.0.0"
targets = ["claude-code"]

[lock]
required = true
`

// lockTestListing is a listing with two published versions: the manifest pins
// 1.0.0, the catalog's latest is 2.0.0 — the drift line --frozen must hold.
func lockTestListing() *domain.Listing {
	l := mcpListing("mcp:example:demo-mcp", "demo-mcp")
	l.Versions = []domain.VersionSummary{
		{Version: "1.0.0", ImmutableRef: "git:1111111111111111111111111111111111111111"},
		{Version: "2.0.0", ImmutableRef: "git:2222222222222222222222222222222222222222"},
	}
	l.Source = domain.SourceReference{
		SourceID:   "example",
		UpstreamID: "demo-mcp",
		URL:        "https://example.com/demo-mcp",
	}
	l.PublisherClaim = domain.PublisherClaim{Name: "Example Publisher"}
	l.ComponentsSummary = []domain.ComponentSummary{{Kind: domain.ComponentMCPProvider, Name: "server"}}
	return l
}

// lockTestVersions are published version records for lockTestListing: the
// version record is the only place the catalog states a transport.
func lockTestVersions() []*domain.VersionRecord {
	versions := make([]*domain.VersionRecord, 0, 2)
	for _, v := range []string{"1.0.0", "2.0.0"} {
		versions = append(versions, &domain.VersionRecord{
			ListingID: "mcp:example:demo-mcp",
			Version:   v,
			Components: []domain.Component{{
				ID:   string(domain.NewComponentID(domain.ListingID("mcp:example:demo-mcp"), v, domain.ComponentMCPProvider, "server")),
				Kind: domain.ComponentMCPProvider,
				Name: "server",
				Runtime: &domain.RuntimeDescriptor{
					Type:    "stdio",
					Command: "npx",
					Args:    []string{"-y", "demo-mcp"},
				},
			}},
		})
	}
	return versions
}

// seedLockCatalog compiles a release into a temp cache dir and returns a
// client whose index was loaded from that cache — the offline path a synced
// machine takes. No network is ever touched.
func seedLockCatalog(t *testing.T) *catalog.Client {
	t.Helper()
	out, err := catalogbuild.CompileRelease("rel-locktest-001", 1, nil,
		[]*domain.Listing{lockTestListing()}, lockTestVersions(), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	cacheDir := t.TempDir()
	if err := out.WriteToDirectory(cacheDir); err != nil {
		t.Fatalf("WriteToDirectory: %v", err)
	}
	client := catalog.NewClient("https://registry.invalid", cacheDir, nil)
	if client.Count() == 0 {
		t.Fatal("seeded catalog index is empty; cache did not load")
	}
	return client
}

// writeLockManifest writes litespm.yml into dir and returns its path.
func writeLockManifest(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "litespm.yml")
	if err := os.WriteFile(p, []byte(lockTestManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return p
}

// freezeLockFixture seeds manifest + catalog and freezes the lock without
// writing it.
func freezeLockFixture(t *testing.T, dir string) (*manifest.Manifest, *lockfile.Lock) {
	t.Helper()
	mPath := writeLockManifest(t, dir)
	m, err := manifest.Load(mPath)
	if err != nil {
		t.Fatalf("Load manifest: %v", err)
	}
	l, err := freezeProjectLock(context.Background(), m, seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freezeProjectLock: %v", err)
	}
	return m, l
}

// 1. Round-trip: freeze from the real local index → WriteFile → Load →
// Verify() ok, CheckManifest ok, and only catalog-published fields filled.
func TestLockFreezeRoundTripFromLocalIndex(t *testing.T) {
	dir := t.TempDir()
	m, l := freezeLockFixture(t, dir)

	if l.LockVersion != lockfile.LockVersion || l.SchemaVersion != lockfile.SchemaVersion {
		t.Errorf("versions = %d/%d, want %d/%d", l.LockVersion, l.SchemaVersion,
			lockfile.LockVersion, lockfile.SchemaVersion)
	}
	if want := lockfile.DigestString(m.MarshalCanonical()); l.ManifestDigest != want {
		t.Errorf("manifestDigest = %s, want %s", l.ManifestDigest, want)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(l.Entries))
	}
	e := l.Entries[0]

	// Fields the local catalog publishes.
	if e.ID != "mcp:example:demo-mcp" {
		t.Errorf("id = %q", e.ID)
	}
	if e.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (manifest pins it)", e.Version)
	}
	if e.Constraint != "1.0.0" {
		t.Errorf("constraint = %q", e.Constraint)
	}
	if len(e.Targets) != 1 || e.Targets[0] != "claude-code" {
		t.Errorf("targets = %v", e.Targets)
	}
	if e.SourceIdentity != "example" || e.SourceURL != "https://example.com/demo-mcp" {
		t.Errorf("source = %q/%q", e.SourceIdentity, e.SourceURL)
	}
	if e.PublisherName != "Example Publisher" {
		t.Errorf("publisher = %q", e.PublisherName)
	}
	if e.Transport != "stdio" {
		t.Errorf("transport = %q, want stdio from the version record", e.Transport)
	}
	if len(e.Components) != 1 || !strings.Contains(e.Components[0], "#mcp-provider/server") {
		t.Errorf("components = %v", e.Components)
	}

	// Honesty defaults: never fabricated green.
	if e.SignatureScheme != lockfile.SchemeNone || e.SignatureResult != lockfile.ResultUnavailable {
		t.Errorf("signature = %s/%s, want none/unavailable", e.SignatureScheme, e.SignatureResult)
	}
	if e.License != lockfile.LicenseNoAssertion {
		t.Errorf("license = %q, want NOASSERTION (catalog publishes no license)", e.License)
	}
	for name, val := range map[string]string{
		"artifactType": e.ArtifactType, "artifactLocator": e.ArtifactLocator,
		"artifactSha256": e.ArtifactSHA256, "treeDigest": e.TreeDigest,
		"policyDigest": e.PolicyDigest, "planDigest": e.PlanDigest, "commit": e.Commit,
	} {
		if val != "" {
			t.Errorf("%s = %q, must stay empty: the local catalog publishes none of these", name, val)
		}
	}
	if e.ArtifactSize != 0 {
		t.Errorf("artifactSize = %d, must stay 0", e.ArtifactSize)
	}

	// Write → Load → Verify → CheckManifest.
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, err := lockfile.Load(lockPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loaded.CheckManifest(m); err != nil {
		t.Fatalf("CheckManifest: %v", err)
	}
	if _, ok := loaded.Lookup("mcp:example:demo-mcp"); !ok {
		t.Error("Lookup failed after round-trip")
	}
	if loaded.LockDigest != l.LockDigest {
		t.Errorf("lockDigest drifted: %s != %s", loaded.LockDigest, l.LockDigest)
	}
}

// 2. Determinism: same manifest + same local catalog → byte-identical lock.
func TestLockFreezeDeterministicByteIdentical(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()

	mA := writeLockManifest(t, dirA)
	mB := writeLockManifest(t, dirB)
	mb, err := manifest.Load(mB)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	la, err := freezeProjectLock(context.Background(), mustLoadManifest(t, mA), seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freeze A: %v", err)
	}
	// Second freeze: fresh manifest load, fresh catalog client (fresh compile).
	lb, err := freezeProjectLock(context.Background(), mb, seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freeze B: %v", err)
	}
	if la.MarshalCanonical() != lb.MarshalCanonical() {
		t.Error("two freezes of the same manifest + same local catalog are not byte-identical")
	}

	// And the written bytes are identical too.
	pa, pb := filepath.Join(dirA, lockfile.LockFileName), filepath.Join(dirB, lockfile.LockFileName)
	if err := la.WriteFile(pa); err != nil {
		t.Fatal(err)
	}
	if err := lb.WriteFile(pb); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(pa)
	bb, _ := os.ReadFile(pb)
	if string(ba) != string(bb) {
		t.Error("written lock files differ byte-for-byte")
	}
}

func mustLoadManifest(t *testing.T, path string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load manifest: %v", err)
	}
	return m
}

// 3. --check: clean lock passes; a hand-edited lock and a missing lock fail
// with LPSM-LOCK-DRIFT (exit non-zero in runLock).
func TestLockCheckCleanPasses(t *testing.T) {
	dir := t.TempDir()
	m, l := freezeLockFixture(t, dir)
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := checkProjectLock(context.Background(), m, seedLockCatalog(t), lockPath); err != nil {
		t.Fatalf("clean check must pass, got: %v", err)
	}
}

func TestLockCheckHandEditedLockFails(t *testing.T) {
	dir := t.TempDir()
	m, l := freezeLockFixture(t, dir)
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the resolved version — a classic hand edit.
	edited := strings.Replace(string(raw), `version = "1.0.0"`, `version = "9.9.9"`, 1)
	if edited == string(raw) {
		t.Fatal("fixture does not contain the expected version line")
	}
	if err := os.WriteFile(lockPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	err = checkProjectLock(context.Background(), m, seedLockCatalog(t), lockPath)
	if err == nil {
		t.Fatal("hand-edited lock must fail --check")
	}
	if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("error must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

func TestLockCheckMissingLockFails(t *testing.T) {
	dir := t.TempDir()
	m := mustLoadManifest(t, writeLockManifest(t, dir))
	missing := filepath.Join(dir, lockfile.LockFileName)
	err := checkProjectLock(context.Background(), m, seedLockCatalog(t), missing)
	if err == nil {
		t.Fatal("missing lock must fail --check")
	}
	if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("error must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

// 4. Staleness: editing litespm.yml after locking is reported by CheckManifest
// and refused by the --frozen gate with LPSM-LOCK-DRIFT.
func TestLockStaleManifestRefused(t *testing.T) {
	dir := t.TempDir()
	mPath := writeLockManifest(t, dir)
	m := mustLoadManifest(t, mPath)
	l, err := freezeProjectLock(context.Background(), m, seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatal(err)
	}

	// Sanity: the unedited manifest is not stale.
	if err := l.CheckManifest(m); err != nil {
		t.Fatalf("unedited manifest must not be stale: %v", err)
	}

	// Edit the manifest: pin a different constraint.
	edited := strings.Replace(lockTestManifest, `constraint = "1.0.0"`, `constraint = ">=1.0.0"`, 1)
	if err := os.WriteFile(mPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	m2 := mustLoadManifest(t, mPath)
	if err := l.CheckManifest(m2); err == nil {
		t.Error("CheckManifest must report the edited manifest")
	} else if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("staleness must carry LPSM-LOCK-DRIFT, got: %v", err)
	}

	// The --frozen gate refuses with the same code.
	_, err = frozenVersionFor(dir, "mcp:example:demo-mcp", "")
	if err == nil {
		t.Fatal("frozen install must refuse a stale lock")
	}
	if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("frozen refusal must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

// 5. --frozen installs the LOCK's version even though the catalog's latest is
// newer, and refuses a listing the lock does not carry.
func TestFrozenInstallPinsLockedVersion(t *testing.T) {
	dir := t.TempDir()
	mPath := writeLockManifest(t, dir)
	m := mustLoadManifest(t, mPath)
	client := seedLockCatalog(t)

	// The catalog's latest for this listing is 2.0.0 (proves the divergence).
	r := resolver.NewResolver(&catalogResolutionProvider{client: client})
	res, err := r.Resolve(context.Background(), "mcp:example:demo-mcp", "")
	if err != nil {
		t.Fatalf("resolve latest: %v", err)
	}
	if res.SelectedVersions["mcp:example:demo-mcp"] != "2.0.0" {
		t.Fatalf("catalog latest = %q, want 2.0.0 for this fixture", res.SelectedVersions["mcp:example:demo-mcp"])
	}

	l, err := freezeProjectLock(context.Background(), m, client)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := l.WriteFile(filepath.Join(dir, lockfile.LockFileName)); err != nil {
		t.Fatal(err)
	}

	// The frozen gate hands back the locked version; runInstall passes it
	// where versionOrLatest(flags.version) used to run, so the install
	// requests 1.0.0 — never the catalog's 2.0.0.
	locked, err := frozenVersionFor(dir, "mcp:example:demo-mcp", "")
	if err != nil {
		t.Fatalf("frozenVersionFor: %v", err)
	}
	if locked != "1.0.0" {
		t.Fatalf("frozen version = %q, want the locked 1.0.0 (catalog latest is 2.0.0)", locked)
	}
	if got := versionOrLatest(locked); got != "1.0.0" {
		t.Errorf("versionOrLatest(%q) = %q, want 1.0.0", locked, got)
	}

	// An explicitly requested version that disagrees with the lock is drift.
	if _, err := frozenVersionFor(dir, "mcp:example:demo-mcp", "2.0.0"); err == nil {
		t.Error("--frozen with a --version that contradicts the lock must refuse")
	} else if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("refusal must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

func TestFrozenInstallRefusesListingNotInLock(t *testing.T) {
	dir := t.TempDir()
	mPath := writeLockManifest(t, dir)
	m := mustLoadManifest(t, mPath)
	l, err := freezeProjectLock(context.Background(), m, seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := l.WriteFile(filepath.Join(dir, lockfile.LockFileName)); err != nil {
		t.Fatal(err)
	}
	_, err = frozenVersionFor(dir, "skill:example:not-locked", "")
	if err == nil {
		t.Fatal("a listing absent from the lock must be refused")
	}
	if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("refusal must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

func TestFrozenInstallRefusesMissingLock(t *testing.T) {
	dir := t.TempDir()
	writeLockManifest(t, dir) // manifest present, lock absent
	_, err := frozenVersionFor(dir, "mcp:example:demo-mcp", "")
	if err == nil {
		t.Fatal("frozen install without a lock must fail closed")
	}
	if !strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		t.Errorf("refusal must carry LPSM-LOCK-DRIFT, got: %v", err)
	}
}

// 6a. --sbom: both formats parse as JSON and carry the lock digest.
func TestLockSBOMCarriesLockDigest(t *testing.T) {
	dir := t.TempDir()
	_, l := freezeLockFixture(t, dir)
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"cyclonedx-json", "spdx-json"} {
		doc, err := sbomFromLock(lockPath, format)
		if err != nil {
			t.Fatalf("sbomFromLock(%s): %v", format, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(doc, &parsed); err != nil {
			t.Errorf("%s: document is not valid JSON: %v", format, err)
		}
		if !strings.Contains(string(doc), l.LockDigest) {
			t.Errorf("%s: document does not carry the lock digest %s", format, l.LockDigest)
		}
	}
}

// 6b. --verify: per-entry output states the honest signature result.
func TestLockVerifyReportsUnavailableSignatures(t *testing.T) {
	dir := t.TempDir()
	mPath := writeLockManifest(t, dir)
	m := mustLoadManifest(t, mPath)
	l, err := freezeProjectLock(context.Background(), m, seedLockCatalog(t))
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	lockPath := filepath.Join(dir, lockfile.LockFileName)
	if err := l.WriteFile(lockPath); err != nil {
		t.Fatal(err)
	}
	lines, err := verifyProjectLock(mPath, lockPath)
	if err != nil {
		t.Fatalf("verifyProjectLock: %v", err)
	}
	report := strings.Join(lines, "\n")
	if !strings.Contains(report, "signature=none") {
		t.Errorf("verify report must state signature=none, got:\n%s", report)
	}
	if !strings.Contains(report, "result=unavailable") {
		t.Errorf("verify report must state result=unavailable, got:\n%s", report)
	}
	if !strings.Contains(report, l.LockDigest) {
		t.Errorf("verify report must state the lock digest, got:\n%s", report)
	}
}

// Flag parsing: usage errors, mode exclusivity, format validation.
func TestParseLockFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		check   bool
		verify  bool
		sbom    string
		help    bool
	}{
		{name: "check", args: []string{"--check"}, check: true},
		{name: "verify", args: []string{"--verify"}, verify: true},
		{name: "sbom cyclonedx", args: []string{"--sbom", "cyclonedx-json"}, sbom: "cyclonedx-json"},
		{name: "sbom spdx", args: []string{"--sbom", "spdx-json"}, sbom: "spdx-json"},
		{name: "help", args: []string{"--help"}, help: true},
		{name: "bad format", args: []string{"--sbom", "spdx-tag-value"}, wantErr: true},
		{name: "missing value", args: []string{"--sbom"}, wantErr: true},
		{name: "conflicting modes", args: []string{"--check", "--verify"}, wantErr: true},
		{name: "positional", args: []string{"extra"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseLockFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %v", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.check != tc.check || f.verify != tc.verify || f.sbom != tc.sbom || f.showHelp != tc.help {
				t.Errorf("parsed %+v, want check=%v verify=%v sbom=%q help=%v", f, tc.check, tc.verify, tc.sbom, tc.help)
			}
		})
	}
}

// The install-side flag parses and defaults off; non-frozen behavior is
// unchanged.
func TestParseInstallFlagsFrozen(t *testing.T) {
	f, err := parseInstallFlags([]string{"mcp:example:demo-mcp"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.frozen {
		t.Error("--frozen must default to false")
	}
	f, err = parseInstallFlags([]string{"mcp:example:demo-mcp", "--frozen", "--version", "1.0.0"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !f.frozen {
		t.Error("--frozen not parsed")
	}
	if f.version != "1.0.0" {
		t.Errorf("version = %q", f.version)
	}
}
