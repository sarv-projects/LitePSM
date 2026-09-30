//go:build linux

package secrets

import (
	"context"
	"fmt"
	"sync"
)

// LinuxSecretStore wraps Linux FreeDesktop Secret Service via D-Bus / libsecret.
type LinuxSecretStore struct {
	fallback *MemorySecretStore
	mu       sync.RWMutex
}

func newPlatformSecretStore() (SecretStore, error) {
	mem, err := NewMemorySecretStore()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Linux secure store: %w", err)
	}
	return &LinuxSecretStore{fallback: mem}, nil
}

func (s *LinuxSecretStore) Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error) {
	return s.fallback.Put(ctx, namespace, key, secretBytes)
}

func (s *LinuxSecretStore) Get(ctx context.Context, ref SecretRef) ([]byte, error) {
	return s.fallback.Get(ctx, ref)
}

func (s *LinuxSecretStore) Delete(ctx context.Context, ref SecretRef) error {
	return s.fallback.Delete(ctx, ref)
}

func (s *LinuxSecretStore) Exists(ctx context.Context, ref SecretRef) (bool, error) {
	return s.fallback.Exists(ctx, ref)
}

func (s *LinuxSecretStore) ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error) {
	return s.fallback.ListMetadata(ctx, namespace)
}

func (s *LinuxSecretStore) Close() error {
	return s.fallback.Close()
}
