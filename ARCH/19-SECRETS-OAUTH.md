# Secrets Management & OAuth Architecture

> **State (see `STATUS.md` §1, §4).** `internal/secrets` is `WIRED` — the vault
> is opened by the daemon and by `doctor`. `internal/auth` (the OAuth PKCE
> broker) is **`IMPLEMENTED`, not `WIRED`**: it compiles, is unit-tested, and has
> **zero production importers**. Nothing in this document may be read as "OAuth
> runs today".

## 1. SecretStore Interface

LiteSPM enforces a strict separation between metadata stored in SQLite and secret material stored in an encrypted vault keyed by an OS-protected master key:

```go
type SecretStore interface {
    Put(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error)
    Get(ctx context.Context, ref SecretRef) ([]byte, error)
    Delete(ctx context.Context, ref SecretRef) error
    Exists(ctx context.Context, ref SecretRef) (bool, error)
    ListMetadata(ctx context.Context, namespace string) ([]SecretMetadata, error)
    Close() error
}

type SecretRef struct {
    URI       string `json:"uri"`       // secret:<namespace>/<key>
    Namespace string `json:"namespace"` // e.g. "mcp:provider", "oauth"
    Key       string `json:"key"`       // e.g. "postgres_password"
}
```

(`internal/secrets/types.go:12-16, 56-63` — the six methods above are the whole
interface; `Close()` is part of it and is easy to drop when quoting it.)

### 1.1 Launch-time resolution

```go
// ResolveLaunchSecrets resolves a map of environment variables to secret URIs
// into plaintext values.
func ResolveLaunchSecrets(ctx context.Context, store SecretStore, secretRefs map[string]string) (map[string]string, error)
```

`secretRefs` maps an **environment variable name → `secret:` URI**; the result
maps the same name to the plaintext value. It validates env-var names, refuses
a denylist of dangerous variables (`LD_PRELOAD`, `LD_LIBRARY_PATH`,
`DYLD_*`, `PATH`, `IFS`, … — `internal/secrets/types.go:67-74`), and fails closed
on an unresolvable reference (`internal/secrets/types.go:76-105`).

**Wiring caveat:** the provider supervisor has the injection seam
(`LaunchSpec.SecretEnv` → `Supervisor.StartProvider`,
`internal/provider/supervisor.go:76,183`), but **`ResolveLaunchSecrets` has no
production caller** — nothing currently pulls a secret out of the vault and
fills `SecretEnv`. Treat "secrets injected at provider launch" as an intended
path, not as a demonstrated end-to-end flow — `STATUS.md` §1 records launch
injection as `IMPLEMENTED`, not `WIRED`.

---

## 2. Platform-Specific Secret Vault Backends

**Mechanism: CLI wrappers and Win32/DPAPI, not library bindings.** The platform
store protects one thing — a 32-byte **master key** — and secret payloads live
in an AES-256-GCM encrypted vault file that the master key unlocks.

```text
┌─────────────┬──────────────────────────────────────────────────────────────┐
│ Operating   │ Master-key protection (what the code actually calls)         │
├─────────────┼──────────────────────────────────────────────────────────────┤
│ Linux       │ `secret-tool` CLI (FreeDesktop Secret Service over D-Bus):   │
│             │ `secret-tool lookup/store service litespm account master-key`│
│             │ (`internal/secrets/store_linux.go`, `exec.Command`)          │
├─────────────┼──────────────────────────────────────────────────────────────┤
│ macOS       │ `/usr/bin/security` CLI: `add-generic-password` /           │
│             │ `find-generic-password -s litespm -a master-key`            │
│             │ (`internal/secrets/store_darwin.go`, `exec.Command`)         │
├─────────────┼──────────────────────────────────────────────────────────────┤
│ Windows     │ DPAPI — `CryptProtectData` / `CryptUnprotectData` via        │
│             │ `golang.org/x/sys/windows`, protecting `master.key`          │
│             │ (`internal/secrets/store_windows.go`). No Credential         │
│             │ Manager / `CredRead` binding exists.                         │
├─────────────┼──────────────────────────────────────────────────────────────┤
│ Other OS    │ No store: `ErrAuthVaultUnavailable`                          │
│             │ (`internal/secrets/store_other.go`)                          │
└─────────────┴──────────────────────────────────────────────────────────────┘
```

*   **Vault payload:** `vault.enc` at the platform data path, AES-256-GCM,
    `NewFileEncryptedSecretStoreWithKey` (`internal/secrets/vault_file.go:43`).
    The path-derived-key variant `NewFileEncryptedSecretStore`
    (`vault_file.go:73`) is used only by tests.
*   **In-memory store:** `MemorySecretStore` (`store_mem.go`) is an AES-256-GCM
    in-memory vault used by tests and test harnesses — it is not a fallback for
    a missing OS vault.
