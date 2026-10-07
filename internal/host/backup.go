package host

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CreateAtomicBackup writes a byte-for-byte copy of originalPath into backupDir before any edits.
func CreateAtomicBackup(originalPath, backupDir, hostID string) (string, error) {
	data, err := os.ReadFile(originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // Nothing to back up if file does not exist yet
		}
		return "", fmt.Errorf("failed to read original config for backup %s: %w", originalPath, err)
	}

	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup dir %s: %w", backupDir, err)
	}

	hash := sha256.Sum256(data)
	shortHash := hex.EncodeToString(hash[:])[:8]
	timestamp := time.Now().UTC().Format("20060102_150405")

	// Sanitize hostID to avoid directory traversal
	cleanHostID := filepath.Base(filepath.Clean(hostID))
	if cleanHostID == "." || cleanHostID == "/" || cleanHostID == "\\" {
		cleanHostID = "host"
	}

	backupFileName := fmt.Sprintf("%s_%s_%s.bak", cleanHostID, timestamp, shortHash)
	backupPath := filepath.Join(backupDir, backupFileName)

	return backupPath, AtomicWriteFile(backupPath, data, 0600)
}

// AtomicWriteFile writes data to targetPath atomically via a temporary file.
//
// Two properties matter because the target is someone else's config:
//
//   - Symlinks are preserved, not severed. A plain rename over a symlink
//     replaces the link with a regular file, silently breaking the user's
//     dotfile management (stow, chezmoi, a hand-made link). When the final
//     path component is a symlink it is resolved and the write goes to the
//     link's target, leaving the link itself intact.
//   - The existing file mode is preserved. Forcing 0600 rewrote a file the
//     user deliberately shared (or a test fixture at 0644) into a private
//     one; a new file still gets 0600.
func AtomicWriteFile(targetPath string, data []byte, perm os.FileMode) error {
	resolved := targetPath
	if fi, err := os.Lstat(targetPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(targetPath)
		if err != nil {
			return fmt.Errorf("resolving symlink %s: %w", targetPath, err)
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(targetPath), link)
		}
		resolved = link
	}

	effective := perm
	if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() {
		effective = fi.Mode().Perm()
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// O_EXCL so a leftover temp file from a crashed run is never silently
	// truncated and reused; retry with a fresh name on collision.
	var tmp *os.File
	var tmpName string
	for attempt := 0; attempt < 5; attempt++ {
		tmpName = filepath.Join(dir, fmt.Sprintf(".tmp_%d_%d_%s", os.Getpid(), time.Now().UnixNano(), filepath.Base(resolved)))
		f, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, effective)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return err
		}
		tmp = f
		break
	}
	if tmp == nil {
		return fmt.Errorf("could not create a unique temp file in %s", dir)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, effective); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Chmod(resolved, effective)
}
