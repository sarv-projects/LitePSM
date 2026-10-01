package connector

// lifecycle.go — the three-layer model: provider → integration → connection.
//
// A provider is a ConnectorManifest: static, versioned, signed, shipped in the
// repo. An integration is a user's instance of a provider: which auth scheme
// was chosen, which OAuth client (ours or theirs), which scopes were granted.
// A connection is one account's live credentials on one integration.
//
// Lifetimes differ per layer, which is why they are separate records:
// manifests change when we ship; integrations change when the user reconfigures;
// connections change every time a token refreshes.
//
// Health is DERIVED, never stored as an enum. A status column that someone must
// remember to update rots; timestamps and counters do not. ConnectionStatus is
// computed from last success/failure, attempt counts and revocation marks.

import (
	"fmt"
	"strings"
	"time"
)

// ConnectionStatus is computed, not persisted. Persist the inputs
// (timestamps, counters, revocation); compute the status.
type ConnectionStatus string

const (
	StatusActive   ConnectionStatus = "ACTIVE"
	StatusExpired  ConnectionStatus = "EXPIRED"
	StatusDisabled ConnectionStatus = "DISABLED"
	StatusFailed   ConnectionStatus = "FAILED"
	StatusRevoked  ConnectionStatus = "REVOKED"
	StatusStale    ConnectionStatus = "STALE"
)

// Refresh outcome policy, shared by the executor and any future poller:
// one refresh attempt per 401, then exactly one replay. A second 401 marks
// the connection STALE and stops. Retry-forever on a 401 is a footgun: it
// burns rate limit against a credential the provider has already rejected.
const maxRefreshAttemptsPerCall = 1

// Integration is a configured instance of a provider.
type Integration struct {
	ID           string         `json:"id"`
	ProviderID   string         `json:"providerId"`
	ManifestHash string         `json:"manifestHash"`
	Scheme       AuthSchemeType `json:"scheme"`

	// GrantedScopes is the exact scope set the user consented to. Operations
	// requiring any scope outside this set must re-prompt, never silently
	// widen.
	GrantedScopes []string `json:"grantedScopes"`

	// OAuthClientRef points at the vault record holding our client id/secret
	// for this integration, if the scheme is OAuth2. Never inline.
	OAuthClientRef string `json:"oauthClientRef,omitempty"`

	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Connection is one account's credentials on one integration.
type Connection struct {
	ID            string `json:"id"`
	IntegrationID string `json:"integrationId"`
	ProviderID    string `json:"providerId"`

	// Visibility is always PRIVATE in this implementation. There is no sharing
	// edge in a single-user vault; the field exists so a future sharing model
	// must explicitly change it rather than inherit an open default.
	Visibility string `json:"visibility"`

	// SecretRef is the ONLY credential pointer. It names a vault record; the
	// plaintext lives exclusively behind secrets.SecretStore.Get.
	SecretRef string `json:"secretRef"`

	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt *time.Time `json:"lastFailureAt,omitempty"`
	FailureCount  int        `json:"failureCount"`
	RefreshCount  int        `json:"refreshCount"`

	DisabledAt *time.Time `json:"disabledAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`

	// NonSecretMetadata holds display-safe facts: account login, workspace
	// name, token type. Anything secret-shaped is refused at write time.
	Metadata map[string]string `json:"metadata,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DeriveStatus computes the connection's health from its recorded history.
// Precedence is deliberate: an explicit human or provider action (revoke,
// disable) outranks any inference; exhaustion outranks expiry because a dead
// credential and an expired one need different UX.
func (c *Connection) DeriveStatus(now time.Time, exhaustedAfter int) ConnectionStatus {
	if c.RevokedAt != nil {
		return StatusRevoked
	}
	if c.DisabledAt != nil {
		return StatusDisabled
	}
	if exhaustedAfter > 0 && c.FailureCount >= exhaustedAfter {
		return StatusFailed
	}
	if c.ExpiresAt != nil && now.After(*c.ExpiresAt) {
		return StatusExpired
	}
	if c.LastFailureAt != nil {
		if c.LastSuccessAt == nil || c.LastFailureAt.After(*c.LastSuccessAt) {
			return StatusStale
		}
	}
	return StatusActive
}

// RecordSuccess resets the failure streak. Called only after a provider call
// returned 2xx with this connection's credentials.
func (c *Connection) RecordSuccess(now time.Time) {
	c.LastSuccessAt = &now
	c.FailureCount = 0
	c.UpdatedAt = now
}

// RecordFailure advances the failure streak. The caller decides, from the
// provider's response class, whether this failure is refreshable (401 with a
// refresh token available) or terminal for this call (403, 404, 422).
func (c *Connection) RecordFailure(now time.Time) {
	c.LastFailureAt = &now
	c.FailureCount++
	c.UpdatedAt = now
}

// ReconnectInPlace replaces the credential pointer after a fresh authorization
// while preserving everything else: metadata, history, integration binding.
// Delete-and-recreate would orphan audit continuity and synced state, so the
// store must offer this instead of forcing callers to rebuild the record.
func (c *Connection) ReconnectInPlace(newSecretRef string, newExpiry *time.Time, now time.Time) error {
	if strings.TrimSpace(newSecretRef) == "" {
		return fmt.Errorf("reconnect requires a new secret reference")
	}
	c.SecretRef = newSecretRef
	c.ExpiresAt = newExpiry
	c.FailureCount = 0
	c.RevokedAt = nil
	c.UpdatedAt = now
	return nil
}

// NeedsRefresh reports whether the credential should be refreshed before use.
// Refresh is lazy and inline: proactively before expiry, or reactively on one
// 401. There is no background daemon; a laptop is not a server fleet.
func (c *Connection) NeedsRefresh(now time.Time, leadTime time.Duration) bool {
	if c.ExpiresAt == nil {
		return false
	}
	return now.Add(leadTime).After(*c.ExpiresAt)
}

// ScopesCover reports whether the integration's granted scopes include every
// scope an operation needs. Missing scopes re-prompt; they never widen
// silently.
func (i *Integration) ScopesCover(required []string) bool {
	if len(required) == 0 {
		return true
	}
	have := make(map[string]bool, len(i.GrantedScopes))
	for _, s := range i.GrantedScopes {
		have[s] = true
	}
	for _, r := range required {
		if !have[r] {
			return false
		}
	}
	return true
}
