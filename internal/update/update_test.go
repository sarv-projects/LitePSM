package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdater_CheckForUpdate(t *testing.T) {
	ctx := context.Background()
	u := NewUpdater("https://registry.litepsm.dev")

	status, info, err := u.CheckForUpdate(ctx, "0.0.9")
	if err != nil {
		t.Fatalf("CheckForUpdate failed: %v", err)
	}

	if !status.UpdateAvailable {
		t.Errorf("expected updateAvailable=true for current version 0.0.9 vs latest 0.1.0")
	}
	if info == nil || info.Version != "0.1.0" {
		t.Errorf("unexpected release info: %+v", info)
	}

	// Test when already on current version
	statusCurrent, _, err := u.CheckForUpdate(ctx, "0.1.0")
	if err != nil {
		t.Fatalf("CheckForUpdate failed: %v", err)
	}
	if statusCurrent.UpdateAvailable {
		t.Errorf("expected updateAvailable=false for identical version")
	}
}

func TestUpdater_ApplyUpdateAndIntegrityCheck(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	u := NewUpdater("")

	targetPath := filepath.Join(tempDir, "bin", "litepsm_target")
	_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
	_ = os.WriteFile(targetPath, []byte("old-binary-content"), 0755)

	newPayload := []byte("#!/bin/sh\necho LitePSM v0.1.0\n")
	hash := sha256.Sum256(newPayload)
	expectedSHA := hex.EncodeToString(hash[:])

	stagingDir := filepath.Join(tempDir, "staging")

	// 1. Valid update application
	err := u.ApplyUpdate(ctx, newPayload, expectedSHA, targetPath, stagingDir)
	if err != nil {
		t.Fatalf("ApplyUpdate failed: %v", err)
	}

	updatedBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read updated target binary: %v", err)
	}
	if string(updatedBytes) != string(newPayload) {
		t.Errorf("binary content mismatch after update")
	}

	// 2. Corrupt checksum rejected
	corruptErr := u.ApplyUpdate(ctx, newPayload, "badchecksum000000000000000000000000000000000000000000000000000000", targetPath, stagingDir)
	if corruptErr == nil {
		t.Errorf("expected checksum mismatch error, got nil")
	}
}

func TestTargetBinaryName(t *testing.T) {
	name := TargetBinaryName()
	if name == "" {
		t.Fatalf("expected non-empty target binary name")
	}
}
