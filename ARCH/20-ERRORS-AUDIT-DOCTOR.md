# Errors, Audit & Doctor Framework

## 1. Stable Machine Error Taxonomy

Every error returned by LiteSPM across CLI, IPC, and Bridge MCP interfaces conforms to a structured taxonomy:

```text
┌─────────────────────┬──────────────────────────────────────────────────────────────────┐
│ Category Prefix     │ Functional Domain                                                │
├─────────────────────┼──────────────────────────────────────────────────────────────────┤
│ LPSM-CATALOG-*      │ Remote catalog fetch, release manifest, and cache failures       │
│ LPSM-SOURCE-*       │ Upstream source adapter ingestion and manifest parsing errors    │
│ LPSM-RESOLVE-*      │ Dependency graph resolution, cycles, and version conflicts       │
│ LPSM-PLAN-*         │ Plan expiration, precondition failures, and hash mismatches      │
│ LPSM-APPROVAL-*     │ Approval token invalidation, missing actor, or rejected consent  │
│ LPSM-ARTIFACT-*     │ Archive corruption, size limit breaches, and path traversal      │
│ LPSM-CAS-*          │ Content-addressed store: archive slips and unsafe extraction     │
│ LPSM-CORE-*         │ Core internal failures (e.g. an empty skill body from the daemon) │
│ LPSM-INSTALL-*      │ Two-phase staging, atomic rename, and SQLite commit failures     │
│ LPSM-STATE-*        │ SQLite locks, foreign key violations, and database corruption     │
│ LPSM-HOST-*         │ Host configuration discovery, parse errors, and name collisions  │
│ LPSM-PROVIDER-*     │ Provider process supervision, child exit codes, and schema drift │
│ LPSM-MCP-*          │ MCP protocol handshake, JSON-RPC framing, and transport timeouts │
│ LPSM-AUTH-*         │ Vault access failures, OAuth state mismatch, and expired tokens  │
│ LPSM-POLICY-*       │ Hard invariant denials and policy rule rejections                │
│ LPSM-CONFIG-*       │ Local config.toml syntax errors and missing environment paths     │
│ LPSM-IPC-*          │ Local IPC transport, daemon-unreachable, and framing failures     │
│ LPSM-DOMAIN-*       │ Invalid canonical IDs and domain-model violations                 │
│ LPSM-VERIFY-*       │ Checksum / digest verification failures                           │
│ LPSM-INTERNAL-*     │ Unhandled panics and operating system kernel errors              │
└─────────────────────┴──────────────────────────────────────────────────────────────────┘
```

> The table is the contract. The set actually constructed in the tree today is
> discoverable with `grep -rhoE 'LPSM-[A-Z0-9-]+' internal/ cmd/ | sort -u`; some
> categories above (notably `LPSM-CATALOG-*`, `LPSM-SOURCE-*`, `LPSM-APPROVAL-*`,
> `LPSM-INSTALL-*`, `LPSM-MCP-*`, `LPSM-CONFIG-*`, `LPSM-INTERNAL-*`) are reserved
> but not yet emitted. Approval failures currently surface as
> `LPSM-POLICY-APPROVAL-CONSUMED` / `LPSM-POLICY-APPROVAL-EXPIRED`, and install
> failures as `LPSM-PLAN-*`, `LPSM-POLICY-*`, `LPSM-STATE-*`, `LPSM-CAS-*` or
> `LPSM-VERIFY-*` — there is no `LPSM-APPROVAL-*` or `LPSM-INSTALL-*` code in the
> tree. `LPSM-ARTIFACT-*` is in the same reserved state: `SpoolDownloadBounded`
> rejects oversized downloads with unstructured text (`exceeds maximum download
> limit`), extraction failures are `LPSM-CAS-ARCHIVE-SLIP`, and the literal
> `LPSM-ARTIFACT-PATH-TRAVERSAL` appears **only** inside a test assertion
> (`internal/artifact/artifact_test.go:104`); §1.2 records the emission status of
> every code that does appear in the tree.

