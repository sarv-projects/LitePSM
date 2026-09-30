package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

type memoryEntry struct {
	encryptedData []byte
	nonce         []byte
	metadata      SecretMetadata
}

// MemorySecretStore provides an in-memory encrypted secret vault using AES-256-GCM.
type MemorySecretStore struct {
	key     []byte
	gcm     cipher.AEAD
	entries map[string]memoryEntry
	mu      sync.RWMutex
}

// NewMemorySecretStore initializes an AES-256-GCM encrypted in-memory vault.
func NewMemorySecretStore() (*MemorySecretStore, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate random secret key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &MemorySecretStore{
		key:     key,
		gcm:     gcm,
		entries: make(map[string]memoryEntry),
	}, nil
}

func (s *MemorySecretStore) Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref := NewSecretRef(namespace, key)
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := s.gcm.Seal(nil, nonce, secretBytes, []byte(ref.URI))

	now := time.Now().UTC()
	existing, ok := s.entries[ref.URI]
	createdAt := now
	if ok {
		createdAt = existing.metadata.CreatedAt
	}

	s.entries[ref.URI] = memoryEntry{
		encryptedData: ciphertext,
		nonce:         nonce,
		metadata: SecretMetadata{
			URI:       ref.URI,
			Namespace: namespace,
			Key:       key,
			CreatedAt: createdAt,
			UpdatedAt: now,
		},
	}

	return ref, nil
}

func (s *MemorySecretStore) Get(ctx context.Context, ref SecretRef) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[ref.URI]
	if !ok {
		return nil, domain.ErrNotFound("secret", ref.URI)
	}

	plaintext, err := s.gcm.Open(nil, entry.nonce, entry.encryptedData, []byte(ref.URI))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secret %s: %w", ref.URI, err)
	}

	return plaintext, nil
}

func (s *MemorySecretStore) Delete(ctx context.Context, ref SecretRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[ref.URI]; !ok {
		return domain.ErrNotFound("secret", ref.URI)
	}

	delete(s.entries, ref.URI)
	return nil
}

func (s *MemorySecretStore) Exists(ctx context.Context, ref SecretRef) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.entries[ref.URI]
	return ok, nil
}

func (s *MemorySecretStore) ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []SecretMetadata
	for _, entry := range s.entries {
		if namespace == "" || entry.metadata.Namespace == namespace {
			result = append(result, entry.metadata)
		}
	}
	return result, nil
}

func (s *MemorySecretStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Zeroize memory
	for i := range s.key {
		s.key[i] = 0
	}
	s.entries = make(map[string]memoryEntry)
	return nil
}
