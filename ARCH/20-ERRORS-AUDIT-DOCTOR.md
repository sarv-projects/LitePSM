# Errors, Audit & Doctor Framework

## 1. Stable Machine Error Taxonomy

Every error returned by LitePSM across CLI, IPC, and Bridge MCP interfaces conforms to a structured taxonomy:

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
│ LPSM-INSTALL-*      │ Two-phase staging, atomic rename, and SQLite commit failures     │
│ LPSM-STATE-*        │ SQLite locks, foreign key violations, and database corruption    │
│ LPSM-HOST-*         │ Host configuration discovery, parse errors, and name collisions  │
│ LPSM-PROVIDER-*     │ Provider process supervision, child exit codes, and schema drift │
│ LPSM-MCP-*          │ MCP protocol handshake, JSON-RPC framing, and transport timeouts │
│ LPSM-AUTH-*         │ Vault access failures, OAuth state mismatch, and expired tokens  │
│ LPSM-POLICY-*       │ Hard invariant denials and policy rule rejections                │
│ LPSM-CONFIG-*       │ Local config.toml syntax errors and missing environment paths     │
│ LPSM-INTERNAL-*     │ Unhandled panics and operating system kernel errors              │
└─────────────────────┴──────────────────────────────────────────────────────────────────┘
```

### 1.1 Representative Machine Error Codes
*   `LPSM-PLAN-STALE`: Preconditions or source digests changed between plan creation and commit.
*   `LPSM-APPROVAL-UNAVAILABLE`: Operation requires user consent, but host lacks an elicitation channel.
*   `LPSM-ARTIFACT-UNSAFE-PATH`: Archive contains directory traversal (`../`) or absolute paths.
*   `LPSM-PROVIDER-SCHEMA-DRIFT`: Downstream tool schema changed; existing grant invalidated.
*   `LPSM-HOST-NAME-COLLISION`: Target agent configuration already has an unmanaged `litepsm` entry.

---

## 2. CLI Exit Code Contract (target — current CLI exits 0/1 only)

CLI exit codes follow stable numerical ranges to allow robust shell scripting. Implementation note: `cmd/litepsm/*.go` currently calls `os.Exit(1)` for every failure mode, so distinct codes are not yet observable; the table below is the contract to implement:

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

Running `litepsm doctor` executes 10 non-mutating system health checks:

```text
$ litepsm doctor
[✓] SQLite state database integrity verified (WAL mode active).
[✓] Incomplete operation journal: clean (no orphaned operations).
[✓] Content-Addressed Store: 14 trees verified; 0 missing digests.
[✓] Host configurations:
    ├── Claude Code: connected (/Users/username/.claude.json)
    └── OpenAI Codex: connected (/Users/username/.codex/config.json)
[✓] OS Secret Vault: functional (macOS Keychain accessible).
[✓] Provider runtimes: Node.js (v20.10.0), Python (v3.11.4) detected.
[!] Cached catalog release: 7 days old. Run 'litepsm refresh' to update.
[✓] Local disk space: 42.5 GiB available in DATA_ROOT.
[✓] Host configuration backups: 3 backups stored safely.

Status: HEALTHY (1 informational recommendation)
```

### The `--repair` Flag
If inconsistencies exist (e.g., orphaned staging files or dangling CAS entries), `litepsm doctor --repair` generates an explicit repair plan, displays it for user confirmation, and cleans up the anomalies.
