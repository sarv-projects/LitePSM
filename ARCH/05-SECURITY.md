# Security, Trust, and Credentials

> **Precedence and status.** [ARCH/00](00-INDEX.md#1-document-status--precedence) ranks this
> document first among ARCH specifications. The evidence states used below (`DESIGNED`,
> `IMPLEMENTED`, `WIRED`, `TESTED`, `VERIFIED`, `SHIPPED`) are defined by
> [ARCH/31 §2](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide),
> and [STATUS.md](../STATUS.md) is the single source of truth for the current state of any
> subsystem. A control that is specified but not enforced by code is marked `DESIGNED`; a control
> whose code exists but has no production caller is marked `IMPLEMENTED`. Neither is described as
> shipped.

## 1. Core Security Invariant

LiteSPM's hosted catalog service is strictly a discovery index. It is **never** a secret store, execution environment, MCP proxy, or authentication authority. Downstream service credentials and runtime tool execution remain strictly on the user's local machine or with the user's chosen remote provider.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Hosted Catalog (Untrusted)                      │
│                                                                        │
│   Public JSON · Static Schemas · Publisher Manifests · Search Index    │
│   (Zero credentials · Zero execution · Zero tool proxying)             │
└────────────────────────────────────────────────────────────────────────┘
                                │ HTTPS (Read-Only)
════════════════════════════════╪═════════════════════════════════════════
                     PRIVILEGE BOUNDARY (Local Machine)
                                │
                                ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        LiteSPM Local Control Plane                     │
│                                                                        │
│   Daemon owns:                                                         │
│     ├── OS secret vault (DPAPI / Keychain / secret-tool master key)    │
│     ├── Process supervision (Job Object kill-on-close on Windows;      │
│     │   process group + parent-death signal / watchdog on Unix)        │
│     ├── Effect policy on the install and skills paths                  │
│     └── Content-addressed local store & operation journal              │
└────────────────────────────────────────────────────────────────────────┘
```

Two honesty notes bound this diagram:

*   **No cgroups, no resource caps.** The Windows Job Object is created with
    `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` only (`internal/provider/isolation_windows.go:70-79`); Unix
    isolation is a process group plus `PR_SET_PDEATHSIG` / a watchdog control pipe
    (`internal/provider/isolation_unix.go:11-31`). No memory, CPU, or I/O limit is applied, and no
    sandbox tier is declared — that declaration is `DESIGNED`
    ([STATUS.md](../STATUS.md) §5; [ARCH/31 §5](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#5-adjudication-of-the-60-proposals) #22).
*   **Policy reaches only paths that can run.** The daemon wires the policy engine into the install
    engine (`cmd/litespm/main.go:1235-1236`) and the skills CLI wires its own checker
    (`cmd/litespm/skills_policy.go:63-80`), which is why [STATUS.md](../STATUS.md) §1 records
    `internal/policy` as `WIRED`. In practice the install engine refuses an `Execute` that supplies
    neither a tree nor an archive source *before* it evaluates policy
    (`internal/install/engine.go:204-207` vs `:209-232`), and the CLI install path constructs an
    engine with no policy at all (`cmd/litespm/main.go:843`) — so the install-side gate currently
    runs for no successful install.

---

## 2. Artifact Extraction & Filesystem Safety

Third-party packages (skills, MCP distribution archives, plugins) are untrusted. Artifact extraction is centralized in `internal/artifact` and called from the install engine (`internal/install/engine.go:322,345`). No production path currently supplies an archive to that engine (MCP/plugin installs fail closed with `LPSM-ARTIFACT-UNAVAILABLE`); skills install as files through `internal/skills`, which refuses symlinks and bounds the copied tree.

### 2.1 Extraction Safety Thresholds

| Limit | Value | Enforced | Evidence |
|---|---|---|---|
| Max download archive size | 256 MiB | **Yes** | `internal/artifact/extractor.go:21-22`; spool bound `:57-77`; on-disk stat bound `:92-98` |
| Max extracted tree size | 1 GiB | **Yes** | `extractor.go:24-25`; zip accumulates *declared* `UncompressedSize64` `:185-189`, tar accumulates header size `:291-294` |
| Max total extracted files | 20,000 | **Yes** | `extractor.go:27-28`; `:154-156` (zip), `:252-253` (tar) |
| Max single file size | 128 MiB | **Yes** | `extractor.go:30-31`; header check `:181-183` / `:287-289` plus a write-time bound `:357-379` |
| Max normalized path length | 1,024 bytes | **Yes** | `extractor.go:33-34`; `:319-321` |
| Max compression ratio (zip-bomb defense) | 100:1 | **No — `DESIGNED`** | No ratio check exists anywhere in `internal/artifact`. The size caps above are the enforced mitigation; the ratio check is a planned addition, not a present control. |
| Max HTTP redirects | 3 hops, same origin; cross-origin re-authorization | **No — `DESIGNED`** | No `CheckRedirect` is set on any client (see §3.2). Do not cite this as enforced. |

`LPSM-CAS-ARCHIVE-SLIP` (`internal/domain/errors.go:247-249`) is the error code every
extraction rejection above produces; no `LPSM-ARTIFACT-*` constant exists in `internal/domain`.

### 2.2 Prohibited Archive Artifacts

The extraction engine rejects the entire archive if any of the following are detected:

1.  **Directory Traversal:** paths containing `..`, un-normalized slashes, or trailing dots — `extractor.go:318-343` plus the staging-prefix assertion `:168-171` / `:275-278`.
2.  **Absolute & Escape Paths:** paths starting with `/`, `\`, a Windows drive letter, a UNC path, or any `:` (`extractor.go:329-342`).
3.  **Special File Types:** symlinks, hardlinks, named pipes (FIFOs), sockets, and device nodes — `extractor.go:163-166` (zip), `:270-273` (tar), plus a pre-write `Lstat` symlink sweep `:196-201`.
4.  **Case Collisions:** multiple files whose paths collide on case-insensitive filesystems — `extractor.go:345-352`.
5.  **Digest Discrepancies:** rejected by the **install engine**, not the extractor: the spooled archive digest is compared to the plan's declared artifact digest (`internal/install/engine.go:329-336`), and the canonical tree digest is recomputed from what actually landed in the CAS before commit (`:403-417`, `LPSM-VERIFY-CHECKSUM-MISMATCH`).

Skill directories have their own bounds and are copied by `internal/skills`, not `internal/artifact`:
32 MiB per skill tree (`internal/skills/install.go:22-25`), https-only sources (`:98-107`), and an
outright refusal of symlinks, FIFOs, devices and sockets with a regular-file re-check against TOCTOU
swaps (`:330-366`).

---

## 3. Network Fetch Policy & SSRF Prevention

### 3.1 Enforced today

*   **HTTPS on skill sources:** `internal/skills/install.go:98-107` accepts `https://` git URLs only; `http`, `ssh`, and `file:` are refused.
*   **HTTPS on self-update downloads:** `internal/update/updater.go:265-271` refuses any non-`https` download URL.
*   **Policy-level SSRF invariant:** a declared `network.outbound` effect whose target names loopback, RFC 1918, link-local, or metadata addresses is denied unless its provenance is `user_classified` (`internal/policy/engine.go:149-169`). This inspects *declared effect targets*, not HTTP traffic, and it only runs on paths that reach the policy engine (§1).

### 3.2 Not enforced (`DESIGNED`)

The following are requirements, not properties of the current code:

*   **Scheme allow-listing for catalog and update clients.** `internal/catalog/client.go:38-41` and `internal/update/updater.go:97` build a plain `http.Client{Timeout}`. The scheme of `DefaultRegistryURL` (`internal/config/config.go:21`) is `https`, but nothing rejects a user- or config-supplied non-`https` registry URL.
*   **Private-network / cloud-metadata destination filtering.** No DNS resolution check, no IP-range classification, and no `169.254.169.254` guard exists in any Go HTTP client in this repository.
*   **Redirect caps.** The default Go redirect policy (up to 10 hops, cross-origin allowed) is in force everywhere.

Consequently §3.3 below must be read as a design intent until those three controls exist.

### 3.3 User-Configured Local Sources

Users may explicitly configure local filesystem paths or private network Git repositories. The rule — **public catalog records must never redirect a client to a private network destination** — is normative here but is *not yet enforced*; see §3.2.

---

## 4. Nested MCP Capability Clamping

When the LiteSPM Daemon acts as an MCP client connecting to downstream local or remote MCP servers, it **clamps** its advertised client capabilities to protect user privacy. The clamps are implemented in `internal/mcpclient`, which has **zero production importers** ([STATUS.md](../STATUS.md) §4), so the whole set is `IMPLEMENTED`, not `WIRED`:

1.  **Roots Forwarding Disabled:** `DefaultClientCaps()` returns `Roots: nil` (`internal/mcpclient/types.go:78-84`); the modern client attaches it to request metadata (`client_2026.go:66`) and the stdio client initializes with `"capabilities": {}` (`client_stdio.go:47`). A downstream provider cannot query the user's workspace structure through LiteSPM.
2.  **Model Sampling Disabled:** `Sampling` is likewise `nil` by default (`types.go:69,83`).
3.  **Elicitation Clamping:** **`DESIGNED`, not enforced.** `ClientCaps` has no elicitation field, and the only elicitation knowledge in the tree is the per-host `SupportsFormElicit` flag (`internal/host/types.go:16`; e.g. `internal/host/piagent.go:25`). Nothing outside `internal/host` reads that flag, so no code today blocks a downstream provider from proposing a prompt.
4.  **Stderr Bounding (corrected):** provider stderr is captured in a bounded **64 KiB** rotating ring buffer (`internal/provider/supervisor.go:17-18`, `:201`), not 1 MiB. **No redaction or sensitive-pattern stripping exists** — there is no scrubbing code in this repository. `provider.probe` returns at most the last 2 KiB of that tail (`internal/provider/supervisor.go:321-323`, `cmd/litespm/main.go:1648-1674`). Treat the buffer as unfiltered diagnostic data.

---

## 5. Capability Schema-Drift Defense

Downstream MCP providers may update their tool definitions dynamically between restarts. An attacker or modified server could alter an innocuous tool (`view_file`) into an effectful action (`overwrite_file`).

LiteSPM's defense is implemented in `internal/policy` and `internal/domain`:

1.  **Schema Fingerprint:** `domain.ComputeSchemaFingerprint` hashes the canonicalized tool input JSON Schema (`internal/domain/canonical.go:59-70`), mirroring `CanonicalizeJSON`.
2.  **Cryptographic Grant Binding:** user capability approvals are bound to strong identity tuples:
    *   **Local Stdio Providers:** `(capability_id, schemaFingerprint, casTreeDigest)`.
    *   **Remote HTTP Providers:** `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`.
    Both tuples are read back and compared in `policy.checkCapabilityGrant` (`internal/policy/engine.go:256-329`).
3.  **Drift Invalidation:** schema drift → `LPSM-PROVIDER-SCHEMA-DRIFT`, local tree drift → `LPSM-PROVIDER-CODE-DRIFT`, remote origin/version drift → `LPSM-PROVIDER-ENDPOINT-DRIFT` / `LPSM-PROVIDER-CODE-DRIFT` (`internal/policy/engine.go:283-317`; codes at `internal/domain/errors.go:11-14`). A drifted capability is denied until fresh approval is granted.

**Reachability caveat (`DESIGNED` at runtime).** Every grant row must exist in `capability_grants`
before any of this can fire. The writers for `providers`, `capabilities`, `capability_grants`,
`audit_events`, and `host_registrations` have **no non-test caller**
([STATUS.md](../STATUS.md) §4), so today no drift check can be triggered by a real install or
invocation. The code is `IMPLEMENTED`; the defense-in-depth loop is not yet closed.

---

## 6. Effect Provenance & Unknown Tools

LiteSPM tags every declared effect with an explicit classification provenance. The four values are
real enumerations, not documentation labels (`internal/domain/models.go:102-109`), and the policy
engine carries them on each declaration (`internal/policy/engine.go:65-70`):

*   `publisher_declared`: unverified claims from the package author's manifest.
*   `curated`: reviewed and certified by catalog maintainers.
*   `runtime_observed`: dynamically observed in automated sandbox test runs — **no such test-run
    pipeline exists yet**, so no catalog row carries this provenance.
*   `user_classified`: explicitly configured by the user in local policy. This is the only
    provenance that can lift the SSRF invariant (`internal/policy/engine.go:161`).

**Unknown tools.** LiteSPM never infers side effects from tool parameter names. An action whose
effects are not classified is denied by the default-deny tier (`internal/policy/engine.go:246-253`,
`DEFAULT_DENY_FAIL_CLOSED`); an effectful operation that reaches the human-in-the-loop tier returns
`ask` and requires interactive approval (`:212-234`). The correct statement is "fails closed by
default, escalates to an approval channel when classified as dangerous", not "an atomic capability
invocation".

---

## 7. Command Marketplace Source Prohibition

Marketplace manifests from several upstreams permit `command` sources that execute local shell commands to generate plugin files.

*   **V1 Rule:** LiteSPM rejects marketplace sources of type `command`.
*   **How it is enforced today:** by omission at ingestion — `internal/source/claude_marketplace.go:91-94` skips any plugin whose `source.IsCommand()`, and the deployed Python builder does the same (`scripts/build_full_catalog.py:401-424`). The policy engine independently denies any target reference prefixed `cmd:` or `command:` (`internal/policy/engine.go:126-134`).
*   **Correction:** no listing is annotated `supportedByLiteSPM: "no"` with the explanation `UNSUPPORTED_COMMAND_SOURCE`. Neither string exists in this repository; `supportedByLiteSPM` is a per-component `SupportLevel` enum (`internal/domain/models.go:74-81,281`) that the deployed catalog rows do not carry. The field, not the literal explanation, is the supported mechanism if annotation is added later.

Note the ingestion side is `IMPLEMENTED`, not `WIRED`: the eight `internal/source` adapters have
test-only callers ([STATUS.md](../STATUS.md) §2), and the deployed catalog is produced by
`scripts/build_full_catalog.py`.

---

## 8. Local Daemon IPC Permissions

Communication between Bridge shims, the CLI, and the local Daemon is secured against unauthorized local processes. This subsystem is `TESTED` ([STATUS.md](../STATUS.md) §1):

*   **Windows Security Descriptors:** the Named Pipe is initialized with a Discretionary Access Control List (DACL) granting access exclusively to the owning user's Security Identifier — `SDDL: D:(A;;GA;;;OW)` at `internal/ipc/transport_windows.go:13-16`. The endpoint is `\\.\pipe\litespm-daemon-<hash>`, where `<hash>` is the first 12 hex characters of a SHA-256 over the username (`internal/config/paths.go:60-64`); the legacy `litepsm-daemon-` prefix is still recognised for upgrade adoption (`internal/config/paths.go:28,37`).
*   **Unix / macOS Domain Sockets:** directory `0700`, socket file `0600` (`internal/ipc/transport_unix.go:12-31`), under `$XDG_RUNTIME_DIR/litespm/`.
*   **Bounded framing:** a single JSON-RPC message may not exceed 16 MiB (`internal/ipc/protocol.go:25-27`).

---

## 9. Operating System Secret Storage

LiteSPM never stores plaintext credentials in SQLite, in unencrypted files, in environment variables at rest, or in log streams.

*   **What the OS keystore holds: the vault master key, not each secret.** A 32-byte master key is generated once and protected by the platform keystore:
    *   **Windows:** DPAPI-encrypted `master.key` (`internal/secrets/store_windows.go:16-69`).
    *   **macOS:** a generic password in the login keychain via `/usr/bin/security` (`internal/secrets/store_darwin.go:15-44`).
    *   **Linux:** the FreeDesktop Secret Service via the `secret-tool` CLI, which must be on `PATH` (`internal/secrets/store_linux.go:16-42`).
*   **Fail-closed open.** On any platform without a usable keystore — including unsupported GOOS values — the store returns `LPSM-AUTH-VAULT-UNAVAILABLE` and no secrets are readable (`internal/secrets/store_linux.go:17-18`, `store_darwin.go:16-17`, `store_other.go:13`).
*   **Secret bytes live in an encrypted vault file.** Entries are AES-256-GCM ciphertext in `secrets/vault.enc` under the platform data root (`internal/secrets/vault_file.go:31-36`, `:78-92`). SQLite holds only the opaque reference `auth_profiles.secret_ref` (`internal/state/repositories.go:553`), formatted `secret:<namespace>/<key>` (`internal/secrets/types.go:11-38`).
*   **Retrieval at launch is `IMPLEMENTED`, not `WIRED`.** `secrets.ResolveLaunchSecrets` resolves references to plaintext in memory (`internal/secrets/types.go:77-105`), and the supervisor would splice such values into a child environment (`internal/provider/supervisor.go:183-185`). Neither has a production caller today: no code populates `LaunchSpec.SecretEnv`, and the `providers` table is never written by non-test code ([STATUS.md](../STATUS.md) §4).

---

## 10. Honest Security Claims

1.  **Not an OS Sandbox:** LiteSPM provides protocol mediation, schema validation, and authorization gating. Running a local stdio MCP provider executes real code on the host machine under the user's OS privileges. No sandbox tier is declared yet (`DESIGNED` — [STATUS.md](../STATUS.md) §5, [ARCH/31 §5](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#5-adjudication-of-the-60-proposals) #22), and `doctor` reports process supervision only. Claiming any isolation above the tier `doctor` actually reports is a forbidden claim ([ARCH/31 §13](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#13-forbidden-claims-binding) #3).
2.  **Credentials Stay Client-Side:** LiteSPM hosted servers do not receive user credentials. However, when an agent invokes a downstream provider, that specific provider receives the authorized token necessary to fulfill the request.
3.  **Untrusted Skill Text:** Installing a skill does not run code, but skill Markdown instructions can influence model behavior (prompt injection). Skill text is presented to models as untrusted data with clear provenance metadata.
4.  **Supply chain — integrity-checked, not signed.** `litespm self-update` verifies a SHA-256 checksum and refuses on mismatch or missing entry (`WIRED`, **no signature check**; [STATUS.md](../STATUS.md) §1). Signed releases, SBOM generation, Sigstore/SLSA provenance, advisories/quarantine, and TUF-style catalog signing are all `DESIGNED` in [ARCH/36](36-ENTERPRISE-POLICY-AND-AUDIT.md) and are recorded as such in [SECURITY.md](../SECURITY.md).
5.  **Connectors do not exist.** `internal/connector` was **deleted** under decision `D1` for having zero production importers; [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md) is a design record only. The credential-custody design described there (connection handle instead of a secret, pinned target host, private/loopback/link-local refusal after DNS resolution, stripping of caller-supplied `Authorization`/`Cookie`/`Proxy-Authorization` headers) carries **no shipped guarantee**, and any connector executor must first re-decide `D1` ([STATUS.md](../STATUS.md) §4).
