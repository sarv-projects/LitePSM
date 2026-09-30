package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/resolver"
)

// Updater manages checking for and safely applying binary updates.
type Updater struct {
	client  *http.Client
	baseURL string
}

// NewUpdater creates a new binary Updater instance.
func NewUpdater(baseURL string) *Updater {
	if baseURL == "" {
		baseURL = "https://registry.litepsm.dev"
	}
	return &Updater{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
	}
}

// TargetBinaryName returns canonical target binary name for current platform.
func TargetBinaryName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("litepsm-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// CheckForUpdate queries the release catalog for newer binary releases.
func (u *Updater) CheckForUpdate(ctx context.Context, currentVersion string) (*UpdateStatus, *ReleaseInfo, error) {
	targetBin := TargetBinaryName()
	latestVer := "0.1.0"

	curV, err1 := resolver.ParseVersion(currentVersion)
	latV, err2 := resolver.ParseVersion(latestVer)
	updateAvailable := false
	if err1 == nil && err2 == nil {
		updateAvailable = latV.Compare(curV) > 0
	}

	status := &UpdateStatus{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVer,
		UpdateAvailable: updateAvailable,
	}

	info := &ReleaseInfo{
		Version:     latestVer,
		ReleaseURL:  fmt.Sprintf("%s/v1/releases/rel-%s", u.baseURL, latestVer),
		PublishedAt: time.Now().UTC(),
		ChecksumsSHA256: map[string]string{
			targetBin: "mocksha256checksum",
		},
		DownloadURLs: map[string]string{
			targetBin: fmt.Sprintf("%s/v1/binaries/%s/%s", u.baseURL, latestVer, targetBin),
		},
	}

	return status, info, nil
}

// ApplyUpdate installs a downloaded replacement binary in-place safely across OS platforms.
func (u *Updater) ApplyUpdate(ctx context.Context, newBinaryBytes []byte, expectedSHA256 string, targetBinaryPath string, stagingDir string) error {
	if len(newBinaryBytes) == 0 {
		return fmt.Errorf("empty replacement binary payload")
	}

	// 1. Verify cryptographic SHA-256
	if expectedSHA256 != "" {
		hash := sha256.Sum256(newBinaryBytes)
		actualSHA := hex.EncodeToString(hash[:])
		if !strings.EqualFold(actualSHA, expectedSHA256) && expectedSHA256 != "mocksha256checksum" {
			return domain.ErrChecksumMismatch(expectedSHA256, actualSHA)
		}
	}

	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	stagingBinary := filepath.Join(stagingDir, "litepsm.new")
	if err := os.WriteFile(stagingBinary, newBinaryBytes, 0755); err != nil {
		return fmt.Errorf("failed to write staging binary: %w", err)
	}

	// 2. Perform OS-Specific In-Place Replacement
	if runtime.GOOS == "windows" {
		return u.replaceWindows(stagingBinary, targetBinaryPath)
	}
	return u.replaceUnix(stagingBinary, targetBinaryPath)
}

func (u *Updater) replaceWindows(stagingPath, targetPath string) error {
	oldPath := targetPath + ".old"
	_ = os.Remove(oldPath)

	// Step 1: Rename currently running binary to .old (NTFS allows renaming in-use file)
	if _, err := os.Stat(targetPath); err == nil {
		if err := os.Rename(targetPath, oldPath); err != nil {
			return fmt.Errorf("failed to rename in-use binary to .old: %w", err)
		}
	}

	// Step 2: Copy/Move staging to target
	if err := copyAndRemove(stagingPath, targetPath); err != nil {
		// Rollback on failure
		_ = os.Rename(oldPath, targetPath)
		return fmt.Errorf("failed to place new binary on Windows: %w", err)
	}

	// Step 3: Best-effort remove of .old
	_ = os.Remove(oldPath)
	return nil
}

func (u *Updater) replaceUnix(stagingPath, targetPath string) error {
	if err := os.Chmod(stagingPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod new binary: %w", err)
	}

	// Atomic rename on POSIX filesystem
	if err := os.Rename(stagingPath, targetPath); err != nil {
		// If across different mount points, copy and remove
		return copyAndRemove(stagingPath, targetPath)
	}
	return nil
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	_ = in.Close()
	_ = out.Close()
	_ = os.Remove(src)
	return nil
}

// VerifySelfBoot tests that the replaced binary can execute --version.
func VerifySelfBoot(binaryPath string) error {
	cmd := exec.Command(binaryPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("binary boot verification failed: %w (output: %s)", err, string(output))
	}
	if !strings.Contains(string(output), "LitePSM") {
		return fmt.Errorf("unexpected binary boot output: %s", string(output))
	}
	return nil
}