### 1.1 Representative Machine Error Codes
*   `LPSM-PLAN-STALE`: Preconditions or source digests changed between plan creation and commit.
*   `LPSM-POLICY-UNAUTHORIZED`: Operation denied by policy.
*   `LPSM-CAS-ARCHIVE-SLIP`: Archive contains directory traversal (`../`), an absolute path, or another unsafe entry — the extractor's single failure error (`internal/artifact/extractor.go`).
*   `LPSM-CORE-INTERNAL`: Core invariant failure, e.g. a skill answered with no instruction body.
*   `LPSM-PROVIDER-SCHEMA-DRIFT`: Downstream tool schema changed; existing grant invalidated.
*   `LPSM-HOST-CONFIG-NOT-FOUND`: No host configuration file was located for the adapter. *(Defined at `errors.go:261` but currently has **zero callers** — §1.2; host adapters return plain `fmt.Errorf` today.)*

### 1.2 Full Inventory of Codes in the Tree (emission status)

Verified 2026-10-05 against `internal/domain/errors.go` and every non-test call
site. The rows below are the `LPSM-*` codes this inventory tracks; each states where
the code is defined and whether production code actually emits it today.

| Code | Defined | Emitted in production? |
|---|---|---|
| `LPSM-POLICY-UNAUTHORIZED` | `errors.go:11,201` | Yes — `install/engine.go:223,226` (policy gate + approval required) |
| `LPSM-POLICY-APPROVAL-CONSUMED` | `errors.go:177` | Yes — `state/repositories.go:125` (one-time consumption) |
| `LPSM-POLICY-APPROVAL-EXPIRED` | `errors.go:189` | Yes — `state/repositories.go:128` |
| `LPSM-PLAN-STALE` | `errors.go:113` | Yes — `install/engine.go:104,112` (planHash mismatch) |
| `LPSM-PLAN-EXPIRED` | `errors.go:126` | Yes — `install/engine.go:116` |
| `LPSM-RESOLVE-CONFLICT` | `errors.go:152` | Yes — `resolver/resolver.go:37,95,126,168` |
| `LPSM-RESOLVE-CYCLE` | `errors.go:165` | Yes — `resolver/resolver.go:73,201` |
| `LPSM-STATE-NOT-FOUND` | `errors.go:214` | Yes — 18 production `ErrNotFound` call sites |
| `LPSM-STATE-CONFLICT` | `errors.go:227` | Yes — 6 production `ErrStateConflict` call sites |
| `LPSM-DOMAIN-INVALID-ID` | `errors.go:100` | Yes — 5 production `ErrInvalidIdentifier` call sites |
| `LPSM-VERIFY-CHECKSUM-MISMATCH` | `errors.go:236` | Yes — 4 production call sites |
| `LPSM-CAS-ARCHIVE-SLIP` | `errors.go:249` | Yes — 16 production `ErrArchiveSlip` call sites |
| `LPSM-CORE-INTERNAL` | `errors.go:296` | Yes — 5 `ErrInternal` call sites + `bridge/shim.go:426` |
| `LPSM-IPC-DAEMON-UNREACHABLE` | `errors.go:274` | Yes — `bridge/shim.go:581` (fail-closed bridge; the `errors.go` constructor itself has no callers) |
| `LPSM-AUTH-VAULT-UNAVAILABLE` | `errors.go:15,284` | Yes — 7 sites in `internal/secrets` (fail-closed vault open) |
| `LPSM-PROVIDER-SCHEMA-DRIFT` | `errors.go:12,138` | Partly — emitted as a **policy reason code** at `policy/engine.go:287`; the `ErrSchemaDrift` constructor is test-only (`domain_test.go:254`) |
| `LPSM-PROVIDER-CODE-DRIFT` | `errors.go:13` | Partly — policy reason code at `policy/engine.go:296,314` |
| `LPSM-PROVIDER-ENDPOINT-DRIFT` | `errors.go:14` | Partly — policy reason code at `policy/engine.go:305` |
| `LPSM-AUTH-OAUTH-STATE-MISMATCH` | `errors.go:16` | Code path exists (`auth/loopback.go:118`) but `internal/auth` has **no production consumer** — not reachable end-to-end |
| `LPSM-AUTH-CALLBACK-TIMEOUT` | `errors.go:17` | Same as above (`auth/loopback.go:161`) — not reachable end-to-end |
| `LPSM-HOST-CONFIG-NOT-FOUND` | `errors.go:261` | **No** — `ErrHostConfigNotFound` has zero callers in the tree; `internal/host` returns plain `fmt.Errorf` instead |
| `LPSM-ARTIFACT-PATH-TRAVERSAL` | *(none)* | **No** — never constructed; the literal exists only in `internal/artifact/artifact_test.go:104` |
| `LPSM-INSTALL-TARGET-UNAVAILABLE` | `errors.go:281` | No — nothing to install into (no host set up); `cmd/litespm/install_mcp.go`. Mapped to `InvalidParams` |
| `LPSM-NAME-CONFLICT` | `errors.go:267` | No — an install refuses to overwrite a name the user already registered; `internal/host/entry_install.go` and `cmd/litespm/install_mcp.go`. Mapped to `InvalidParams`, because the remedy (`--force`) belongs to the caller |
| `LPSM-ARTIFACT-UNAVAILABLE` | `errors.go:288` | Yes — no-fetchable-artifact paths: `cmd/litespm/install_skill.go` (skill source gaps), `cmd/litespm/main.go` (unsupported kind, missing runtime descriptor), `internal/artifact/fetcher.go` (missing locator or archive type) |
| `LPSM-EGRESS-BLOCKED` | `errors.go:283` | Yes — from the shared guard `internal/egress` (catalog sync, remote-MCP dials in `internal/mcpclient` via `internal/discover`, and plan-time `checkRemoteEndpoint` in `cmd/litespm/install_mcp.go`), plus `internal/artifact/fetcher.go` (non-HTTPS scheme, URL credentials, SSRF ranges, redirect cap/downgrade). The artifact fetcher itself still has **no production caller yet** (no artifact locators in the catalog) |

