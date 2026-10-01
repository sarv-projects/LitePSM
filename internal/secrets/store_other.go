//go:build !windows && !darwin && !linux

package secrets

func newPlatformSecretStore() (SecretStore, error) {
	vaultPath := DefaultVaultPath()
	store, err := NewFileEncryptedSecretStore(vaultPath)
	if err != nil {
		return NewMemorySecretStore()
	}
	return store, nil
}
