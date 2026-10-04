//go:build !linux && !windows && !darwin

package secrets

import (
	"fmt"
	"runtime"

	"github.com/sarv-projects/litepsm/internal/domain"
)

func newPlatformSecretStore() (SecretStore, error) {
	return nil, domain.ErrAuthVaultUnavailable(fmt.Sprintf("unsupported platform OS %q for secure secret store", runtime.GOOS))
}
