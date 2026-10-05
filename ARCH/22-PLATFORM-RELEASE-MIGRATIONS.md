# Platform, Release Engineering & Migrations

## 1. Cross-Platform Compilation Matrix

LiteSPM is compiled as a single native static Go binary (`cmd/litespm`):

```text
┌─────────────────┬──────────────────────┬───────────────────────────────────────────┐
│ Target OS       │ Architecture         │ Native System Integrations                │
├─────────────────┼──────────────────────┼───────────────────────────────────────────┤
│ Windows         │ amd64, arm64         │ DPAPI, Named Pipes with DACL, Job Objects │
├─────────────────┼──────────────────────┼───────────────────────────────────────────┤
│ macOS           │ arm64 (Apple Silicon)│ Apple Keychain Services, Domain Sockets   │
│                 │ amd64 (Intel)        │                                           │
├─────────────────┼──────────────────────┼───────────────────────────────────────────┤
│ Linux           │ amd64, arm64         │ Secret Service (D-Bus), Domain Sockets    │
└─────────────────┴──────────────────────┴───────────────────────────────────────────┘
```

*   **SQLite Compilation:** Uses a pure-Go SQLite driver (`modernc.org/sqlite`) or static CGO compilation, ensuring zero dynamic C-runtime library dependencies across target environments.

---

## 2. Database Migration Lifecycle

Database migrations are tracked in `schema_migrations` and executed on daemon startup:

```go
type Migration struct {
    Version     int
    Description string
    UpSQL       string
}
```

### Migration Rules
1.  **Transactional Execution:** Each migration runs inside an isolated SQLite transaction (`BEGIN IMMEDIATE`).
2.  **Pre-Migration Backup:** Before applying migrations, the daemon creates a copy of `state.db` at `DATA_ROOT/backups/db/state-pre-migration-v<N>.db`.
3.  **Strict Downgrade Prevention:** If the database contains a schema version higher than the compiled binary's maximum supported version:
    $$\text{Database Schema Version} > \text{Binary Max Supported Version}$$
    The daemon halts immediately with `LPSM-STATE-VERSION-INCOMPATIBLE`. It **never** attempts automated downgrades, which could destroy user state.

---

## 3. Binary Self-Update Architecture

LiteSPM binary updates are isolated from extension package updates:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Binary Self-Update Flow                         │
│                                                                        │
│   $ litespm self-update                                                │
│         │                                                              │
│         ▼                                                              │
│   1. Check Active Transactions (Abort if operation in-flight)          │
│   2. Fetch Latest Release Manifest & SHA256SUMS.txt checksums          │
│   3. Download Target Platform Binary to staging/litespm.new            │
│   4. Verify SHA-256 checksum only (no signature check yet)             │
│   5. On Windows: Rename running binary to litespm.old (File in use)    │
│      On Unix: Atomic rename staging/litespm.new -> current binary      │
│   6. Verify New Binary Boots ('litespm --version')                     │
│   7. Clean up backup binary (litespm.old)                              │
└────────────────────────────────────────────────────────────────────────┘
```

**Current verification is SHA-256 only.** `internal/update/updater.go` fetches `SHA256SUMS.txt`, requires a matching entry for the platform binary, and refuses an update with no expected checksum (`refusing to apply an update with no expected SHA-256 checksum`). It performs **no** signature, cosign, or attestation verification. Because the checksum travels in the same release as the binary, it proves transit integrity, not publisher authenticity. Signed releases (cosign) are a target tracked in [31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) Phase 5 and specified in [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md); do not describe self-update as signature-verified until that lands.
