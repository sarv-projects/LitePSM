// Package update implements LiteSPM's binary self-update path: discovering a
// newer release, verifying its integrity, and replacing the running executable.
//
// # Release manifest
//
// The release artifacts this package consumes are the ones the build actually
// publishes. `scripts/build-release.sh` compiles `litespm-<os>-<arch>[.exe]`
// for the six supported targets and writes a `SHA256SUMS.txt` listing beside
// them; `.github/workflows/release.yml` attaches those files to a GitHub
// Release tagged `v<version>`. There is no registry-hosted JSON manifest for
// binaries (the `/v1/releases/...` tree is catalog metadata only), so the only
// machine-readable release manifest is GitHub's Releases API. CheckForUpdate
// reads it, resolves the target asset's download URL, and fetches the
// checksum listing asset. Both URLs are injectable for mirrors and tests.
//
// # Integrity limits
//
// The checksum travels in the same release as the binary. It therefore proves
// the download was not corrupted in transit; it does not prove the release
// itself is authentic. Signed releases are the missing piece (see SECURITY.md).
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/resolver"
	"github.com/sarv-projects/litespm/internal/trust"
)

const (
	// DefaultReleaseRepository is the GitHub repository that hosts the release
	// binaries and their SHA256SUMS.txt listing.
	DefaultReleaseRepository = "sarv-projects/LiteSPM"

	// DefaultReleaseManifestURL is the release manifest consulted when the
	// caller does not supply one. It is the GitHub Releases "latest" endpoint,
	// which returns the tag, publish time and asset list for the newest
	// non-draft release.
	DefaultReleaseManifestURL = "https://api.github.com/repos/" + DefaultReleaseRepository + "/releases/latest"

	// checksumManifestAsset is the asset name published by build-release.sh.
	checksumManifestAsset = "SHA256SUMS.txt"

	// defaultManifestLimit bounds the release manifest read (4 MiB).
	defaultManifestLimit = 4 << 20
	// defaultChecksumLimit bounds the SHA256SUMS.txt read (1 MiB).
	defaultChecksumLimit = 1 << 20

	// bootCheckTimeout caps the post-replacement `--version` probe so a broken
	// binary can never hang the updater indefinitely.
	bootCheckTimeout = 20 * time.Second
)

// Updater manages checking for and safely applying binary updates.
type Updater struct {
	client      *http.Client
	manifestURL string

	// maxManifestBytes and maxChecksumBytes bound remote reads. They are never
	// unbounded: a hostile or misconfigured origin must not be able to exhaust
	// memory.
	maxManifestBytes int64
	maxChecksumBytes int64

	// bootCheck verifies that a replaced binary still executes. It defaults to
	// VerifySelfBoot and is overridable in tests (unexported: no public API
	// commitment).
	bootCheck func(context.Context, string) error
}

// NewUpdater creates a new binary Updater instance.
//
// manifestURL is the full HTTPS URL of the release manifest. An empty value
// selects DefaultReleaseManifestURL. This parameter used to be interpreted as
// the catalog registry origin; it is now a manifest URL, so callers that
// previously passed config.DefaultRegistryURL must pass the default (or an
// explicit manifest URL) instead.
func NewUpdater(manifestURL string) *Updater {
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = DefaultReleaseManifestURL
	}
	return &Updater{
		client:           &http.Client{Timeout: 30 * time.Second},
		manifestURL:      manifestURL,
		maxManifestBytes: defaultManifestLimit,
		maxChecksumBytes: defaultChecksumLimit,
		bootCheck:        VerifySelfBoot,
	}
}