*   **No plaintext fallback:** if the platform store cannot be opened, LiteSPM
    reports `LPSM-AUTH-VAULT-UNAVAILABLE` (`internal/domain/errors.go:15`) and
    refuses to continue on paths that would write credentials. Unencrypted
    configuration files are never used as a substitute.

### 2.1 The "halt on vault failure" invariant is enforced

*   **Daemon serve:** `runDaemonServe` opens the store before it binds IPC and
    exits if it cannot — `cmd/litespm/main.go:1133-1139`:
    `Fatal: [LPSM-AUTH-VAULT-UNAVAILABLE] …` → `os.Exit(1)`. The comment at
    `main.go:1133-1134` records this document as the source of the rule.
*   **Doctor:** deliberately does **not** halt — it must keep diagnosing the
    failure it is reporting. It prints a `WARNING: [LPSM-AUTH-VAULT-UNAVAILABLE]`
    and appends a FAIL check `check_secrets_open` with the recommendation
    "LiteSPM refuses to store credentials in plaintext (ARCH/19)"
    (`cmd/litespm/main.go:1009-1030`).
*   **Everywhere else:** any open failure surfaces as the same typed error; there
    is no silent degradation path.

---

## 3. AuthBroker & OAuth 2.0 PKCE Loopback — `IMPLEMENTED`, not `WIRED`

`internal/auth` implements the full flow (`broker.go`, `pkce.go`, `loopback.go`)
and is exercised by `internal/auth/auth_test.go` (which is `package auth`, not an
importer). A repo-wide search for the import path `litespm/internal/auth`
returns **no importer anywhere** — production or test outside the package. It
therefore sits behind the same gap as the connector design: it needs a consumer
before it can be called `WIRED`.

**Planned wiring points** (all currently `DESIGNED`):

*   provider launch / capability execution — `ARCH/34` (invocation engine,
    receipts) and `STATUS.md` §4 ("wire into provider launch");
*   connector credential brokering and capability leases — `ARCH/29` (design
    record; resurrection planned) and `ARCH/35` (profiles & leases);
*   the vault it stores tokens into is this document, §1–§2.

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
1.  **Loopback Binding:** the listener binds exclusively to `127.0.0.1:0`, never `0.0.0.0` or an external interface (`internal/auth/loopback.go:39-49`).
2.  **State Parameter Verification:** a cryptographically random **32-byte** state token (`loopback.go:41-46`); a callback whose `state` does not match is rejected (`loopback.go:98-107`).
3.  **Timeout:** `DefaultLoopbackTimeout = 120 * time.Second` (`loopback.go:19-20`).
4.  **No URL Leaks:** tokens exchanged in HTTP POST bodies go to memory buffers and then to the `SecretStore`; they are not written to HTTP logs or terminal stdout.

Broker entry points, all on `*AuthBroker` (`internal/auth/broker.go`):
`DescribeAuth`, `StartAuth`, `ExchangeToken`, `CompleteAuth`, `RevokeAuth`,
`RefreshAuth`, `GetAuthStatus`.

---

## 4. Persistent Auth Profiles & Secret Linkage

Successful authentication creates an `AuthProfile` in SQLite (`auth_profiles`)
linking non-sensitive profile state to the secure vault handle:

*   **Database record:** `auth_profiles (profile_id, provider_id, profile_type, secret_ref, status, metadata_json, created_at, updated_at)` — `internal/state/repositories.go:553`. `profile_type` is `oauth2` or `api_key` (`internal/auth/types.go:14-15`); `status` starts `valid` (`broker.go:182`) and is set `revoked` on revoke (`broker.go:211`).
*   **Decoupled secrets:** tokens and API keys live only in `SecretStore`; the row carries an opaque `secret_ref`.
*   **Revocation:** `AuthBroker.RevokeAuth(profileID)` (`internal/auth/broker.go:198`) purges the secret from the vault and sets the row status to `revoked`. It runs only if something calls it — today nothing does (§3).
*   **Provider linkage:** `providers.auth_profile_id` exists in the schema (`internal/state/repositories.go:377,415-416`) but provider rows are never written by non-test code (`STATUS.md` §4), so the link is currently inert.

---

## 5. Configuration & environment precedence

Env overrides read the current name first and fall back to the pre-rename name:

```go
func envOverride(current, legacy string) string   // internal/config/config.go:124-129
```

`applyEnvOverrides` (`config.go:131-151`) pairs each `LITESPM_*` variable with its
legacy `LITEPSM_*` twin (`LITESPM_REGISTRY_URL`/`LITEPSM_REGISTRY_URL`,
`LITESPM_LOG_LEVEL`, `LITEPSM_LOG_LEVEL`, policy flags, …): **an upgraded
installation that still exports `LITEPSM_*` keeps its settings, and the current
name always wins** (`config.go:120-123`). The same rename rule covers on-disk
locations — legacy `litepsm` data dirs, socket and pipe prefixes are still
discovered (`internal/config/paths.go:37,100`), and legacy `.litepsm/config.toml`
is adopted when the new path is absent (`internal/config/config.go:105`).
