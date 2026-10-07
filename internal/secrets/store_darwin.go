//go:build darwin

package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

func getOrGenerateDarwinMasterKey() ([]byte, error) {
	if _, err := exec.LookPath("/usr/bin/security"); err != nil {
		return nil, domain.ErrAuthVaultUnavailable("macOS security CLI not found")
	}

	cmdLookup := exec.Command("/usr/bin/security", "find-generic-password", "-s", "litespm", "-a", "master-key", "-w")
	out, err := cmdLookup.Output()
	lookupStr := strings.TrimSpace(string(out))
	if err == nil && len(lookupStr) > 0 {
		key, decodeErr := hex.DecodeString(lookupStr)
		if decodeErr == nil && len(key) == 32 {
			return key, nil
		}
	}

	// Generate new 32-byte key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random vault key: %w", err)
	}

	hexKey := hex.EncodeToString(key)
	// E5: never pass the key as an argv word (visible in `ps`). Like the
	// Linux backend, pipe it on stdin: `security ... -w` with no value reads
	// the password from stdin.
	cmdStore := exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", "litespm", "-a", "master-key", "-w")
	cmdStore.Stdin = bytes.NewReader([]byte(hexKey))
	if err := cmdStore.Run(); err != nil {
		return nil, domain.ErrAuthVaultUnavailable(fmt.Sprintf("failed to store vault master key in macOS Keychain: %v", err))
	}

	return key, nil
}

func newPlatformSecretStore() (SecretStore, error) {
	vaultPath := DefaultVaultPath()
	key, err := getOrGenerateDarwinMasterKey()
	if err != nil {
		return nil, err
	}
	return NewFileEncryptedSecretStoreWithKey(vaultPath, key)
}
