package auth

import (
	"context"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// AuthType represents the type of authentication mechanism.
type AuthType string

const (
	AuthTypeOAuth2 AuthType = "oauth2"
	AuthTypeAPIKey AuthType = "api_key"
	AuthTypeBearer AuthType = "bearer"
	AuthTypeCustom AuthType = "custom"
)

// AuthDescription describes the authentication requirements of a capability or provider.
type AuthDescription struct {
	ProviderID   string   `json:"providerId"`
	Type         AuthType `json:"type"`
	AuthURL      string   `json:"authUrl,omitempty"`
	TokenURL     string   `json:"tokenUrl,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
}

// AuthSession tracks an active in-flight authentication flow.
type AuthSession struct {
	SessionID    string    `json:"sessionId"`
	ProviderID   string    `json:"providerId"`
	Type         AuthType  `json:"type"`
	StateToken   string    `json:"stateToken"`
	PKCEVerifier string    `json:"pkceVerifier,omitempty"`
	RedirectURI  string    `json:"redirectUri"`
	AuthURL      string    `json:"authUrl"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// TokenResponse models the OAuth 2.0 token endpoint JSON response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// AuthStartRequest parameters for initiating authentication.
type AuthStartRequest struct {
	ProviderID   string   `json:"providerId"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	AuthURL      string   `json:"authUrl"`
	TokenURL     string   `json:"tokenUrl"`
	Scopes       []string `json:"scopes,omitempty"`
}

// TokenExchanger is a function that exchanges an authorization code for tokens.
type TokenExchanger func(ctx context.Context, tokenURL, clientID, clientSecret, code, redirectURI, verifier string) (*TokenResponse, error)

// Ensure domain.AuthProfile is available
type AuthProfile = domain.AuthProfile