// TargetBinaryName returns canonical target binary name for current platform.
func TargetBinaryName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("litespm-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// releaseManifest is the subset of a GitHub Releases API document this package
// depends on. Unknown fields are ignored.
type releaseManifest struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// CheckForUpdate queries the release manifest for a newer binary release.
// It never advertises a downgrade: an update is available only when the
// manifest version is strictly greater than currentVersion.
func (u *Updater) CheckForUpdate(ctx context.Context, currentVersion string) (*UpdateStatus, *ReleaseInfo, error) {
	return u.CheckForUpdateWithOptions(ctx, currentVersion, false)
}

// CheckForUpdateWithOptions is CheckForUpdate with an explicit force switch.
// A non-forced check rejects downgrades and reinstalls; forcing permits them
// (for example to repair a corrupt install). The version guard lives here
// because this is where both versions are known.
func (u *Updater) CheckForUpdateWithOptions(ctx context.Context, currentVersion string, force bool) (*UpdateStatus, *ReleaseInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cur, err := resolver.ParseVersion(currentVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid current version %q: %w", currentVersion, err)
	}

	body, err := u.fetch(ctx, u.manifestURL, u.maxManifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch release manifest: %w", err)
	}

	var manifest releaseManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, nil, fmt.Errorf("invalid release manifest from %s: %w", u.manifestURL, err)
	}

	tag := strings.TrimSpace(manifest.TagName)
	if tag == "" {
		return nil, nil, fmt.Errorf("release manifest from %s publishes no tag_name", u.manifestURL)
	}
	latest, err := resolver.ParseVersion(tag)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid release version %q in manifest: %w", tag, err)
	}

	// Monotonicity: a normal check offers only strictly newer releases.
	// Forcing allows the same or an older version (repair / explicit rollback).
	available := force || latest.Compare(cur) > 0

	latestVersion := normalizedVersion(latest)
	info := &ReleaseInfo{
		Version:         latestVersion,
		ReleaseURL:      manifest.HTMLURL,
		PublishedAt:     manifest.PublishedAt,
		ChecksumsSHA256: map[string]string{},
		DownloadURLs:    map[string]string{},
		BundleURLs:      map[string]string{},
	}
	if info.ReleaseURL == "" {
		info.ReleaseURL = fmt.Sprintf("https://github.com/%s/releases/tag/v%s", DefaultReleaseRepository, latestVersion)
	}

	targetBin := TargetBinaryName()
	var sumsURL string
	for _, asset := range manifest.Assets {
		switch asset.Name {
		case targetBin:
			if err := validateHTTPSURL(asset.BrowserDownloadURL); err != nil {
				return nil, nil, fmt.Errorf("release asset %q has an insecure download URL: %w", asset.Name, err)
			}
			info.DownloadURLs[targetBin] = asset.BrowserDownloadURL
		case checksumManifestAsset:
			sumsURL = asset.BrowserDownloadURL
		default:
			// `<asset>.sigstore.json` — the keyless signature bundle
			// release.yml publishes beside each artifact it signs.
			if strings.HasSuffix(asset.Name, trust.DefaultBundleSuffix) {
				if err := validateHTTPSURL(asset.BrowserDownloadURL); err != nil {
					return nil, nil, fmt.Errorf("release asset %q has an insecure download URL: %w", asset.Name, err)
				}
				info.BundleURLs[asset.Name] = asset.BrowserDownloadURL
			}
		}
	}

	if available && info.DownloadURLs[targetBin] == "" {
		return nil, nil, fmt.Errorf("release %s publishes no %s binary for %s/%s", latestVersion, targetBin, runtime.GOOS, runtime.GOARCH)
	}

	if available {
		if sumsURL == "" {
			return nil, nil, fmt.Errorf("release %s publishes no %s; refusing an unverifiable self-update", latestVersion, checksumManifestAsset)
		}
		if err := validateHTTPSURL(sumsURL); err != nil {
			return nil, nil, fmt.Errorf("release %s has an insecure %s URL: %w", latestVersion, checksumManifestAsset, err)
		}
		sumsBody, err := u.fetch(ctx, sumsURL, u.maxChecksumBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch %s for release %s: %w", checksumManifestAsset, latestVersion, err)
		}
		for name, sum := range parseSHA256SUMS(sumsBody) {
			info.ChecksumsSHA256[name] = sum
		}
		if info.ChecksumsSHA256[targetBin] == "" {
			return nil, nil, fmt.Errorf("release %s publishes no SHA-256 checksum for %s; refusing an unverifiable self-update", latestVersion, targetBin)
		}
	}

	status := &UpdateStatus{
		CurrentVersion:  normalizedVersion(cur),
		LatestVersion:   latestVersion,
		UpdateAvailable: available,
	}
	return status, info, nil
}

// fetch performs a bounded, HTTPS-only GET and returns the body.
func (u *Updater) fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := validateHTTPSURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "litespm-updater/1")

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", rawURL, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response from %s exceeds the %d byte limit", rawURL, limit)
	}
	return data, nil
}

