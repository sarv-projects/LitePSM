# API and Package Contracts

## 1. Static Catalog API (v1)

Public discovery metadata is distributed via immutable HTTPS static JSON endpoints hosted on Cloudflare Pages.

```text
/v1/current.json                                      # Lightweight pointer to active release
/v1/releases/{release-id}/metadata.json               # Release provenance, sequence, builder version
/v1/releases/{release-id}/manifest.json               # SHA-256 digests & byte counts of all files
/v1/releases/{release-id}/index.json                  # Compact global search index
/v1/releases/{release-id}/shards/{kind}/{cat}.json    # Category partition shards
/v1/releases/{release-id}/items/{encoded-id}.json     # Full item detail & versions
```

### 1.1 Active Release Pointer (`/v1/current.json`)
```json
{
  "$schema": "https://litepsm.dev/schemas/v1/catalog-pointer.schema.json",
  "releaseId": "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2",
  "sequence": 142,
  "manifestDigest": "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
  "createdAt": "2026-09-30T12:00:00Z",
  "minClientVersion": "1.0.0"
}
```
*   **Cache Policy:** `Cache-Control: public, no-cache, must-revalidate`. Clients check this file to detect catalog updates.

### 1.2 Immutable Release Files (`/v1/releases/{release-id}/...`)
*   **Cache Policy:** `Cache-Control: public, max-age=31536000, immutable`.
*   **Integrity Guarantee:** Before reading shards or item records, clients verify file sizes and SHA-256 digests against `manifest.json`.

---

## 2. InstallPlan v2 Specification

The `InstallPlan` represents an immutable, verifiable contract describing all proposed modifications before execution.

```json
{
  "$schema": "https://litepsm.dev/schemas/v1/install-plan.schema.json",
  "schemaVersion": 2,
  "planId": "plan_01J9X9P3B1N4K8L7M6Q5R2T4W9",
  "planHash": "sha256:4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b",
  "createdAt": "2026-09-30T12:05:00Z",
  "expiresAt": "2026-09-30T12:20:00Z",
  "catalogReleaseId": "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2",
  "sourceSnapshots": ["snap_01J9X8K2M4N5P6Q7R8S9T0U1V2"],
  "request": {
    "listingId": "mcp:builtin:mcp-registry/postgresql",
    "requestedVersion": "1.4.0",
    "selectedComponents": ["mcp-provider/server"],
    "targetScope": "user",
    "targetHost": "claude-code"
  },
  "resolved": {
    "version": "1.4.0",
    "immutableRefs": ["git:7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c"],
    "artifacts": [
      {
        "artifactId": "art_pg_server",
        "type": "npm",
        "locator": "https://registry.npmjs.org/@modelcontextprotocol/server-postgres/-/server-postgres-1.4.0.tgz",
        "digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        "size": 184520
      }
    ],
    "dependencies": [],
    "runtimeRequirements": [
      { "type": "executable", "name": "node", "minVersion": "18.0.0" }
    ]
  },
  "effects": [
    { "type": "package.install", "target": "trees/sha256/e3/e3b0c4..." },
    { "type": "provider.start", "target": "node dist/index.js" }
  ],
  "requestedAccess": [
    { "resource": "network.outbound", "description": "Connect to PostgreSQL server on user-configured host/port" }
  ],
  "hostChanges": [],
  "providerLaunches": [
    {
      "providerName": "postgresql",
      "executable": "node",
      "args": ["trees/sha256/e3/e3b0c4.../dist/index.js"],
      "environmentVariables": ["POSTGRES_URL"]
    }
  ],
  "collisions": [],
  "warnings": [],
  "preconditions": [
    { "type": "path_not_exists", "target": "trees/sha256/e3/e3b0c4..." },
    { "type": "catalog_release_matches", "expected": "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2" }
  ],
  "approval": {
    "required": true,
    "reasonCodes": ["REQUIRES_EXTERNAL_NETWORK", "EXECUTES_LOCAL_BINARY"],
    "minimumChannel": "cli-tty"
  }
}
```

### Cryptographic Plan Hashing Rule
`planHash` is computed over the RFC 8785 canonical JSON representation of all execution fields (excluding volatile fields `planId`, `createdAt`, `expiresAt`). Approval binds strictly to `planHash`.

---

## 3. Local Daemon IPC Contract (JSON-RPC 2.0)

Local Bridge Shims, CLI sessions, and the diagnostic doctor interact with the Daemon over a versioned JSON-RPC 2.0 stream (Windows Named Pipe or Unix Domain Socket).

