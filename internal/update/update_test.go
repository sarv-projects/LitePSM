package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// releaseFixture describes a fake release served over TLS by newReleaseServer.
type releaseFixture struct {
	version   string
	payload   []byte
	binName   string
	sumsName  string
	sumsEntry string // filename written into SHA256SUMS.txt (defaults to binName)
	checksum  string
	omitBin   bool
	omitSums  bool
	insecure  bool // emit http:// (not https://) asset URLs
	published time.Time
}

// newReleaseServer starts an HTTPS test server exposing a release manifest and
// the assets it references. It is the only network the tests touch.
func newReleaseServer(t *testing.T, version string, payload []byte, opts ...func(*releaseFixture)) *httptest.Server {
	t.Helper()
	fx := releaseFixture{
		version:   version,
		payload:   payload,
		binName:   TargetBinaryName(),
		sumsName:  checksumManifestAsset,
		sumsEntry: TargetBinaryName(),
		checksum:  sha256Hex(payload),
		published: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	}
	for _, opt := range opts {
		opt(&fx)
	}

	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	assetBase := srv.URL
	if fx.insecure {
		assetBase = "http://" + strings.TrimPrefix(srv.URL, "https://")
	}

	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		assets := make([]map[string]string, 0, 2)
		if !fx.omitBin {
			assets = append(assets, map[string]string{
				"name":                 fx.binName,
				"browser_download_url": assetBase + "/" + fx.binName,
			})
		}
		if !fx.omitSums {
			assets = append(assets, map[string]string{
				"name":                 fx.sumsName,
				"browser_download_url": assetBase + "/" + fx.sumsName,
			})
		}
		doc := map[string]any{
			"tag_name":     "v" + fx.version,
			"html_url":     srv.URL + "/releases/tag/v" + fx.version,
			"published_at": fx.published.Format(time.RFC3339),
			"assets":       assets,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/"+fx.binName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fx.payload)
	})
	mux.HandleFunc("/"+fx.sumsName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, fx.checksum+"  "+fx.sumsEntry+"\n")
	})
	return srv
}

// newTLSUpdater points an Updater at srv's manifest and trusts its test cert.
func newTLSUpdater(srv *httptest.Server) *Updater {
	u := NewUpdater(srv.URL + "/manifest.json")
	u.client = srv.Client()
	return u
}

func writeExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertNoUpdateLeftovers(t *testing.T, dir, targetPath string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".litespm-update-") {
			t.Fatalf("staging directory %q was left behind", e.Name())
		}
	}
	if _, err := os.Stat(targetPath + ".old"); err == nil {
		t.Fatalf("backup %q was left behind", targetPath+".old")
	}
}

// TestCheckForUpdateAndApplyHappyPath proves the full path: HTTPS manifest
// fetch, checksum download, checksum verification, staged replacement, and
// post-replacement boot verification.
func TestCheckForUpdateAndApplyHappyPath(t *testing.T) {
	ctx := context.Background()
	payload := []byte("#!/bin/sh\necho LiteSPM v9.9.9\n")
	srv := newReleaseServer(t, "9.9.9", payload)
	u := newTLSUpdater(srv)

	status, info, err := u.CheckForUpdate(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate failed: %v", err)
	}
	if !status.UpdateAvailable {
		t.Fatalf("expected an available update, got %+v", status)
	}
	if status.LatestVersion != "9.9.9" || status.CurrentVersion != "1.0.0" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if info.Version != "9.9.9" {
		t.Fatalf("unexpected release version: %q", info.Version)
	}
	binName := TargetBinaryName()
	if got := info.ChecksumsSHA256[binName]; got != sha256Hex(payload) {
		t.Fatalf("checksum not populated: %q", got)
	}
	if got := info.DownloadURLs[binName]; got != srv.URL+"/"+binName {
		t.Fatalf("download URL not populated: %q", got)
	}
	if want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC); !info.PublishedAt.Equal(want) {
		t.Fatalf("published time = %v, want %v", info.PublishedAt, want)
	}

	// The fake binary is a shell script, which only executes on unix.
	if runtime.GOOS == "windows" {
		t.Skip("fake shell payload cannot execute on windows")
	}
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "litespm")
	writeExecutable(t, targetPath, []byte("#!/bin/sh\necho old\n"))

	if err := u.ApplyUpdate(ctx, payload, info.ChecksumsSHA256[binName], targetPath, targetDir); err != nil {
		t.Fatalf("ApplyUpdate failed: %v", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("target not replaced: %q", got)
	}
	if err := VerifySelfBoot(ctx, targetPath); err != nil {
		t.Fatalf("replaced binary does not boot: %v", err)
	}
	assertNoUpdateLeftovers(t, targetDir, targetPath)
}