// FetchAsset downloads one release asset (a signature bundle, for example)
// under the same bounded, HTTPS-only rules as every other read this package
// performs.
func (u *Updater) FetchAsset(ctx context.Context, rawURL string) ([]byte, error) {
	return u.fetch(ctx, rawURL, u.maxChecksumBytes)
}

// validateHTTPSURL refuses any URL that is not an https URL with a host.
// This is the single choke point for every remote target the updater touches.
func validateHTTPSURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("empty URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("refusing non-https URL %q: scheme must be https", raw)
	}
	if parsed.Host == "" {
		return fmt.Errorf("refusing URL %q with no host", raw)
	}
	return nil
}

// parseSHA256SUMS parses sha256sum/shasum output into a filename -> digest
// map. Lines that do not look like a checksum entry are skipped rather than
// rejected, so a trailing note in the file cannot break verification.
func parseSHA256SUMS(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = sum
		if base := filepath.Base(name); base != name {
			out[base] = sum
		}
	}
	return out
}

// ApplyUpdate installs a downloaded replacement binary in-place.
//
// Verification policy: FAIL CLOSED.
//
// The expected SHA-256 is mandatory. An empty value is refused with an explicit
// reason (the release manifest published no checksum for this platform) rather
// than silently skipping verification. The old code did the opposite and even
// carried a hard-coded bypass string; both are gone.
//
// Replacement is atomic on unix: the new binary is staged in a freshly created
// private directory beside the target, fsynced, chmod 0755, and renamed over the
// target in a single step. Staging beside the target (rather than in the shared
// staging directory) is what makes the rename same-filesystem and therefore
// atomic; it also removes the old fixed `litespm.new` name from the shared
// directory, which was vulnerable to a symlink/TOCTOU pre-placement.
//
// stagingDir is retained for API compatibility and is used only as a fallback
// when a private directory cannot be created beside the target. A cross-device
// rename in that fallback fails closed rather than degrading to a non-atomic
// copy.
//
// On Windows a live executable cannot be overwritten, so the documented
// two-step is used: the in-use binary is renamed to `.old`, the new binary is
// moved into place, and `.old` is removed only after verification. The residual
// limitation is that the two renames are not a single atomic operation, so a
// process start during the swap can observe a missing path.
//
// After the swap the replacement is re-hashed and boot-checked. Any failure
// restores the backup automatically.
func (u *Updater) ApplyUpdate(ctx context.Context, newBinaryBytes []byte, expectedSHA256, targetBinaryPath, stagingDir string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(newBinaryBytes) == 0 {
		return errors.New("empty replacement binary payload")
	}

	expected := strings.TrimSpace(expectedSHA256)
	if expected == "" {
		return errors.New("refusing to apply an update with no expected SHA-256 checksum: " +
			"the release manifest did not publish one for this platform")
	}

	actual := sha256Hex(newBinaryBytes)
	if !strings.EqualFold(actual, expected) {
		return domain.ErrChecksumMismatch(expected, actual)
	}

	if strings.TrimSpace(targetBinaryPath) == "" {
		return errors.New("target binary path is empty")
	}
	targetInfo, statErr := os.Lstat(targetBinaryPath)
	if statErr != nil {
		return fmt.Errorf("target binary %q is not accessible: %w", targetBinaryPath, statErr)
	}
	if targetInfo.IsDir() {
		return fmt.Errorf("target binary %q is a directory", targetBinaryPath)
	}

	// Stage beside the target so the final rename is same-filesystem and
	// therefore atomic. The directory is brand new and mode 0700, so the
	// O_EXCL file inside it can never collide with (or follow) a pre-existing
	// symlink.
	targetDir := filepath.Dir(targetBinaryPath)
	tmpDir, err := os.MkdirTemp(targetDir, ".litespm-update-")
	if err != nil && strings.TrimSpace(stagingDir) != "" {
		tmpDir, err = os.MkdirTemp(stagingDir, ".litespm-update-")
	}
	if err != nil {
		return fmt.Errorf("failed to create private staging directory beside target: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	stagedPath := filepath.Join(tmpDir, "litespm.new")
	if err := writeExclusiveFile(stagedPath, newBinaryBytes, 0o755); err != nil {
		return fmt.Errorf("failed to stage replacement binary: %w", err)
	}
	_ = syncDir(tmpDir)

	backupPath, err := backupTarget(targetBinaryPath, tmpDir)
	if err != nil {
		return err
	}

	if err := swapBinary(stagedPath, targetBinaryPath, backupPath); err != nil {
		if restoreErr := restoreBackup(backupPath, targetBinaryPath); restoreErr != nil {
			return fmt.Errorf("%w (additionally failed to restore the original binary: %v)", err, restoreErr)
		}
		return err
	}
	_ = syncDir(targetDir)

	if verifyErr := u.verifyReplaced(ctx, targetBinaryPath, expected); verifyErr != nil {
		if restoreErr := restoreBackup(backupPath, targetBinaryPath); restoreErr != nil {
			return fmt.Errorf("replacement verification failed: %w (additionally failed to restore the original binary: %v)", verifyErr, restoreErr)
		}
		return fmt.Errorf("replacement verification failed; original binary restored: %w", verifyErr)
	}

	// Verified: the backup is no longer needed.
	_ = os.Remove(backupPath)
	return nil
}

// verifyReplaced re-hashes the installed binary and probes that it boots.
func (u *Updater) verifyReplaced(ctx context.Context, targetPath, expected string) error {
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("failed to read replaced binary: %w", err)
	}
	if actual := sha256Hex(data); !strings.EqualFold(actual, expected) {
		return fmt.Errorf("post-replacement SHA-256 mismatch: expected %s, got %s", expected, actual)
	}

	check := u.bootCheck
	if check == nil {
		check = VerifySelfBoot
	}
	if err := check(ctx, targetPath); err != nil {
		return fmt.Errorf("replaced binary failed boot verification: %w", err)
	}
	return nil
}

// writeExclusiveFile creates path with O_EXCL (never following or overwriting
// an existing file/symlink), writes data, fsyncs it, and asserts the result is
// a regular file.
func writeExclusiveFile(path string, data []byte, mode os.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}

	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return fmt.Errorf("staged path %q is not a regular file", path)
	}
	return nil
}

