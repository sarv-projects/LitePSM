//go:build !windows && !darwin && !linux

package secrets

import "fmt"

func newPlatformSecretStore() (SecretStore, error) {
	mem, err := NewMemorySecretStore()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fallback secure store: %w", err)
	}
	return mem, nil
}
