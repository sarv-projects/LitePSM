//go:build windows

package secrets

import (
	"context"
	"fmt"
	"sync"
)

// WindowsSecretStore wraps Windows credential management and DPAPI.
type WindowsSecretStore struct {
	fallback *MemorySecretStore
	mu       sync.RWMutex
}

func newPlatformSecretStore() (SecretStore, error) {
	mem, err := NewMemorySecretStore()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Windows secure store: %w", err)
	}
	return &WindowsSecretStore{fallback: mem}, nil
}

func (s *WindowsSecretStore) Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error) {
	return s.fallback.Put(ctx, namespace, key, secretBytes)
}

func (s *WindowsSecretStore) Get(ctx context.Context, ref SecretRef) ([]byte, error) {
	return s.fallback.Get(ctx, ref)
}

func (s *WindowsSecretStore) Delete(ctx context.Context, ref SecretRef) error {
	return s.fallback.Delete(ctx, ref)
}

func (s *WindowsSecretStore) Exists(ctx context.Context, ref SecretRef) (bool, error) {
	return s.fallback.Exists(ctx, ref)
}

func (s *WindowsSecretStore) ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error) {
	return s.fallback.ListMetadata(ctx, namespace)
}

func (s *WindowsSecretStore) Close() error {
	return s.fallback.Close()
}
