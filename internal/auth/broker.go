package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/state"
)

// AuthBroker coordinates OAuth 2.0 PKCE authentication, secure secret storage, and persistent profile state.
type AuthBroker struct {
	db         *state.DB
	secrets    secrets.SecretStore
	httpClient *http.Client
	sessions   map[string]*AuthSession
	sessionsMu sync.RWMutex
}

// NewAuthBroker creates a new AuthBroker.
func NewAuthBroker(db *state.DB, secretStore secrets.SecretStore, httpClient *http.Client) *AuthBroker {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &AuthBroker{
		db:         db,
		secrets:    secretStore,
		httpClient: httpClient,
		sessions:   make(map[string]*AuthSession),
	}
}

// DescribeAuth returns authentication requirements for a provider.
func (b *AuthBroker) DescribeAuth(ctx context.Context, providerID string) (*AuthDescription, error) {
	return &AuthDescription{
		ProviderID:   providerID,
		Type:         AuthTypeOAuth2,
		Instructions: "Authenticate with your provider account using browser-based OAuth PKCE.",
	}, nil
}

// StartAuth initiates a PKCE-secured authentication flow and starts a loopback listener.
func (b *AuthBroker) StartAuth(ctx context.Context, req AuthStartRequest, listener *LoopbackListener) (*AuthSession, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	sessionID := fmt.Sprintf("auth_sess_%d", time.Now().UnixNano())

	redirectURI := listener.RedirectURI()
	stateToken := listener.StateToken()

	// Construct provider authorization URL
	authURL, err := url.Parse(req.AuthURL)
	if err != nil {
		return nil, fmt.Errorf("invalid auth URL %q: %w", req.AuthURL, err)
	}

	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", req.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", stateToken)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	if len(req.Scopes) > 0 {
		q.Set("scope", strings.Join(req.Scopes, " "))
	}
	authURL.RawQuery = q.Encode()

	now := time.Now().UTC()
	session := &AuthSession{
		SessionID:    sessionID,
		ProviderID:   req.ProviderID,
		Type:         AuthTypeOAuth2,
		StateToken:   stateToken,
		PKCEVerifier: pkce.Verifier,
		RedirectURI:  redirectURI,
		AuthURL:      authURL.String(),
		CreatedAt:    now,
		ExpiresAt:    now.Add(DefaultLoopbackTimeout),
	}

	b.sessionsMu.Lock()
	b.sessions[sessionID] = session
	b.sessionsMu.Unlock()

	return session, nil
}

// ExchangeToken exchanges an authorization code and PKCE verifier for OAuth tokens.
func (b *AuthBroker) ExchangeToken(ctx context.Context, tokenURL, clientID, clientSecret, code, redirectURI, verifier string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", clientID)
	data.Set("code_verifier", verifier)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer httpResp.Body.Close()

	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", httpResp.StatusCode, string(bodyBytes))
	}

	var tokenRes TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenRes); err != nil {
		return nil, fmt.Errorf("failed to parse token response JSON: %w", err)
	}

	return &tokenRes, nil
}