---

## 2. CLI Exit Code Contract

CLI exit codes follow stable numerical ranges to allow robust shell scripting. The
category codes **10–70 are implemented** for `litespm doctor`: each failing check
carries a category and `doctorCategoryExitCode` maps it to the documented code,
worst-wins (`cmd/litespm/main.go:935-982`). Other commands currently return `0`,
`1` (internal failure), or `2` (usage error); the table below is the contract they
should adopt as they gain categorized failures.

| Exit Code | Classification | Meaning |
|---|---|---|
| **0** | `SUCCESS` | Operation completed successfully. |
| **1** | `INTERNAL_FAILURE` | Unhandled error or panic. |
| **2** | `USAGE_ERROR` | Invalid flags, missing required arguments, or unknown command. |
| **10** | `CATALOG_ERROR` | Catalog unreachable, manifest digest mismatch, or offline error. |
| **20** | `RESOLVE_ERROR` | Dependency conflict, circular reference, or unresolvable version. |
| **30** | `APPROVAL_DENIED` | Policy denied execution or user rejected installation prompt. |
| **40** | `INSTALL_ERROR` | Archive download failed, safety limit exceeded, or staging error. |
| **50** | `PROVIDER_ERROR` | Provider crashed on startup, protocol mismatch, or auth failure. |
| **60** | `HOST_ERROR` | Unable to locate or safely modify agent configuration file. |
| **70** | `STATE_ERROR` | SQLite database locked, migration failed, or recovery needed. |

---

## 3. Local Audit Log & Redaction

Security-relevant actions are recorded in the `audit_events` table in SQLite:
*   **Logged Events:** Source addition, package installation/update/removal, host bridge registration, capability approval grants/revocations, provider process starts/stops.
*   **Redaction Filters:** Before logging, all metadata objects pass through a redaction filter that strips authorization headers, token values, passwords, and private SSH keys.

---

## 4. The `doctor` Diagnostic Framework

Running `litespm doctor` executes 10 non-mutating system health checks:

```text
$ litespm doctor
[✓] SQLite state database integrity verified (WAL mode active).
[✓] Incomplete operation journal: clean (no orphaned operations).
[✓] Content-Addressed Store: 14 trees verified; 0 missing digests.
[✓] Host configurations:
    ├── Claude Code: connected (/Users/username/.claude.json)
    └── OpenAI Codex: connected (/Users/username/.codex/config.json)
[✓] OS Secret Vault: functional (macOS Keychain accessible).
[✓] Provider runtimes: Node.js (v20.10.0), Python (v3.11.4) detected.
[!] Cached catalog release: 7 days old. Run 'litespm refresh' to update.
[✓] Local disk space: 42.5 GiB available in DATA_ROOT.
[✓] Host configuration backups: 3 backups stored safely.

Status: HEALTHY (1 informational recommendation)
```

### The `--repair` Flag
If inconsistencies exist (e.g., orphaned staging files or dangling CAS entries), `litespm doctor --repair` generates an explicit repair plan, displays it for user confirmation, and cleans up the anomalies.
