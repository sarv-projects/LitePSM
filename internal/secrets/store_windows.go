//go:build windows

package secrets

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/sarv-projects/litespm/internal/domain"
	"golang.org/x/sys/windows"
)

func getOrGenerateWindowsMasterKey(vaultPath string) ([]byte, error) {
	keyFile := filepath.Join(filepath.Dir(vaultPath), "master.key")

	// If key file exists, decrypt with DPAPI
	if data, err := os.ReadFile(keyFile); err == nil && len(data) > 0 {
		var inBlob windows.DataBlob
		inBlob.Size = uint32(len(data))
		inBlob.Data = &data[0]

		var outBlob windows.DataBlob
		err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, 0, &outBlob)
		if err != nil {
			return nil, domain.ErrAuthVaultUnavailable(fmt.Sprintf("failed to decrypt master key via Windows DPAPI: %v", err))
		}
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

		key := make([]byte, outBlob.Size)
		copy(key, unsafe.Slice(outBlob.Data, outBlob.Size))
		if len(key) == 32 {
			return key, nil
		}
	}

	// Generate a new 32-byte random key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}

	var inBlob windows.DataBlob
	inBlob.Size = uint32(len(key))
	inBlob.Data = &key[0]

	desc, _ := windows.UTF16PtrFromString("LiteSPM Vault Master Key")
	var outBlob windows.DataBlob
	err := windows.CryptProtectData(&inBlob, desc, nil, 0, nil, 0, &outBlob)
	if err != nil {
		return nil, domain.ErrAuthVaultUnavailable(fmt.Sprintf("failed to protect master key via Windows DPAPI: %v", err))
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	encryptedBytes := make([]byte, outBlob.Size)
	copy(encryptedBytes, unsafe.Slice(outBlob.Data, outBlob.Size))

	if err := os.MkdirAll(filepath.Dir(keyFile), 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory for master key: %w", err)
	}

	if err := os.WriteFile(keyFile, encryptedBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to save encrypted master key: %w", err)
	}

	return key, nil
}

func newPlatformSecretStore() (SecretStore, error) {
	vaultPath := DefaultVaultPath()
	key, err := getOrGenerateWindowsMasterKey(vaultPath)
	if err != nil {
		return nil, err
	}
	return NewFileEncryptedSecretStoreWithKey(vaultPath, key)
}
