package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/state"
)

func TestPKCE_GenerationAndVerification(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE failed: %v", err)
	}

	if len(pkce.Verifier) < 40 {
		t.Errorf("expected verifier length >= 40, got %d", len(pkce.Verifier))
	}
	if pkce.Challenge == "" {
		t.Errorf("empty PKCE challenge")
	}
	if pkce.Method != "S256" {
		t.Errorf("expected S256 method, got %s", pkce.Method)
	}

	if !VerifyPKCE(pkce.Verifier, pkce.Challenge) {
		t.Errorf("PKCE challenge verification failed")
	}

	if VerifyPKCE("wrong_verifier", pkce.Challenge) {
		t.Errorf("expected wrong verifier to fail")
	}
}

func TestLoopbackListener_SuccessAndStateProtection(t *testing.T) {
	listener, err := StartLoopbackListener()
	if err != nil {
		t.Fatalf("StartLoopbackListener failed: %v", err)
	}

	port := listener.Port()
	if port <= 0 {
		t.Fatalf("invalid listener port: %d", port)
	}

	redirectURI := listener.RedirectURI()
	if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") {
		t.Errorf("redirect URI must bind strictly to 127.0.0.1: %s", redirectURI)
	}

	stateToken := listener.StateToken()

	// 1. Test CSRF state mismatch (HTTP 403)
	badResp, err := http.Get(fmt.Sprintf("%s?code=abc&state=bad_state", redirectURI))
	if err != nil {
		t.Fatalf("bad state GET failed: %v", err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Errorf("expected HTTP 403 for state mismatch, got %d", badResp.StatusCode)
	}

	// 2. Test valid callback (HTTP 200)
	goodResp, err := http.Get(fmt.Sprintf("%s?code=valid_auth_code_123&state=%s", redirectURI, stateToken))
	if err != nil {
		t.Fatalf("good state GET failed: %v", err)
	}
	_ = goodResp.Body.Close()
	if goodResp.StatusCode != http.StatusOK {
		t.Errorf("expected HTTP 200 for valid callback, got %d", goodResp.StatusCode)
	}

	ctx := context.Background()
	code, err := listener.WaitForCallback(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForCallback failed: %v", err)
	}
	if code != "valid_auth_code_123" {
		t.Errorf("unexpected authorization code received: %s", code)
	}
}

func TestAuthBroker_CompleteFlow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_state.db")

	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	defer db.Close()

	secretStore, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	defer secretStore.Close()

	// Mock OAuth token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grantType := r.FormValue("grant_type")
		w.Header().Set("Content-Type", "application/json")

		if grantType == "authorization_code" {
			code := r.FormValue("code")
			verifier := r.FormValue("code_verifier")
			if code == "auth_code_xyz" && verifier != "" {
				_ = json.NewEncoder(w).Encode(TokenResponse{
					AccessToken:  "access_token_abc_123",
					RefreshToken: "refresh_token_def_456",
					TokenType:    "Bearer",
					ExpiresIn:    3600,
					Scope:        "repo read:org",
				})
				return
			}
		} else if grantType == "refresh_token" {
			refToken := r.FormValue("refresh_token")
			if refToken == "refresh_token_def_456" {
				_ = json.NewEncoder(w).Encode(TokenResponse{
					AccessToken:  "access_token_refreshed_789",
					RefreshToken: "refresh_token_def_456",
					TokenType:    "Bearer",
					ExpiresIn:    3600,
				})
				return
			}
		}

		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer tokenServer.Close()

	broker := NewAuthBroker(db, secretStore, tokenServer.Client())

	ctx := context.Background()

	// 1. Start Auth
	listener, err := StartLoopbackListener()
	if err != nil {
		t.Fatalf("StartLoopbackListener failed: %v", err)
	}
	defer listener.Close()

	sess, err := broker.StartAuth(ctx, AuthStartRequest{
		ProviderID: "github",
		ClientID:   "client_123",
		AuthURL:    "https://github.com/login/oauth/authorize",
		TokenURL:   tokenServer.URL,
		Scopes:     []string{"repo"},
	}, listener)
	if err != nil {
		t.Fatalf("StartAuth failed: %v", err)
	}

	if !strings.Contains(sess.AuthURL, "code_challenge=") {
		t.Errorf("AuthURL missing PKCE code_challenge: %s", sess.AuthURL)
	}

	// 2. Exchange token
	tokenRes, err := broker.ExchangeToken(ctx, tokenServer.URL, "client_123", "", "auth_code_xyz", listener.RedirectURI(), sess.PKCEVerifier)
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}
	if tokenRes.AccessToken != "access_token_abc_123" {
		t.Fatalf("unexpected access token: %s", tokenRes.AccessToken)
	}

	// 3. Complete Auth
	profile, err := broker.CompleteAuth(ctx, sess.SessionID, tokenRes)
	if err != nil {
		t.Fatalf("CompleteAuth failed: %v", err)
	}

	if profile.Status != "valid" || profile.ProfileID != "prof_github" {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	// Verify secret is in SecretStore and not in plain SQLite
	secRef, err := secrets.ParseSecretRef(profile.SecretRef)
	if err != nil {
		t.Fatalf("invalid secret ref: %v", err)
	}
	storedBytes, err := secretStore.Get(ctx, *secRef)
	if err != nil {
		t.Fatalf("secret missing from store: %v", err)
	}
	if !strings.Contains(string(storedBytes), "access_token_abc_123") {
		t.Fatalf("secret store contents mismatch")
	}

	// Verify status
	status, err := broker.GetAuthStatus(ctx, "github")
	if err != nil || status != "valid" {
		t.Errorf("expected valid auth status, got %s (%v)", status, err)
	}

	// 4. Refresh Auth
	err = broker.RefreshAuth(ctx, profile.ProfileID, tokenServer.URL, "client_123", "")
	if err != nil {
		t.Fatalf("RefreshAuth failed: %v", err)
	}

	refreshedBytes, err := secretStore.Get(ctx, *secRef)
	if err != nil {
		t.Fatalf("Get refreshed failed: %v", err)
	}
	if !strings.Contains(string(refreshedBytes), "access_token_refreshed_789") {
		t.Fatalf("refreshed token not updated in secret store: %s", string(refreshedBytes))
	}

	// 5. Revoke Auth
	err = broker.RevokeAuth(ctx, profile.ProfileID)
	if err != nil {
		t.Fatalf("RevokeAuth failed: %v", err)
	}

	status, _ = broker.GetAuthStatus(ctx, "github")
	if status != "revoked" {
		t.Errorf("expected revoked status, got %s", status)
	}

	// Secret must be purged from secretStore
	_, err = secretStore.Get(ctx, *secRef)
	if err == nil {
		t.Errorf("secret should have been purged from secretStore upon revocation")
	}
}