### 3.1 Initial Handshake (`daemon.handshake`)
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "daemon.handshake",
  "params": {
    "protocolVersion": "1.0",
    "clientType": "bridge",
    "clientVersion": "1.0.0",
    "hostId": "claude-code",
    "sessionId": "sess_01J9X9W4A5B6C7D8E9F0"
  }
}
```

### 3.2 Supported IPC Methods
| Method | Description |
|---|---|
| `tools.list` | Lists installed capabilities plus read-only detected external host tools |
| `catalog.search` | Queries cached index with filters and limits |
| `catalog.get_item` | Retrieves full listing metadata and version history |
| `resolver.prepare_plan` | Pure dependency resolution generating `InstallPlan` v2 |
| `install.execute` | Submits approval and begins transactional execution |
| `install.remove` | Safe removal and unreferenced CAS pruning |
| `skills.list` | Returns progressive-disclosure skill index for installed trees |
| `skills.load_body` | Retrieves progressive `SKILL.md` body on demand |
| `skills.read_resource` | Reads bounded skill supporting resource |
| `capabilities.search` | Searches capability/tool names across providers |
| `capabilities.describe` | Inspects capability schema, effects, and status |
| `provider.probe` | Checks provider health and probes tool schemas |
| `provider.invoke` | Policy-evaluated tool execution |
| `invocation.get` | Retrieves invocation status |
| `invocation.cancel` | Cancels an active invocation |
| `host.detect_config` | Probes default agent configuration file path |
| `host.apply_setup` | Injects Bridge shim entry with atomic pre-edit backup |
| `doctor.run_checks` | Runs 10 non-mutating system diagnostics |
| `system.status` | Returns daemon version, protocol, PID, and readiness |

> `install.update` is planned but has no handler in `cmd/litepsm/main.go:registerCoreHandlers` — update flows must go through a fresh `resolver.prepare_plan` + `install.execute` until it is implemented.

---

## 4. Bridge MCP Tool Surface (In-Agent Access)

When an agent host (e.g., Claude Code, Codex, OpenCode) boots the LitePSM Bridge via stdio, the shim advertises 12 bounded tools:

```text
┌──────────────────────┬────────────────────────────────────────────────────────┐
│ Bridge MCP Tool      │ Operational Signature & Role                           │
├──────────────────────┼────────────────────────────────────────────────────────┤
│ search_catalog       │ (query: str, kinds?: str[], limit?: int) -> Listing[]  │
│ get_extension        │ (id: str, version?: str) -> ListingDetail              │
│ prepare_install      │ (id: str, version?: str) -> InstallPlanSummary         │
│ request_install      │ (planId: str, approvalToken?: str) -> InstallResult    │
│ list_installed       │ (kind?: str, enabled?: bool) -> InstalledComponent[]   │
│ search_capabilities  │ (query: str, limit?: int) -> CapabilitySummary[]       │
│ describe_capability  │ (capabilityId: str) -> CapabilityDetail                │
│ load_skill           │ (skillId: str, version?: str) -> SkillBodyText         │
│ read_skill_resource  │ (skillId: str, path: str) -> ResourceContent           │
│ invoke_capability    │ (capabilityId: str, arguments: object) -> ToolResult   │
│ get_invocation       │ (invocationId: str) -> InvocationStatus                │
│ cancel_invocation    │ (invocationId: str) -> bool                            │
└──────────────────────┴────────────────────────────────────────────────────────┘
```

*   **Progressive Disclosure:** Tools return minimal structured tokens. Skill bodies and capability schemas are loaded only when requested.
*   **Fail-Closed Invocations:** If `invoke_capability` targets an unapproved tool or if the tool's `schemaFingerprint` has drifted, the call fails closed and requests approval.

---

## 5. Standardized Machine Error Envelope

All errors returned through the CLI, IPC, or Bridge MCP tools conform to a machine-readable JSON envelope:

```json
{
  "code": "LPSM-PROVIDER-SCHEMA-DRIFT",
  "message": "Downstream provider tool schema changed since approval was granted.",
  "category": "LPSM-PROVIDER",
  "retryable": false,
  "correlationId": "corr_01J9XA12B3C4D5E6F7G8",
  "causeCode": "SCHEMA_FINGERPRINT_MISMATCH",
  "details": {
    "providerId": "prov_postgres_1",
    "capabilityId": "inst-1/db/query",
    "previousFingerprint": "sha256:112233...",
    "currentFingerprint": "sha256:445566..."
  }
}
```
