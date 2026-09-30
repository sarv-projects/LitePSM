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

	backupFileName := fmt.Sprintf("%s_%s_%s.bak", hostID, timestamp, shortHash)
	backupPath := filepath.Join(backupDir, backupFileName)

	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write backup file %s: %w", backupPath, err)
	}

	return backupPath, nil
}
