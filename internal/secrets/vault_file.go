package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

type vaultDiskEntry struct {
	EncryptedData []byte         `json:"encrypted_data"`
	Nonce         []byte         `json:"nonce"`
	Metadata      SecretMetadata `json:"metadata"`
}

type serializedVault struct {
	Version int                       `json:"version"`
	Entries map[string]vaultDiskEntry `json:"entries"`
}

// FileEncryptedSecretStore provides a persistent encrypted secret vault on disk using AES-256-GCM.
type FileEncryptedSecretStore struct {
	vaultPath string
	key       []byte
	gcm       cipher.AEAD
	entries   map[string]vaultDiskEntry
	mu        sync.RWMutex
}

// NewFileEncryptedSecretStoreWithKey initializes or loads a persistent AES-256-GCM encrypted vault with an explicit 32-byte key.
func NewFileEncryptedSecretStoreWithKey(vaultPath string, key []byte) (*FileEncryptedSecretStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher init error: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm init error: %w", err)
	}

	store := &FileEncryptedSecretStore{
		vaultPath: vaultPath,
		key:       key,
		gcm:       gcm,
		entries:   make(map[string]vaultDiskEntry),
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load encrypted vault: %w", err)
	}
	return store, nil
}

// NewFileEncryptedSecretStore initializes or loads a persistent AES-256-GCM encrypted vault.
// Note: For native keystore integration, use OpenSecretStore().
func NewFileEncryptedSecretStore(vaultPath string) (*FileEncryptedSecretStore, error) {
	key := deriveVaultKey(vaultPath)
	return NewFileEncryptedSecretStoreWithKey(vaultPath, key)
}

// DefaultVaultPath returns the platform-standard location of the encrypted secrets vault.
func DefaultVaultPath() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			return filepath.Join(appData, "LitePSM", "secrets", "vault.enc")
		}
	} else if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "LitePSM", "secrets", "vault.enc")
		}
	}

	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "litepsm", "secrets", "vault.enc")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "litepsm", "secrets", "vault.enc")
	}
	return filepath.Join(".", "secrets", "vault.enc")
}

func deriveVaultKey(path string) []byte {
	h := sha256.New()
	h.Write([]byte("litepsm-vault-key-salt-v1:"))
	h.Write([]byte(path))
	if u, err := user.Current(); err == nil && u != nil {
		h.Write([]byte(u.Uid + ":" + u.Username))
	}
	if host, err := os.Hostname(); err == nil {
		h.Write([]byte(host))
	}
	return h.Sum(nil)
}

func (s *FileEncryptedSecretStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.vaultPath)
	if err != nil {
		return err
	}

	var vault serializedVault
	if err := json.Unmarshal(data, &vault); err != nil {
		return err
	}

	s.entries = vault.Entries
	if s.entries == nil {
		s.entries = make(map[string]vaultDiskEntry)
	}
	return nil
}

func (s *FileEncryptedSecretStore) persist() error {
	dir := filepath.Dir(s.vaultPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	vault := serializedVault{
		Version: 1,
		Entries: s.entries,
	}

	data, err := json.MarshalIndent(vault, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := s.vaultPath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmpFile, 0600); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	if err := os.Rename(tmpFile, s.vaultPath); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	return os.Chmod(s.vaultPath, 0600)
}

func (s *FileEncryptedSecretStore) Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error) {
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
		createdAt = existing.Metadata.CreatedAt
	}

	s.entries[ref.URI] = vaultDiskEntry{
		EncryptedData: ciphertext,
		Nonce:         nonce,
		Metadata: SecretMetadata{
			URI:       ref.URI,
			Namespace: namespace,
			Key:       key,
			CreatedAt: createdAt,
			UpdatedAt: now,
		},
	}

	if err := s.persist(); err != nil {
		return nil, fmt.Errorf("failed to persist encrypted vault: %w", err)
	}
	return ref, nil
}

func (s *FileEncryptedSecretStore) Get(ctx context.Context, ref SecretRef) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[ref.URI]
	if !ok {
		return nil, domain.ErrNotFound("secret", ref.URI)
	}

	plaintext, err := s.gcm.Open(nil, entry.Nonce, entry.EncryptedData, []byte(ref.URI))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secret %s: %w", ref.URI, err)
	}

	return plaintext, nil
}

func (s *FileEncryptedSecretStore) Delete(ctx context.Context, ref SecretRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[ref.URI]; !ok {
		return domain.ErrNotFound("secret", ref.URI)
	}

	delete(s.entries, ref.URI)
	if err := s.persist(); err != nil {
		return fmt.Errorf("failed to persist encrypted vault after deletion: %w", err)
	}
	return nil
}

func (s *FileEncryptedSecretStore) Exists(ctx context.Context, ref SecretRef) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.entries[ref.URI]
	return ok, nil
}

func (s *FileEncryptedSecretStore) ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []SecretMetadata
	for _, entry := range s.entries {
		if namespace == "" || entry.Metadata.Namespace == namespace {
			result = append(result, entry.Metadata)
		}
	}
	return result, nil
}

func (s *FileEncryptedSecretStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.key {
		s.key[i] = 0
	}
	s.entries = make(map[string]vaultDiskEntry)
	return nil
}
