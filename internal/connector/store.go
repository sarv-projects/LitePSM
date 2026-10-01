package connector

// store.go — the grant table and the vault-backed secret resolver.
//
// The executor asks "release this field" via SecretResolver. This file is the
// production answer: check the grant table FIRST, decrypt SECOND. A component
// that is not in the connection's grant list — or that asks for a field it
// was not granted — gets an error, never plaintext.
//
// Grants are created only by explicit human action (connect flow, scope
// approval). Nothing in this package creates a grant implicitly: no wildcard
// components, no "all fields", no default-allow. An empty grant table means
// nothing resolves, which is the safe default.
//
// Persistence note: grants live in memory in this implementation. Durable
// storage (SQLite, alongside the connection records) is a daemon-integration
// concern and intentionally out of scope — this package must stay importable
// without the daemon's storage layer.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/sarv-projects/litepsm/internal/secrets"
)

// FieldGrant authorizes one component to receive one credential field of one
// connection. Created by explicit human approval; never inferred.
type FieldGrant struct {
	ConnectionID string
	ComponentID  string
	Field        string
	// ScopesBound pins the grant to the scope set that was approved with it.
	// If the integration's granted scopes later widen, old grants do not
	// silently cover the new scopes — the caller re-checks ScopesCover.
	ScopesBound []string
}

// GrantStore is the in-memory grant table. All methods are safe for
// concurrent use; the executor may resolve from many goroutines.
type GrantStore struct {
	mu     sync.RWMutex
	grants map[string]map[string]map[string]FieldGrant // connection → component → field
}

// NewGrantStore returns an empty (default-deny) grant table.
func NewGrantStore() *GrantStore {
	return &GrantStore{grants: make(map[string]map[string]map[string]FieldGrant)}
}

// Approve records one field grant. Idempotent: re-approving the same triple
// refreshes the scope binding.
func (g *GrantStore) Approve(grant FieldGrant) error {
	if strings.TrimSpace(grant.ConnectionID) == "" || strings.TrimSpace(grant.ComponentID) == "" || strings.TrimSpace(grant.Field) == "" {
		return fmt.Errorf("connector: grant requires connection, component, and field")
	}
	// No wildcards. A grant for "*" would be default-allow with extra steps.
	for _, s := range []string{grant.ConnectionID, grant.ComponentID, grant.Field} {
		if s == "*" {
			return fmt.Errorf("connector: wildcard grants are refused")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	byComp, ok := g.grants[grant.ConnectionID]
	if !ok {
		byComp = make(map[string]map[string]FieldGrant)
		g.grants[grant.ConnectionID] = byComp
	}
	byField, ok := byComp[grant.ComponentID]
	if !ok {
		byField = make(map[string]FieldGrant)
		byComp[grant.ComponentID] = byField
	}
	byField[grant.Field] = grant
	return nil
}

// RevokeComponent removes every grant for one component on one connection.
// Used when a component is uninstalled or its approval is withdrawn.
func (g *GrantStore) RevokeComponent(connectionID, componentID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if byComp, ok := g.grants[connectionID]; ok {
		delete(byComp, componentID)
	}
}

// RevokeConnection removes every grant on one connection. Used on reconnect
// (grants are re-approved against the new credential) and on revoke.
func (g *GrantStore) RevokeConnection(connectionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.grants, connectionID)
}

// Lookup reports whether (connection, component, field) is granted.
func (g *GrantStore) Lookup(connectionID, componentID, field string) (FieldGrant, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	byComp, ok := g.grants[connectionID]
	if !ok {
		return FieldGrant{}, false
	}
	byField, ok := byComp[componentID]
	if !ok {
		return FieldGrant{}, false
	}
	grant, ok := byField[field]
	return grant, ok
}

// VaultResolver is the production SecretResolver: grant check first, vault
// read second. The secretRef convention is "secret:<namespace>/<key>" via
// secrets.ParseSecretRef; the namespace isolates connector credentials from
// every other secret class in the store.
func VaultResolver(store secrets.SecretStore, grants *GrantStore, refFor func(connectionID, field string) (string, error)) SecretResolver {
	return func(ctx context.Context, check GrantCheck) (string, error) {
		if _, ok := grants.Lookup(check.ConnectionID, check.ComponentID, check.Field); !ok {
			return "", fmt.Errorf("connector: component %q is not granted field %q on connection %q",
				check.ComponentID, check.Field, check.ConnectionID)
		}
		uri, err := refFor(check.ConnectionID, check.Field)
		if err != nil {
			return "", fmt.Errorf("connector: resolving secret reference: %w", err)
		}
		ref, err := secrets.ParseSecretRef(uri)
		if err != nil {
			return "", fmt.Errorf("connector: invalid secret reference: %w", err)
		}
		plaintext, err := store.Get(ctx, *ref)
		if err != nil {
			return "", fmt.Errorf("connector: vault read failed: %w", err)
		}
		// Copy out of the store's buffer so zeroing (if the backend ever
		// zeroes its buffer on return) cannot race the caller.
		out := make([]byte, len(plaintext))
		copy(out, plaintext)
		return string(out), nil
	}
}

// StandardRefFor maps (connection, field) to the canonical vault URI. One
// function owns the convention so connection records, the vault, and the
// resolver cannot disagree about where a credential lives.
func StandardRefFor(connectionID, field string) (string, error) {
	if strings.TrimSpace(connectionID) == "" || strings.TrimSpace(field) == "" {
		return "", fmt.Errorf("connector: connection and field are required")
	}
	if strings.ContainsAny(connectionID, "/:") || strings.ContainsAny(field, "/:") {
		return "", fmt.Errorf("connector: connection and field must not contain '/' or ':'")
	}
	return fmt.Sprintf("secret:connector/%s/%s", connectionID, field), nil
}

// GrantedFields lists every field one component may receive on one connection.
// Used by UIs that show "this component can access: …" — field names, never
// values.
func (g *GrantStore) GrantedFields(connectionID, componentID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []string
	if byComp, ok := g.grants[connectionID]; ok {
		if byField, ok := byComp[componentID]; ok {
			for f := range byField {
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}