// TestCheckForUpdateRejectsHTTPManifest proves non-https manifest URLs are
// refused before any request is made.
func TestCheckForUpdateRejectsHTTPManifest(t *testing.T) {
	u := NewUpdater("http://example.invalid/manifest.json")
	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https refusal, got %v", err)
	}
}

// TestCheckForUpdateRejectsInsecureAssetURL proves a manifest cannot redirect
// the updater at an http:// asset.
func TestCheckForUpdateRejectsInsecureAssetURL(t *testing.T) {
	srv := newReleaseServer(t, "9.9.9", []byte("x"), func(f *releaseFixture) { f.insecure = true })
	u := newTLSUpdater(srv)
	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("expected insecure asset URL refusal, got %v", err)
	}
}

// TestCheckForUpdateMissingChecksumAsset proves an available update with no
// published checksum listing fails closed with an explicit reason.
func TestCheckForUpdateMissingChecksumAsset(t *testing.T) {
	srv := newReleaseServer(t, "9.9.9", []byte("x"), func(f *releaseFixture) { f.omitSums = true })
	u := newTLSUpdater(srv)
	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil || !strings.Contains(err.Error(), checksumManifestAsset) {
		t.Fatalf("expected missing checksum error, got %v", err)
	}
}

// TestCheckForUpdateMissingChecksumEntry proves a checksum listing that lacks
// the target platform also fails closed.
func TestCheckForUpdateMissingChecksumEntry(t *testing.T) {
	srv := newReleaseServer(t, "9.9.9", []byte("x"), func(f *releaseFixture) {
		f.sumsEntry = "some-other-platform"
	})
	u := newTLSUpdater(srv)
	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "no SHA-256 checksum") {
		t.Fatalf("expected missing checksum entry error, got %v", err)
	}
}

// TestCheckForUpdateRejectsDowngradeUnlessForced proves the monotonicity guard.
func TestCheckForUpdateRejectsDowngradeUnlessForced(t *testing.T) {
	srv := newReleaseServer(t, "1.0.0", []byte("x"))
	u := newTLSUpdater(srv)
	ctx := context.Background()

	status, _, err := u.CheckForUpdate(ctx, "2.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate failed: %v", err)
	}
	if status.UpdateAvailable {
		t.Fatalf("downgrade was advertised: %+v", status)
	}

	forced, _, err := u.CheckForUpdateWithOptions(ctx, "2.0.0", true)
	if err != nil {
		t.Fatalf("forced CheckForUpdate failed: %v", err)
	}
	if !forced.UpdateAvailable {
		t.Fatalf("forced downgrade was not advertised: %+v", forced)
	}

	same, _, err := u.CheckForUpdate(ctx, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if same.UpdateAvailable {
		t.Fatalf("reinstall of the installed version was advertised: %+v", same)
	}
}

// TestCheckForUpdateBoundsManifestRead proves remote reads are bounded.
func TestCheckForUpdateBoundsManifestRead(t *testing.T) {
	srv := newReleaseServer(t, "9.9.9", []byte("x"))
	u := newTLSUpdater(srv)
	u.maxManifestBytes = 16
	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected read limit error, got %v", err)
	}
}

