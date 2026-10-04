//go:build linux

package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
)

func getOrGenerateLinuxMasterKey() ([]byte, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, domain.ErrAuthVaultUnavailable("FreeDesktop Secret Service (secret-tool) not available in PATH")
	}

	// Try looking up existing master key
	cmdLookup := exec.Command("secret-tool", "lookup", "service", "litepsm", "account", "master-key")
	out, err := cmdLookup.Output()
	lookupStr := strings.TrimSpace(string(out))
	if err == nil && len(lookupStr) > 0 {
		key, decodeErr := hex.DecodeString(lookupStr)
		if decodeErr == nil && len(key) == 32 {
			return key, nil
		}
	}

	// Generate a new random 32-byte master key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random vault key: %w", err)
	}

	hexKey := hex.EncodeToString(key)
	cmdStore := exec.Command("secret-tool", "store", "--label=LitePSM Vault Key", "service", "litepsm", "account", "master-key")
	cmdStore.Stdin = bytes.NewReader([]byte(hexKey))
	if err := cmdStore.Run(); err != nil {
		return nil, domain.ErrAuthVaultUnavailable(fmt.Sprintf("failed to store vault master key via secret-tool: %v", err))
	}

	return key, nil
}

func newPlatformSecretStore() (SecretStore, error) {
	vaultPath := DefaultVaultPath()
	key, err := getOrGenerateLinuxMasterKey()
	if err != nil {
		return nil, err
	}
	return NewFileEncryptedSecretStoreWithKey(vaultPath, key)
}
