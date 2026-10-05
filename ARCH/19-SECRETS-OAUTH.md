# Secrets Management & OAuth Architecture

## 1. SecretStore Interface

LiteSPM enforces a strict separation between metadata stored in SQLite and sensitive credentials stored in native operating system vaults:

```go
type SecretStore interface {
    Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error)
    Get(ctx context.Context, ref SecretRef) ([]byte, error)
    Delete(ctx context.Context, ref SecretRef) error
    Exists(ctx context.Context, ref SecretRef) (bool, error)
    ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error)
}

type SecretRef struct {
    URI       string `json:"uri"`       // secret:<namespace>/<key>
    Namespace string `json:"namespace"` // e.g. "mcp:provider"
    Key       string `json:"key"`       // e.g. "postgres_password"
}
```

---

## 2. Platform-Specific Secret Vault Backends

```text
┌─────────────────┬─────────────────────────────────────────────────────────────┐
│ Operating System│ Native Credential Store Implementation                      │
├─────────────────┼─────────────────────────────────────────────────────────────┤
│ Windows         │ Windows Credential Manager (`wincred.dll` API)              │
│                 │ Fallback: Data Protection API (DPAPI) encrypted local store │
├─────────────────┼─────────────────────────────────────────────────────────────┤
│ macOS           │ Apple Keychain Services (`SecItemAdd`, `SecItemCopyMatching`) │
├─────────────────┼─────────────────────────────────────────────────────────────┤
│ Linux           │ FreeDesktop Secret Service specification via D-Bus          │
│                 │ (`org.freedesktop.secrets` / libsecret)                     │
└─────────────────┴─────────────────────────────────────────────────────────────┘
```

*   **No Plaintext Fallback:** If an operating system lacks a functional credential vault, LiteSPM halts with `LPSM-AUTH-VAULT-UNAVAILABLE`. Storing credentials in unencrypted configuration files is strictly forbidden.

---

## 3. AuthBroker & OAuth 2.0 PKCE Loopback

When an MCP provider requires user authentication via OAuth 2.0:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        OAuth PKCE Loopback Flow                        │
│                                                                        │
│   1. Generate PKCE Verifier & Code Challenge (RFC 7636)                │
│   2. Bind Local Loopback Listener strictly to 127.0.0.1 on Port 0      │
│   3. Construct Auth URL with redirect_uri=http://127.0.0.1:<port>/callback │
│   4. Open System Default Browser to Provider Auth Page                 │
│   5. Await Callback with State Validation (Timeout: 120 seconds)       │
│   6. Exchange Authorization Code for Access & Refresh Tokens           │
│   7. Store Tokens into SecretStore; return opaque AuthProfileId        │
│   8. Close Ephemeral Loopback Listener                                 │
└────────────────────────────────────────────────────────────────────────┘
```

### 3.1 Loopback Security Rules
1.  **Loopback Binding:** The listener binds exclusively to `127.0.0.1:0` (never `0.0.0.0` or external network interfaces).
2.  **State Parameter Verification:** Generates a cryptographically random 32-byte state token. Callbacks lacking an identical state token are rejected with HTTP 403.
3.  **No URL Leaks:** Tokens exchanged in HTTP POST bodies are written directly to memory buffers and pushed to the `SecretStore`. No tokens are recorded in HTTP logs or terminal stdout.

---

## 4. Persistent Auth Profiles & Secret Linkage

Successful authentication creates an `AuthProfile` in SQLite (`auth_profiles` table) linking non-sensitive profile state to the secure OS secret handle:
*   **Database Record:** Persists `profile_id`, `provider_id`, `profile_type` (`oauth2`, `api_key`), `status` (`valid`, `expired`, `revoked`), and opaque `secret_ref`.
*   **Decoupled Secrets:** Tokens and API keys are stored solely in `SecretStore`.
*   **Revocation:** Calling `RevokeAuth(profileID)` purges the secret from the OS vault and updates the SQLite profile status to `revoked`.