// TestCheckForUpdateRespectsCancelledContext proves the request honours ctx.
func TestCheckForUpdateRespectsCancelledContext(t *testing.T) {
	srv := newReleaseServer(t, "9.9.9", []byte("x"))
	u := newTLSUpdater(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := u.CheckForUpdate(ctx, "1.0.0"); err == nil {
		t.Fatal("expected cancelled context to fail")
	}
}

// TestApplyUpdateChecksumMismatchLeavesTargetUntouched binds the pre-write
// checksum gate. The payload is a bootable script and the expected checksum is
// deliberately wrong. If the early comparison were removed, ApplyUpdate would
// proceed to the swap and fail later at the post-replacement re-hash with a
// DIFFERENT, non-checksum-mismatch error before rolling back — so asserting the
// specific checksum-mismatch error code (not merely a non-nil error) is what
// makes this test fail when the gate is disabled. The target must be untouched
// and no staging/backup leftovers may remain.
func TestApplyUpdateChecksumMismatchLeavesTargetUntouched(t *testing.T) {
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "litespm")
	writeExecutable(t, targetPath, []byte("original"))

	payload := []byte("#!/bin/sh\necho LiteSPM v9.9.9\n")
	u := NewUpdater("")
	err := u.ApplyUpdate(context.Background(), payload, strings.Repeat("a", 64), targetPath, targetDir)
	if err == nil {
		t.Fatal("a checksum mismatch was accepted")
	}
	var lpsmErr *domain.LPSMError
	if !errors.As(err, &lpsmErr) || lpsmErr.Code != "LPSM-VERIFY-CHECKSUM-MISMATCH" {
		t.Fatalf("expected a pre-write checksum-mismatch refusal, got: %v", err)
	}
	got, _ := os.ReadFile(targetPath)
	if string(got) != "original" {
		t.Fatalf("target was modified despite refusal: %q", got)
	}
	assertNoUpdateLeftovers(t, targetDir, targetPath)
}

// TestApplyUpdateAppliesBootablePayloadWithCorrectChecksum is the positive
// control for TestApplyUpdateChecksumMismatchLeavesTargetUntouched: the same
// bootable payload with its correct checksum is applied successfully. The boot
// probe is injected so the assertion is deterministic on every platform (the
// real probe executes the binary and therefore needs a unix shell).
func TestApplyUpdateAppliesBootablePayloadWithCorrectChecksum(t *testing.T) {
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "litespm")
	writeExecutable(t, targetPath, []byte("#!/bin/sh\necho old\n"))

	payload := []byte("#!/bin/sh\necho LiteSPM v9.9.9\n")
	u := NewUpdater("")
	bootChecked := false
	u.bootCheck = func(_ context.Context, path string) error {
		bootChecked = true
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(got) != string(payload) {
			return fmt.Errorf("boot probe observed unexpected content")
		}
		return nil
	}

	if err := u.ApplyUpdate(context.Background(), payload, sha256Hex(payload), targetPath, targetDir); err != nil {
		t.Fatalf("ApplyUpdate with the correct checksum failed: %v", err)
	}
	if !bootChecked {
		t.Fatal("boot probe was not invoked after replacement")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("target not replaced: %q", got)
	}
	assertNoUpdateLeftovers(t, targetDir, targetPath)
}

// TestApplyUpdateFailsClosedWithoutChecksum pins the policy that replaced a
// silent skip: an update with no expected checksum must be refused, because
// "no integrity data" must never mean "no integrity check".
func TestApplyUpdateFailsClosedWithoutChecksum(t *testing.T) {
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "litespm")
	writeExecutable(t, targetPath, []byte("original"))

	u := NewUpdater("")
	err := u.ApplyUpdate(context.Background(), []byte("payload"), "", targetPath, targetDir)
	if err == nil {
		t.Fatal("an update with no expected checksum was applied")
	}
	if !strings.Contains(err.Error(), "no expected SHA-256 checksum") {
		t.Fatalf("error does not name the reason: %v", err)
	}
	got, _ := os.ReadFile(targetPath)
	if string(got) != "original" {
		t.Fatalf("target was modified despite refusal: %q", got)
	}
	assertNoUpdateLeftovers(t, targetDir, targetPath)
}

