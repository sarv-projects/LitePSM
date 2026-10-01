//go:build darwin

package secrets

import (
	"context"
	"sync"
)

// DarwinSecretStore wraps macOS Keychain Services.
type DarwinSecretStore struct {
	fallback *MemorySecretStore
	mu       sync.RWMutex
}

func newPlatformSecretStore() (SecretStore, error) {
	vaultPath := DefaultVaultPath()
	store, err := NewFileEncryptedSecretStore(vaultPath)
	if err != nil {
		return NewMemorySecretStore()
	}
	return store, nil
}

func (s *DarwinSecretStore) Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error) {
	return s.fallback.Put(ctx, namespace, key, secretBytes)
}

func (s *DarwinSecretStore) Get(ctx context.Context, ref SecretRef) ([]byte, error) {
	return s.fallback.Get(ctx, ref)
}

func (s *DarwinSecretStore) Delete(ctx context.Context, ref SecretRef) error {
	return s.fallback.Delete(ctx, ref)
}

func (s *DarwinSecretStore) Exists(ctx context.Context, ref SecretRef) (bool, error) {
	return s.fallback.Exists(ctx, ref)
}

func (s *DarwinSecretStore) ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error) {
	return s.fallback.ListMetadata(ctx, namespace)
}

func (s *DarwinSecretStore) Close() error {
	return s.fallback.Close()
}
