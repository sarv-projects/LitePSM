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
    engine (`cmd/litespm/main.go:1808-1809`, in `registerCoreHandlers`) and the skills CLI wires its own checker
    (`cmd/litespm/skills_policy.go:63-80`), which is why [STATUS.md](../STATUS.md) §1 records
    `internal/policy` as `WIRED`. In practice the install engine refuses an `Execute` that supplies
    neither a tree nor an archive source *before* it evaluates policy
    (`internal/install/engine.go:262` vs `:265-282`). The install-side gate does run on both paths
    that can succeed: an MCP install evaluates `package.install` plus `host.config.write` through a
    real engine before any host config is written (`cmd/litespm/install_mcp.go:226`, `:396-432`;
    `DecisionAsk` is accepted only when the human approval consumed for that exact plan is presented,
    and `deny` returns `ErrUnauthorized`), and a skill install builds the same engine and binds its
    `ask` to the same consumed approval (`cmd/litespm/install_skill.go:80`, `:163-175`). So the gate
    runs for every successful MCP and skill install, and any deny rule matching those declared
    effects is exercised there — not "for no successful install".

---

## 2. Artifact Extraction & Filesystem Safety

Third-party packages (skills, MCP distribution archives, plugins) are untrusted. Artifact extraction is centralized in `internal/artifact` and called from the install engine (`internal/install/engine.go:392` spooling, `:415` extraction). No production path currently supplies an archive to that engine (plugin installs fail closed with `LPSM-ARTIFACT-UNAVAILABLE`); skills install as files through `internal/skills`, which refuses symlinks and bounds the copied tree, and MCP servers install as a config entry with no artifact at all.

### 2.1 Extraction Safety Thresholds

