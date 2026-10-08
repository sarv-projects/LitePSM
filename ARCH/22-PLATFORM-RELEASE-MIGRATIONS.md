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
│   4. Verify SHA-256 (fail-closed) + Sigstore bundle if published       │
│   5. On Windows: Rename running binary to litespm.old (File in use)    │
│      On Unix: Atomic rename staging/litespm.new -> current binary      │
│   6. Verify New Binary Boots ('litespm --version')                     │
│   7. Clean up backup binary (litespm.old)                              │
└────────────────────────────────────────────────────────────────────────┘
```

**Verification is SHA-256 fail-closed plus a keyless signature check.** `internal/update/updater.go` fetches `SHA256SUMS.txt`, requires a matching entry for the platform binary, and refuses an update with no expected checksum (`refusing to apply an update with no expected SHA-256 checksum`). On top of that, `verifyDownloadedUpdate` (`cmd/litespm/main.go:391-438`) looks for the release's Sigstore bundle (`<asset>.sigstore.json`, published by `release.yml` for every asset since commit `4cfa14b`) and verifies it against the downloaded bytes before they are staged for install: a bundle that is published and fails verification, or that cannot be fetched, always aborts; a release that publishes no bundle at all falls back to checksum-only **with a stated note**, and `LITESPM_REQUIRE_SIGNED_UPDATE=1` turns that fallback into a hard refusal. Because a checksum travels in the same release as the binary it proves transit integrity, not publisher authenticity — the bundle is what answers "who produced this". Caveat of record: no `v*` tag contains `4cfa14b` (checked `git tag --contains`), so **no published release carries a bundle yet** and every update performed today exercises the checksum-only branch. Signed *catalog* releases remain `DESIGNED` ([31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) Phase 5, specified in [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md)).