// backupTarget preserves the current binary so it can be restored on failure.
// On unix it is a hard link (falling back to a copy); on Windows the in-use
// binary is renamed aside because it cannot be opened for a copy reliably.
func backupTarget(targetPath, tmpDir string) (string, error) {
	if runtime.GOOS == "windows" {
		old := targetPath + ".old"
		_ = os.Remove(old)
		if err := os.Rename(targetPath, old); err != nil {
			return "", fmt.Errorf("failed to rename in-use binary aside: %w", err)
		}
		return old, nil
	}

	backup := filepath.Join(tmpDir, "original")
	if err := os.Link(targetPath, backup); err == nil {
		return backup, nil
	}
	if err := copyFile(targetPath, backup); err != nil {
		return "", fmt.Errorf("failed to back up current binary: %w", err)
	}
	return backup, nil
}

// swapBinary moves the staged binary over the target.
func swapBinary(stagedPath, targetPath, backupPath string) error {
	if runtime.GOOS == "windows" {
		// backupTarget already moved the in-use binary aside.
		if err := os.Rename(stagedPath, targetPath); err != nil {
			return fmt.Errorf("failed to move new binary into place: %w", err)
		}
		return nil
	}
	// Same-directory rename is atomic on POSIX.
	if err := os.Rename(stagedPath, targetPath); err != nil {
		return fmt.Errorf("failed to atomically replace target binary: %w", err)
	}
	return nil
}

// restoreBackup puts the preserved binary back at targetPath.
func restoreBackup(backupPath, targetPath string) error {
	if runtime.GOOS == "windows" {
		_ = os.Remove(targetPath)
	}
	if err := os.Rename(backupPath, targetPath); err != nil {
		return err
	}
	return nil
}

// VerifySelfBoot tests that a binary can execute `--version` and reports the
// expected product banner. It is the default post-replacement check and is
// context-cancellable with a hard timeout.
func VerifySelfBoot(ctx context.Context, binaryPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, bootCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return fmt.Errorf("binary boot verification failed: %w (output: %s)", err, text)
	}
	if !strings.Contains(text, "LiteSPM") {
		return fmt.Errorf("unexpected binary boot output: %s", text)
	}
	return nil
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// copyFile copies src to dst with mode 0755.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// syncDir best-effort fsyncs a directory so a rename survives a crash. It is
// ignored on platforms where a directory cannot be opened for sync.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// normalizedVersion renders v without the leading "v" that release tags carry.
func normalizedVersion(v resolver.Version) string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		s += "-" + v.PreRelease
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}
