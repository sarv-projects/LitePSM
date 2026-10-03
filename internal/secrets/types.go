package secrets

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SecretRef identifies a sensitive credential stored in an OS vault.
type SecretRef struct {
	URI       string `json:"uri"`       // Format: "secret:<namespace>/<key>"
	Namespace string `json:"namespace"` // e.g. "mcp:provider", "oauth"
	Key       string `json:"key"`       // e.g. "github_token"
}

// ParseSecretRef parses a canonical secret URI into a SecretRef.
func ParseSecretRef(uri string) (*SecretRef, error) {
	if !strings.HasPrefix(uri, "secret:") {
		return nil, fmt.Errorf("invalid secret URI %q: must start with 'secret:'", uri)
	}

	rest := strings.TrimPrefix(uri, "secret:")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid secret URI %q: expected 'secret:<namespace>/<key>'", uri)
	}

	return &SecretRef{
		URI:       uri,
		Namespace: parts[0],
		Key:       parts[1],
	}, nil
}

// NewSecretRef creates a SecretRef from namespace and key.
func NewSecretRef(namespace, key string) *SecretRef {
	return &SecretRef{
		URI:       fmt.Sprintf("secret:%s/%s", namespace, key),
		Namespace: namespace,
		Key:       key,
	}
}

// SecretMetadata provides non-sensitive metadata for a stored secret.
type SecretMetadata struct {
	URI       string    `json:"uri"`
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SecretStore defines the contract for secure OS credential storage.
type SecretStore interface {
	Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error)
	Get(ctx context.Context, ref SecretRef) ([]byte, error)
	Delete(ctx context.Context, ref SecretRef) error
	Exists(ctx context.Context, ref SecretRef) (bool, error)
	ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error)
	Close() error
}

var validEnvNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

var dangerousEnvVars = map[string]bool{
	"LD_PRELOAD":            true,
	"LD_LIBRARY_PATH":       true,
	"DYLD_INSERT_LIBRARIES": true,
	"DYLD_LIBRARY_PATH":     true,
	"PATH":                  true,
	"IFS":                   true,
}

// ResolveLaunchSecrets resolves a map of environment variables to secret URIs into plaintext values.
func ResolveLaunchSecrets(ctx context.Context, store SecretStore, secretRefs map[string]string) (map[string]string, error) {
	if len(secretRefs) == 0 {
		return nil, nil
	}

	resolved := make(map[string]string, len(secretRefs))
	for envVar, uri := range secretRefs {
		if !validEnvNameRegex.MatchString(envVar) {
			return nil, fmt.Errorf("invalid environment variable name %q", envVar)
		}
		if dangerousEnvVars[strings.ToUpper(envVar)] {
			return nil, fmt.Errorf("dangerous environment variable %q cannot be injected from manifest", envVar)
		}

		ref, err := ParseSecretRef(uri)
		if err != nil {
			return nil, fmt.Errorf("invalid secret ref for %s: %w", envVar, err)
		}

		secretBytes, err := store.Get(ctx, *ref)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve secret for %s (%s): %w", envVar, uri, err)
		}

		resolved[envVar] = string(secretBytes)
	}

	return resolved, nil
}