// TestApplyUpdateRollsBackWhenVerificationFails proves that a post-replacement
// verification failure restores the original binary.
func TestApplyUpdateRollsBackWhenVerificationFails(t *testing.T) {
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "litespm")
	original := []byte("original-binary")
	writeExecutable(t, targetPath, original)

	payload := []byte("#!/bin/sh\necho LiteSPM v9.9.9\n")
	u := NewUpdater("")
	u.bootCheck = func(context.Context, string) error { return errors.New("forced boot failure") }

	err := u.ApplyUpdate(context.Background(), payload, sha256Hex(payload), targetPath, targetDir)
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if !strings.Contains(err.Error(), "original binary restored") {
		t.Fatalf("error does not report rollback: %v", err)
	}
	got, _ := os.ReadFile(targetPath)
	if string(got) != string(original) {
		t.Fatalf("rollback did not restore the original: %q", got)
	}
	assertNoUpdateLeftovers(t, targetDir, targetPath)
}

// TestApplyUpdateEmptyPayload is a small guard against no-op replacement.
func TestApplyUpdateEmptyPayload(t *testing.T) {
	u := NewUpdater("")
	if err := u.ApplyUpdate(context.Background(), nil, strings.Repeat("a", 64), "ignored", ""); err == nil {
		t.Fatal("empty payload was accepted")
	}
}

// TestVerifySelfBoot exercises the real boot probe.
func TestVerifySelfBoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell payload cannot execute on windows")
	}
	dir := t.TempDir()
	ctx := context.Background()

	good := filepath.Join(dir, "good")
	writeExecutable(t, good, []byte("#!/bin/sh\necho LiteSPM v1.2.3\n"))
	if err := VerifySelfBoot(ctx, good); err != nil {
		t.Fatalf("good binary rejected: %v", err)
	}

	wrongBanner := filepath.Join(dir, "wrong-banner")
	writeExecutable(t, wrongBanner, []byte("#!/bin/sh\necho something-else\n"))
	if err := VerifySelfBoot(ctx, wrongBanner); err == nil {
		t.Fatal("binary with unexpected output was accepted")
	}

	nonZero := filepath.Join(dir, "non-zero")
	writeExecutable(t, nonZero, []byte("#!/bin/sh\nexit 3\n"))
	if err := VerifySelfBoot(ctx, nonZero); err == nil {
		t.Fatal("binary that exits non-zero was accepted")
	}
}

// TestWriteExclusiveFileRefusesExisting proves O_EXCL semantics: an existing
// path is never overwritten.
func TestWriteExclusiveFileRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "litespm.new")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveFile(path, []byte("new"), 0o755); err == nil {
		t.Fatal("O_EXCL did not refuse an existing path")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "existing" {
		t.Fatalf("existing file was modified: %q", got)
	}
}

// TestWriteExclusiveFileDoesNotFollowSymlink proves a pre-existing symlink at
// the staged name is not followed.
func TestWriteExclusiveFileDoesNotFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("victim"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "litespm.new")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveFile(link, []byte("attack"), 0o755); err == nil {
		t.Fatal("O_EXCL did not refuse a pre-existing symlink")
	}
	got, _ := os.ReadFile(victim)
	if string(got) != "victim" {
		t.Fatalf("symlink was followed; victim modified: %q", got)
	}
}

// TestParseSHA256SUMS covers sha256sum and shasum output variants.
func TestParseSHA256SUMS(t *testing.T) {
	sumA := strings.Repeat("a", 64)
	sumB := strings.Repeat("b", 64)
	body := "# generated\n" + sumA + "  bin/linux-amd64\n" + sumB + " *other.exe\n\nnot a checksum\n"
	out := parseSHA256SUMS([]byte(body))
	if out["bin/linux-amd64"] != sumA {
		t.Fatalf("full path entry not parsed: %v", out)
	}
	if out["linux-amd64"] != sumA {
		t.Fatalf("basename entry not parsed: %v", out)
	}
	if out["other.exe"] != sumB {
		t.Fatalf("binary-mode entry not parsed: %v", out)
	}
	if len(out) != 3 {
		t.Fatalf("unexpected entries: %v", out)
	}
}

func TestTargetBinaryName(t *testing.T) {
	name := TargetBinaryName()
	if name == "" || !strings.HasPrefix(name, "litespm-") {
		t.Fatalf("unexpected target binary name %q", name)
	}
}