| Limit | Value | Enforced | Evidence |
|---|---|---|---|
| Max download archive size | 256 MiB | **Yes** | `internal/artifact/extractor.go:21-22`; spool bound `:57-77`; on-disk stat bound `:92-98` |
| Max extracted tree size | 1 GiB | **Yes** | `extractor.go:24-25`; zip accumulates *declared* `UncompressedSize64` `:185-189`, tar accumulates header size `:291-294` |
| Max total extracted files | 20,000 | **Yes** | `extractor.go:27-28`; `:154-156` (zip), `:252-253` (tar) |
| Max single file size | 128 MiB | **Yes** | `extractor.go:30-31`; header check `:181-183` / `:287-289` plus a write-time bound `:357-379` |
| Max normalized path length | 1,024 bytes | **Yes** | `extractor.go:33-34`; `:319-321` |
| Max compression ratio (zip-bomb defense) | 100:1 | **No — `DESIGNED`** | No ratio check exists anywhere in `internal/artifact`. The size caps above are the enforced mitigation; the ratio check is a planned addition, not a present control. |
| Max HTTP redirects | 3 hops on the catalog and MCP clients, 5 on the artifact fetcher, 2 on the OAuth token client | **Yes for those four clients; `DESIGNED` for the update and agent-registry clients** | The catalog delegates its chain to the shared guard (`internal/catalog/client.go:197-203` → `egress.CheckRedirect`, `internal/egress/egress.go:401-430`: 3-hop cap, https→http refusal, no credentials, default-443-port refusal, and a public-address re-validation of every hop's destination). The remote-MCP transports get the same guard with a same-origin requirement: `egress.WrapClient` is applied inside every `ConnectStreamableHTTP` / `ConnectLegacy` call (`internal/mcpclient/client_2026.go:66`, `client_legacy.go:50`; policy at `client_2026.go:47-57`). The artifact fetcher sets its own `CheckRedirect` (`internal/artifact/fetcher.go:139-148`, `MaxRedirects` default 5) and the OAuth broker refuses the third hop (`internal/auth/broker.go:39-50`, `len(via) >= 3`). `internal/update/updater.go:98`, `cmd/litespm/main.go:314` (`http.DefaultClient`) and `internal/agent/registry.go:36` set none, so Go's 10-hop default applies there. Cross-origin hops are *allowed* on the catalog path (CDN edges move) but each hop is re-checked, and the remote-MCP policy refuses an origin change outright; "same origin; cross-origin re-authorization" as such is not implemented. |

`LPSM-CAS-ARCHIVE-SLIP` (`internal/domain/errors.go:247-249`) is the error code every
extraction rejection above produces; no `LPSM-ARTIFACT-*` constant exists in `internal/domain`.

### 2.2 Prohibited Archive Artifacts

The extraction engine rejects the entire archive if any of the following are detected:

1.  **Directory Traversal:** paths containing `..`, un-normalized slashes, or trailing dots — `extractor.go:318-343` plus the staging-prefix assertion `:168-171` / `:275-278`.
2.  **Absolute & Escape Paths:** paths starting with `/`, `\`, a Windows drive letter, a UNC path, or any `:` (`extractor.go:329-342`).
3.  **Special File Types:** symlinks, hardlinks, named pipes (FIFOs), sockets, and device nodes — `extractor.go:163-166` (zip), `:270-273` (tar), plus a pre-write `Lstat` symlink sweep `:196-201`.
4.  **Case Collisions:** multiple files whose paths collide on case-insensitive filesystems — `extractor.go:345-352`.
5.  **Digest Discrepancies:** rejected by the **install engine**, not the extractor: the spooled archive digest is compared to the plan's declared artifact digest (`internal/install/engine.go:399-402`), and the canonical tree digest is recomputed from what actually landed in the CAS before commit (`:530-535`, `LPSM-VERIFY-CHECKSUM-MISMATCH`).

Skill directories have their own bounds and are copied by `internal/skills`, not `internal/artifact`:
32 MiB per skill tree (`internal/skills/install.go:22-25`), https-only sources (`:107-113`), and an
outright refusal of symlinks, FIFOs, devices and sockets with a regular-file re-check against TOCTOU
swaps (`:330-366`).

---

## 3. Network Fetch Policy & SSRF Prevention

### 3.1 Enforced today

*   **HTTPS on skill sources:** `internal/skills/install.go:107-113` accepts `https://` git URLs only; `http`, `ssh`, and `file:` are refused.
*   **HTTPS on self-update downloads:** `validateHTTPSURL` (`internal/update/updater.go:279-291`) is applied to every release-asset URL (`:189`, `:199`), the checksum manifest (`:215`), and each fetch (`:240`), so a non-`https` download URL is refused. Redirects of those fetches still follow Go's default policy — see §3.2.
*   **HTTPS + destination filtering on catalog fetches:** every catalog request URL must be `https` (loopback `http` is exempt for hermetic tests) and must carry no credentials — `egress.CheckURL` behind `checkCatalogURL` (`internal/catalog/client.go:208-210` → `internal/egress/egress.go:306-326`). The transport resolves the host, classifies every returned address and refuses any non-public one, then dials the checked address directly (rebinding-proof) with the environment proxy disabled and `DialTLSContext` cleared so TLS cannot bypass the checked dial (`egress.ConfigureTransport` / `egress.DialContext`, `internal/egress/egress.go:469-529`); the destination is re-checked on every request (`internal/catalog/client.go:171-173` → `egress.go:554-567`); and each redirect hop is re-run through the same rules — 3-hop cap, https-only, no credentials, default-443-port only, public-address resolution (`internal/catalog/client.go:197-203` → `egress.go:401-430`). A config-supplied private registry host is the one deliberate exception (`configuredPrivateHost`, `internal/catalog/client.go:175-177` → `egress.ConfiguredPrivateHost`, `egress.go:243-257`). The rules themselves live in **`internal/egress`**, extracted from `internal/catalog` so the MCP transports cannot grow a second, drifting copy of them ([ARCH/07](07-DECISIONS.md) D-030); `internal/catalog` keeps only thin named delegation points (`client.go:72-210`).
*   **HTTPS + destination filtering on remote (URL) MCP endpoints:** the same guard under the remote policy — https-only with a loopback-http exception (a local dev endpoint and the hermetic test doubles keep working), no URL credentials, at most 3 redirect hops, **same-origin redirects only** (a remote client may carry `Authorization`/session headers a cross-host hop would forward to a third party — `Policy.SameOriginRedirects`, `internal/egress/egress.go:70-74`), RFC1918/ULA refused because the remote policy leaves `AllowPrivate` unset (`egress.go:76-80`, `internal/mcpclient/client_2026.go:52`), checked-IP dial, proxy off, `DialTLSContext` cleared (`client_2026.go:47-57`). `ConnectStreamableHTTP` and `ConnectLegacy` wrap even a caller-supplied client instead of trusting it (`client_2026.go:66`, `client_legacy.go:50`), and `internal/discover` passes a nil client so the fail-closed default builds (`internal/discover/discover.go:673-692`). Plan time runs the static half **before a plan or an approval prompt exists**: `egress.CheckURL` plus literal-IP classification in `checkRemoteEndpoint` (`cmd/litespm/install_mcp.go:150-165`), called from `planRuntimeGates` (`cmd/litespm/main.go:2688`) and re-run on sealed-plan replay (`install_mcp.go:229`), so an unsafe URL is refused with `LPSM-EGRESS-BLOCKED` before it can be approved or written to a config. Plan time performs no DNS (a config write is not a connection): a hostname endpoint is classified when the guard dials. A private (RFC1918/ULA) **literal** endpoint is refused at plan time and there is no opt-in flag for it yet — an open gap, not an oversight.
*   **HTTPS + dial-time SSRF refusal on artifact fetches:** `internal/artifact/fetcher.go` refuses non-`https` (`:139-141`), caps the chain at 5 hops (`:143-146`), and refuses loopback/link-local/private/unspecified/multicast destinations at dial time; the download is spooled under a byte bound and its digest is fail-closed (`ARCH/17` §4). No production caller yet.
*   **HTTPS on the ACP agent registry:** `internal/agent/registry.go:47-49` accepts `https` (and `http` for loopback only).
*   **Policy-level SSRF invariant:** a declared `network.outbound` effect whose target names loopback, RFC 1918, link-local, or metadata space is denied **unconditionally** — the check is structural (`isRestrictedNetworkTarget`, `internal/policy/engine.go:215-236`, prefix list `:117-129`) and runs as invariant 3 before user rules (`:376-396`), so provenance cannot declare past it. It inspects *declared effect targets*, not HTTP traffic, and only runs on paths that reach the policy engine (§1).

### 3.2 Still not enforced (`DESIGNED`)

The controls above now cover the catalog, artifact, skills, self-update-URL, agent-registry,
remote-MCP and policy-declaration paths. What remains open is **destination and redirect policy on
the self-update fetches, and cross-origin re-authorization anywhere**:

*   **Self-update redirect hops.** `internal/update/updater.go:98` and `cmd/litespm/main.go:314` (`http.DefaultClient`) follow Go's default redirect policy: the *initial* URL is https-checked (§3.1), a redirect target is not, and there is no IP-range check on either.
*   **Cross-origin re-authorization.** No client re-authorizes a cross-origin hop; the catalog client instead re-validates the destination of every hop (§3.1), which is a different and narrower control, and the remote-MCP client refuses an origin change outright rather than re-authorizing it.

### 3.3 User-Configured Local Sources

Users may explicitly configure local filesystem paths or private network Git repositories. The rule — **public catalog records must never redirect a client to a private network destination** — is enforced for the catalog client itself: every hop is resolved and refused unless it is a public address (`egress.CheckRedirect` → `resolvePublic`, `internal/egress/egress.go:401-430` and `:382-393`, which re-checks unconditionally), and the configured private-host exception (`egress.ConfiguredPrivateHost`, `internal/catalog/client.go:175-177`) can only name the registry base URL the user configured — never a destination a catalog record supplies. The remote-MCP client applies the same unconditional per-hop public re-validation, plus same-origin refusal (§3.1). It is still *not* enforced on the self-update fetches (§3.2), whose redirect targets are neither destination-checked nor re-validated.

---

## 4. Nested MCP Capability Clamping

When the LiteSPM Daemon acts as an MCP client connecting to downstream local or remote MCP servers, it **clamps** its advertised client capabilities to protect user privacy. The clamps are implemented in `internal/mcpclient`, whose first production importer is `internal/discover` ([STATUS.md](../STATUS.md) §4) — and that one importer reaches **all three** profiles: the **stdio** profile with `"capabilities": {}` for a launched server, and the HTTP and legacy profiles when an installed entry carries a remote endpoint (`dialRemote`, `internal/discover/discover.go:673-692`). What no *published* listing can trigger yet is the remote half: no dataset row carries a `url` (§3.1), so only a locally built dataset or a test install puts a remote profile on the wire. Item-by-item:

1.  **Roots Forwarding Disabled:** `DefaultClientCaps()` returns `Roots: nil` (`internal/mcpclient/types.go:78-84`); the modern client attaches it to request metadata (`client_2026.go:106`) and the stdio client initializes with `"capabilities": {}` (`client_stdio.go:47`). A downstream provider cannot query the user's workspace structure through LiteSPM.
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
    Both tuples are read back and compared in `policy.checkCapabilityGrant` (`internal/policy/engine.go:494-560`).
3.  **Drift Invalidation:** schema drift → `LPSM-PROVIDER-SCHEMA-DRIFT`, local tree drift → `LPSM-PROVIDER-CODE-DRIFT`, remote origin/version drift → `LPSM-PROVIDER-ENDPOINT-DRIFT` / `LPSM-PROVIDER-CODE-DRIFT` (`internal/policy/engine.go:527-560`; codes at `internal/domain/errors.go:12-14`). A drifted capability is denied until fresh approval is granted.

**Reachability caveat (partly closed).** Every grant row must exist in `capability_grants`
before any of this can fire, and the writers are no longer test-only: `litespm grant` writes
`capability_grants` (`cmd/litespm/grant.go:87`), `internal/discover` writes `providers` and
`capabilities` (`internal/discover/discover.go:246,273`), and MCP installs write
`host_registrations` (`cmd/litespm/install_mcp.go:363`, `copy.go:970`) — so a real install or
invocation can now reach a drift check. The one table still without a production writer is
`audit_events` (`RecordAuditEvent`, `internal/state/repositories.go:1165`): a drift denial is
enforced but not recorded. The code is `IMPLEMENTED`; the audited loop is not yet closed.

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
*   **Retrieval at launch is `IMPLEMENTED`, not `WIRED`.** `secrets.ResolveLaunchSecrets` resolves references to plaintext in memory (`internal/secrets/types.go:77-105`), and the supervisor would splice such values into a child environment (`internal/provider/supervisor.go:183-185`). Neither has a production caller today: no code populates `LaunchSpec.SecretEnv`, and although `internal/discover` writes `providers` rows on a `capabilities refresh` (`SaveProvider`, §5), nothing writes one **at install time**, so a fresh machine has nothing to resolve at launch ([STATUS.md](../STATUS.md) §4, "Not yet true, and not claimed").

---

## 10. Honest Security Claims

1.  **Not an OS Sandbox:** LiteSPM provides protocol mediation, schema validation, and authorization gating. Running a local stdio MCP provider executes real code on the host machine under the user's OS privileges. No sandbox tier is declared yet (`DESIGNED` — [STATUS.md](../STATUS.md) §5, [ARCH/31 §5](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#5-adjudication-of-the-60-proposals) #22), and `doctor` reports process supervision only. Claiming any isolation above the tier `doctor` actually reports is a forbidden claim ([ARCH/31 §13](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#13-forbidden-claims-binding) #3).
2.  **Credentials Stay Client-Side:** LiteSPM hosted servers do not receive user credentials. However, when an agent invokes a downstream provider, that specific provider receives the authorized token necessary to fulfill the request.
3.  **Untrusted Skill Text:** Installing a skill does not run code, but skill Markdown instructions can influence model behavior (prompt injection). Skill text is presented to models as untrusted data with clear provenance metadata.
4.  **Supply chain — integrity-checked, with signature verification when the release signs.** `litespm self-update` verifies a SHA-256 checksum and refuses on mismatch or missing entry (`WIRED`), then verifies the release's keyless Sigstore bundle through `verifyDownloadedUpdate` (`cmd/litespm/main.go:391-438`; [STATUS.md](../STATUS.md) §1): a published bundle that fails — or cannot be fetched — aborts, a release that publishes none falls back to checksum-only with a stated note, and `LITESPM_REQUIRE_SIGNED_UPDATE=1` refuses it. No published tag carries a bundle yet, so the branch every update takes today is still checksum-only. TUF-style catalog signing, advisories/quarantine and the remaining SBOM/SLSA provenance work are `DESIGNED` in [ARCH/36](36-ENTERPRISE-POLICY-AND-AUDIT.md) and recorded as such in [SECURITY.md](../SECURITY.md).
5.  **Connectors do not exist.** `internal/connector` was **deleted** under decision `D1` for having zero production importers; [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md) is a design record only. The credential-custody design described there (connection handle instead of a secret, pinned target host, private/loopback/link-local refusal after DNS resolution, stripping of caller-supplied `Authorization`/`Cookie`/`Proxy-Authorization` headers) carries **no shipped guarantee**, and any connector executor must first re-decide `D1` ([STATUS.md](../STATUS.md) §4).