// CompleteAuth stores tokens securely in OS vault and registers the AuthProfile in SQLite.
func (b *AuthBroker) CompleteAuth(ctx context.Context, sessionID string, tokenRes *TokenResponse) (*domain.AuthProfile, error) {
	b.sessionsMu.Lock()
	session, ok := b.sessions[sessionID]
	if ok {
		delete(b.sessions, sessionID)
	}
	b.sessionsMu.Unlock()

	if !ok {
		return nil, domain.ErrNotFound("auth_session", sessionID)
	}

	tokenBytes, err := json.Marshal(tokenRes)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize tokens: %w", err)
	}

	// 1. Store credentials strictly into OS SecretStore
	secretKey := fmt.Sprintf("oauth_%s", session.ProviderID)
	secretRef, err := b.secrets.Put(ctx, "oauth", secretKey, tokenBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to store tokens in secret store: %w", err)
	}

	// 2. Persist non-sensitive metadata in SQLite auth_profiles
	profileID := fmt.Sprintf("prof_%s", session.ProviderID)
	now := time.Now().UTC()

	metaJSON, _ := json.Marshal(map[string]any{
		"token_type": tokenRes.TokenType,
		"expires_in": tokenRes.ExpiresIn,
		"scope":      tokenRes.Scope,
	})

	profile := &domain.AuthProfile{
		ProfileID:    profileID,
		ProviderID:   session.ProviderID,
		ProfileType:  string(session.Type),
		SecretRef:    secretRef.URI,
		Status:       "valid",
		MetadataJSON: string(metaJSON),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := b.db.SaveAuthProfile(ctx, profile); err != nil {
		// Clean up stored secret on DB failure
		_ = b.secrets.Delete(ctx, *secretRef)
		return nil, fmt.Errorf("failed to save auth profile in database: %w", err)
	}

	return profile, nil
}

// RevokeAuth purges secrets from the OS vault and marks the profile revoked in SQLite.
func (b *AuthBroker) RevokeAuth(ctx context.Context, profileID string) error {
	prof, err := b.db.GetAuthProfile(ctx, profileID)
	if err != nil {
		return err
	}

	// Purge secret from OS vault
	ref, err := secrets.ParseSecretRef(prof.SecretRef)
	if err == nil {
		_ = b.secrets.Delete(ctx, *ref)
	}

	// Update SQLite status
	prof.Status = "revoked"
	prof.UpdatedAt = time.Now().UTC()
	return b.db.SaveAuthProfile(ctx, prof)
}

// RefreshAuth uses the stored refresh token to obtain fresh credentials.
func (b *AuthBroker) RefreshAuth(ctx context.Context, profileID, tokenURL, clientID, clientSecret string) error {
	prof, err := b.db.GetAuthProfile(ctx, profileID)
	if err != nil {
		return err
	}

	if prof.Status != "valid" {
		return fmt.Errorf("cannot refresh auth profile %s with status %s", profileID, prof.Status)
	}

	ref, err := secrets.ParseSecretRef(prof.SecretRef)
	if err != nil {
		return err
	}

	storedBytes, err := b.secrets.Get(ctx, *ref)
	if err != nil {
		return fmt.Errorf("failed to retrieve stored tokens: %w", err)
	}

	var currentTokens TokenResponse
	if err := json.Unmarshal(storedBytes, &currentTokens); err != nil {
		return fmt.Errorf("failed to parse stored tokens: %w", err)
	}

	if currentTokens.RefreshToken == "" {
		return fmt.Errorf("no refresh token available for profile %s", profileID)
	}

	// Execute refresh token exchange
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", currentTokens.RefreshToken)
	data.Set("client_id", clientID)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("refresh request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("token refresh failed with status %d", httpResp.StatusCode)
	}

	var freshTokens TokenResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&freshTokens); err != nil {
		return fmt.Errorf("failed to decode refreshed tokens: %w", err)
	}

	// Preserve refresh token if provider omitted it in refresh response
	if freshTokens.RefreshToken == "" {
		freshTokens.RefreshToken = currentTokens.RefreshToken
	}

	freshBytes, _ := json.Marshal(freshTokens)
	_, err = b.secrets.Put(ctx, ref.Namespace, ref.Key, freshBytes)
	if err != nil {
		return fmt.Errorf("failed to update secret store with refreshed tokens: %w", err)
	}

	prof.UpdatedAt = time.Now().UTC()
	return b.db.SaveAuthProfile(ctx, prof)
}

// GetAuthStatus checks the authentication status of a provider.
func (b *AuthBroker) GetAuthStatus(ctx context.Context, providerID string) (string, error) {
	profileID := fmt.Sprintf("prof_%s", providerID)
	prof, err := b.db.GetAuthProfile(ctx, profileID)
	if err != nil {
		return "unauthenticated", nil
	}
	return prof.Status, nil
}
