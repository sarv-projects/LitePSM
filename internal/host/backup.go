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
func AtomicWriteFile(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".tmp_%d_%s", time.Now().UnixNano(), filepath.Base(targetPath)))
	if err := os.WriteFile(tmpFile, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmpFile, perm); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	if err := os.Rename(tmpFile, targetPath); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	return os.Chmod(targetPath, perm)
}
