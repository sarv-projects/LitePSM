package host

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/fslock"
)

const hostConfigWriteLockSuffix = ".litespm-config.lock"

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
	return atomicWriteFile(targetPath, data, perm, nil, false, false)
}

// AtomicWriteFileIfUnchanged stages data and replaces targetPath only if its
// current bytes still match the snapshot used to prepare the update. This is an
// optimistic compare-before-replace guard for host config read/merge/write;
// writers that do not coordinate through LiteSPM can still race the final
// filesystem compare and rename.
func AtomicWriteFileIfUnchanged(targetPath string, data []byte, perm os.FileMode, expected []byte, expectedExists bool) error {
	return atomicWriteFile(targetPath, data, perm, expected, expectedExists, true)
}

// RollbackConfigWrite restores a config pre-image only while the file still
// contains the exact post-image written by LiteSPM. If a user or another tool
// edited the config after the install write, rollback refuses to erase that
// change. An empty backupPath means the install created the config, so the
// verified post-image is removed instead.
func RollbackConfigWrite(targetPath, backupPath, expectedDigest string) error {
	if expectedDigest == "" {
		return fmt.Errorf("refusing to roll back %s without a post-write digest", targetPath)
	}
	lock, err := fslock.Acquire(targetPath+hostConfigWriteLockSuffix, fslock.Options{Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("lock host config %s for rollback: %w", targetPath, err)
	}
	defer func() { _ = lock.Release() }()
	return rollbackConfigWriteLocked(targetPath, backupPath, expectedDigest)
}

// rollbackConfigWriteLocked is for a caller that already holds the per-config
// LiteSPM lock, such as InstallServerEntry's local failure compensation.
func rollbackConfigWriteLocked(targetPath, backupPath, expectedDigest string) error {
	resolved, err := resolveAtomicTarget(targetPath)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read host config %s for rollback: %w", targetPath, err)
	}
	if domain.ComputeBytesDigest(current) != expectedDigest {
		return fmt.Errorf("host config %s changed after LiteSPM wrote it; refusing to overwrite the newer contents", targetPath)
	}

	if backupPath == "" {
		// Re-resolve and recheck immediately before remove. The LiteSPM lock
		// excludes our other writers; this final comparison also catches most
		// edits by tools that do not honor it.
		currentTarget, err := resolveAtomicTarget(targetPath)
		if err != nil {
			return err
		}
		if filepath.Clean(currentTarget) != filepath.Clean(resolved) {
			return fmt.Errorf("host config target %s changed during rollback", targetPath)
		}
		latest, err := os.ReadFile(resolved)
		if err != nil || domain.ComputeBytesDigest(latest) != expectedDigest {
			return fmt.Errorf("host config %s changed during rollback; refusing to remove it", targetPath)
		}
		if err := os.Remove(resolved); err != nil {
			return fmt.Errorf("remove config created by this install %s: %w", targetPath, err)
		}
		return nil
	}

	preimage, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read pre-install backup %s: %w", backupPath, err)
	}
	return AtomicWriteFileIfUnchanged(targetPath, preimage, 0600, current, true)
}

func resolveAtomicTarget(targetPath string) (string, error) {
	fi, err := os.Lstat(targetPath)
	if err != nil {
		return "", fmt.Errorf("inspect host config target %s: %w", targetPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return targetPath, nil
	}
	link, err := os.Readlink(targetPath)
	if err != nil {
		return "", fmt.Errorf("resolve host config symlink %s: %w", targetPath, err)
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(targetPath), link)
	}
	return link, nil
}

func atomicWriteFile(targetPath string, data []byte, perm os.FileMode, expected []byte, expectedExists, compare bool) error {
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
	if compare {
		currentResolved := targetPath
		if fi, err := os.Lstat(targetPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(targetPath)
			if err != nil {
				_ = os.Remove(tmpName)
				return fmt.Errorf("resolving symlink %s before replacement: %w", targetPath, err)
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(targetPath), link)
			}
			currentResolved = link
		} else if err != nil && !os.IsNotExist(err) {
			_ = os.Remove(tmpName)
			return fmt.Errorf("checking target %s before replacement: %w", targetPath, err)
		}
		if filepath.Clean(currentResolved) != filepath.Clean(resolved) {
			_ = os.Remove(tmpName)
			return fmt.Errorf("target %s changed while preparing the write", targetPath)
		}
		current, err := os.ReadFile(resolved)
		if expectedExists {
			if err != nil || !bytes.Equal(current, expected) {
				_ = os.Remove(tmpName)
				return fmt.Errorf("target %s changed while preparing the write", targetPath)
			}
		} else if err == nil || !os.IsNotExist(err) {
			_ = os.Remove(tmpName)
			if err != nil {
				return fmt.Errorf("checking target %s before replacement: %w", targetPath, err)
			}
			return fmt.Errorf("target %s appeared while preparing the write", targetPath)
		}
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Chmod(resolved, effective)
}
