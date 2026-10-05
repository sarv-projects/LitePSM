# API and Package Contracts

> **Precedence and status.** [ARCH/00](00-INDEX.md#1-document-status--precedence) ranks this
> document with the data/state/schema contracts. Evidence states are defined by
> [ARCH/31 §2](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide);
> where a contract here disagrees with [STATUS.md](../STATUS.md), STATUS wins. Sections below state
> the **current** code shape first and the **target** shape second. Where the two differ, they are
> labelled as a known mismatch — this document never claims conformance it cannot demonstrate.

## 1. Static Catalog API (v1)

Public discovery metadata is distributed as immutable HTTPS static JSON served from a Cloudflare
Worker static-assets deployment (`wrangler.toml`); the origin the client defaults to is
`https://litespm.sarveshbh-2022.workers.dev` (`internal/config/config.go:24`).

### 1.1 Endpoint inventory — what exists, what reads it, what the origin serves

| Path | Emitted by | Read by | State |
|---|---|---|---|
| `/v1/current.json` | `scripts/build_full_catalog.py` (deployed producer) and `internal/catalogbuild.CompileRelease` (`compiler.go:172`) | `catalog.Client.FetchCurrent` (`internal/catalog/client.go:93`) | Served: **200** |
| `/v1/releases/{id}/manifest.json` | `catalogbuild` (`compiler.go:150`) | `catalog.Client.FetchManifest` (`client.go:124`) | **Not published**: live 404 |
| `/v1/releases/{id}/listings.json` | `catalogbuild` (`compiler.go:101`) | `catalog.Client.FetchListings` (`client.go:160`), digest-verified against the manifest (`client.go:153-189`) | **Not published**: live 404 |
| `/v1/releases/{id}/versions.json` | `catalogbuild` (`compiler.go:117`) | no client fetch | Emitted by the compiler only; unpublished |
| `/v1/releases/{id}/metadata.json` | — | — | **`DESIGNED`** (ARCH/18 §2); compiler does not emit it |
| `/v1/releases/{id}/index.json` | — | — | **`DESIGNED`** (ARCH/18 §1–§2); not emitted |
| `/v1/releases/{id}/shards/{kind}/{cat}.json` | — | — | **`DESIGNED`** (ARCH/18 §1–§2); not emitted |
| `/v1/releases/{id}/items/{encoded-id}.json` | — | — | **`DESIGNED`** (ARCH/18 §1–§2); not emitted |

`litespm catalog sync` reads exactly the first three paths in order (`internal/catalog/client.go:92-193`).
Against the live origin it succeeds today (probe 2026-10-05):

```text
200  /v1/current.json
200  /v1/releases/rel-2026-10-05-01/manifest.json
200  /v1/releases/rel-2026-10-05-01/listings.json
```

So the sync path is `SHIPPED` ([STATUS.md](../STATUS.md) §2,
[ARCH/31 §4.2](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#42-the-published-catalog-and-the-client-read-different-files--the-sync-path-is-dead-at-the-origin)
— the defect it records was closed on 2026-10-05).
What the current code reads and writes is the `/v1/releases/<id>/{manifest,listings,versions}.json`
layout — `internal/catalog/client.go` and `internal/catalogbuild/compiler.go` agree on it, and
`TestReleasePathContractPinsDocumentedLayout` (`internal/catalog/catalog_test.go:217`) pins both the
builder's emitted keys and the client's requested paths to that layout while rejecting the
un-namespaced `releases/<id>/…` form. What is missing is a **published** tree, not a client path.

### 1.2 Active Release Pointer (`/v1/current.json`)

Shape as typed by `catalogbuild.CurrentPointer` (`internal/catalogbuild/compiler.go:35-43`):

```json
{
  "schemaVersion": 1,
  "releaseId": "rel-2026-09-30-01",
  "sequence": 142,
  "manifestDigest": "sha256:736a31e6eac4a2910a187e1c834dc4d342d3201d4adc1b9961ad3c4a96af6715",
  "itemCount": 5814,
  "createdAt": "2026-10-01T17:22:45Z"
}
```

*   **Cache Policy:** `Cache-Control: public, no-cache, must-revalidate`, written into a deploy-time
    `_headers` file by `scripts/deploy-pages.sh:31-42` — implemented, not aspirational.
*   **No `$schema` field exists.** Earlier revisions of this document showed a `$schema` URI of
    `https://litespm.dev/schemas/v1/catalog-pointer.schema.json`; that file is **not** in `schemas/`,
    and neither `CurrentPointer` nor the live pointer carries the field.
*   **Known mismatches with the live file** (all verified, [ARCH/31 §4.2](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#42-the-published-catalog-and-the-client-read-different-files--the-sync-path-is-dead-at-the-origin)):
    1. the live `manifestDigest` is the SHA-256 of `web/data/catalog.json`, while
       `CurrentPointer.ManifestDigest` is documented as the digest of `manifest.json` — same field,
       two different files;
    2. the live pointer carries no `schemaVersion`, so the Go field unmarshals to `0`;
    3. the live pointer carries `advisories`, `totalCapabilities`, `mcpServersCount`,
       `agentSkillsCount`, `pluginsCount`, `hostCompatibility` — none of which exist in the Go
       struct, and the Go client cannot read them.

### 1.3 Immutable Release Files (`/v1/releases/{release-id}/...`)

*   **Cache Policy:** `Cache-Control: public, max-age=31536000, immutable` for `/v1/releases/*`
    (`scripts/deploy-pages.sh:31-42`).
*   **Integrity Guarantee:** the client verifies `listings.json` against `manifest.json` before
    accepting it (`internal/catalog/client.go:153-189`).
*   **Availability:** built in-repo (`litespm catalog build`, end-to-end tested) and
    **published** — the live origin serves the whole tree (verified 2026-10-05, STATUS §2).

---

## 2. InstallPlan Contract

Three shapes exist and **they disagree**. This section states each honestly.

*   **(a) Current — Go `domain.InstallPlan`** (`internal/domain/models.go:436-452`). This is what
    `resolver.prepare_plan` builds, hashes, persists, and returns
    (`cmd/litespm/main.go:1848-1902`), and what `install.execute` re-verifies before it acts —
    hash and expiry at `internal/install/engine.go:105-120`, artifact source at `:204-207`.
*   **(b) Target — `schemas/install-plan.schema.json`.** Draft 2020-12, `$id`
    `https://litespm.dev/schemas/v1/install-plan.schema.json`, `additionalProperties: false`.
*   **(c) A stale example previously published in this document**, which matched (b) and not (a).

**The Go type does not adhere to the schema, and no test asserts that it does.** The comment at
`internal/domain/models.go:435` ("strictly adheres to") is inaccurate; treat (b) as the target to be
reached by a future migration, not as a description of today's bytes.

### 2.1 Current shape (Go serialization)

```json
{
  "schemaVersion": 2,
  "planId": "plan_7Q2K4M9B5X3T8V6R1D0F9H2J7N4",
  "planHash": "sha256:4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b",
  "createdAt": "2026-10-05T09:15:00Z",
  "expiresAt": "2026-10-05T09:30:00Z",
  "catalogReleaseId": "",
  "request": {
    "listingId": "mcp:builtin:postgresql",
    "requestedVersion": "1.4.0",
    "targetScope": "user"
  },
  "resolved": {
    "version": "1.4.0",
    "artifacts": []
  },
  "effects": ["package.install"],
  "preconditions": {},
  "approval": { "channel": "", "decision": "" }
}
```

Notes on that serialization:

*   `planId` is `plan_` + 26 Crockford base32 symbols, matching `^plan_[0-9A-Za-z]{26}$`
    (`cmd/litespm/main.go:1803-1817`).
*   `catalogReleaseId` is emitted empty today — `buildInstallPlan` never sets it.
*   `preconditions` and `approval` marshal to empty objects because every field inside them is
    `omitempty` (`internal/domain/models.go:393-407`); a plan built by `buildInstallPlan` populates
    none of them.
*   `collisions` and `warnings` have no counterpart in the Go struct at all.
*   Plan lifetime is 15 minutes from creation (`cmd/litespm/main.go:1865`).

### 2.2 Known mismatches: Go type vs `schemas/install-plan.schema.json`

| Field | Go (current) | Schema (target) |
|---|---|---|
| `effects` | `[]string` — `["package.install"]` (`models.go:446`) | array of `{type, target}` objects (`additionalProperties: false`) |
| `preconditions` | `PlanPreconditions{filesystemPaths[], portsAvailable[], runtimesFound[], diskSpaceBytes}` (`models.go:393-398,447`) | array of `{type, target, expected}` |
| `approval` | `PlanApproval{approvalId, channel, decision, decidedAt, approvedBy}` (`models.go:401-407,448`) | `{required: bool, reasonCodes[], minimumChannel}` — `required` is mandatory |
| `requestedAccess` | `*RequestedAccess{tools[], paths[], hosts[]}` (`models.go:429-433,451`) | array of `{resource, description}` |
| `providerLaunches` | `ProviderLaunch{providerId, transport, command, args, env: map[string]string, endpoint}` (`models.go:419-426,450`) | `{providerName, executable, args[], environmentVariables[]}` |
| `collisions`, `warnings` | absent | present and top-level (`additionalProperties: false`) |

`effects`, `preconditions`, and `approval` are **required** by the schema, so a plan produced by the
daemon today would fail validation on three required fields plus any populated optional one. Closing
this gap requires choosing one shape and migrating the other; until then, consumers must use the Go
shape (a).

### 2.3 Cryptographic Plan Hashing Rule (matches the code)

`planHash` is `sha256:` + hex of the RFC 8785 canonical JSON of the execution fields
(`internal/domain/canonical.go:19-57`): `schemaVersion`, `catalogReleaseId`, `request`, `resolved`,
`effects`, `preconditions`, plus `sourceSnapshots`, `hostChanges`, `providerLaunches`,
`requestedAccess` when non-nil. Volatile fields `planId`, `createdAt`, `expiresAt` are excluded, as
are `planHash` itself and `approval` — approval is the record that binds **to** `planHash`. The
install engine re-verifies hash and expiry before doing any work
(`internal/install/engine.go:105-120`).

---

## 3. Local Daemon IPC Contract (JSON-RPC 2.0)

Local Bridge shims, CLI sessions, and the diagnostic doctor interact with the daemon over a
versioned JSON-RPC 2.0 stream (Windows Named Pipe or Unix domain socket). Messages are
newline-delimited and bounded at 16 MiB (`internal/ipc/protocol.go:25-27,76-168`).

### 3.1 Initial Handshake (`daemon.handshake`)

Field names follow `internal/ipc/protocol.go` (`HandshakeParams`/`HandshakeResult`) and
[ARCH/11 §3.1](11-LOCAL-RUNTIME-IPC.md):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "daemon.handshake",
  "params": {
    "clientVersion": "1.0.0",
    "clientKind": "bridge",
    "hostId": "claude-code",
    "pid": 12345
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "daemonVersion": "0.3.0",
    "protocolVersion": "2026-07-28",
    "pid": 54321
  }
}
```

There is **no `sessionId`, `clientType`, or `protocolVersion` request field** — the request uses
`clientKind`, and protocol version is returned, not proposed. The handler is registered as the
baseline handshake in `internal/ipc/server.go:48` and accepts any well-formed handshake without
version gating (version gating, and the `LPSM-IPC-VERSION-INCOMPATIBLE` code, are `DESIGNED`; the
only `LPSM-IPC-*` constant today is `LPSM-IPC-DAEMON-UNREACHABLE`).
**Reachability:** `ipc.Client.Handshake` (`internal/ipc/client.go:94-103`) has a single caller —
`internal/ipc/ipc_test.go:78`. `runBridge` dials and serves without handshaking
(`cmd/litespm/main.go:344`), so the handshake is a registered contract, not a step that occurs in
production today ([ARCH/11 §3.1](11-LOCAL-RUNTIME-IPC.md) records the same
fact).

### 3.2 Supported IPC Methods (19 application handlers; `daemon.handshake` is §3.1)

Status column = highest honest state per [STATUS.md](../STATUS.md) §1/§4.

| Method | Description | Status |
|---|---|---|
| `daemon.handshake` | Baseline connection handshake | `TESTED` (`internal/ipc/server.go:48`); caller is test-only (§3.1) |
| `tools.list` | Lists installed capabilities plus read-only detected external host tools | Resolves (`cmd/litespm/main.go:1244`) |
| `catalog.search` | Queries the local catalog index with filters and limits | Resolves (`:1286`); empty until `catalog sync` succeeds |
| `catalog.get_item` | Retrieves full listing metadata and version history | Resolves (`:1340`) |
| `resolver.prepare_plan` | Pure dependency resolution producing an `InstallPlan`, persisted with `planHash` | Resolves (`:1359`) |
| `install.execute` | Submits approval and begins transactional execution | `IMPLEMENTED`, **cannot complete**: no artifact source (`main.go:1420-1426`, `internal/install/engine.go:204-207`) |
| `install.remove` | Safe removal and unreferenced CAS pruning | Resolves (`:1444`) |
| `skills.list` | Returns progressive-disclosure skill index for installed trees | Resolves (`:1467`) |
| `skills.load_body` | Retrieves progressive `SKILL.md` body on demand | Resolves (`:1522`) |
| `skills.read_resource` | Reads bounded skill supporting resource | Resolves (`:1573`) |
| `capabilities.search` | Searches capability/tool names across providers | **`-32601`** — "not wired yet; use `catalog.search`" (`:1628-1633`) |
| `capabilities.describe` | Inspects capability schema, effects, and status | **`-32601`** (`:1637-1642`) |
| `provider.probe` | Reports the supervisor's real view of one provider | Resolves (`:1648`); nothing tracks a provider in practice (no non-test provider rows) |
| `provider.invoke` | Policy-evaluated tool execution | **`-32601`** — no capability rows, no dispatch (`:1676-1683`) |
| `invocation.get` | Retrieves invocation status | **`-32601`** — no invocation registry (`:1685-1691`) |
| `invocation.cancel` | Cancels an active invocation | **`-32601`** (`:1694-1700`) |
| `host.detect_config` | Probes default agent configuration file path | Resolves (`:1702`) |
| `host.apply_setup` | Injects the Bridge MCP entry with atomic pre-edit backup | Resolves (`:1711`) |
| `doctor.run_checks` | Runs 10 non-mutating system diagnostics | Resolves (`:1740`) |
| `system.status` | Returns daemon version, protocol, PID, and readiness | Resolves (`:1747`) |

Notes:

*   `install.update` has **no handler**; update flows must go through a fresh
    `resolver.prepare_plan` + `install.execute` until one exists.
*   The five `-32601` handlers are deliberate fail-closed stubs with concrete reasons; they are not
    missing-method errors and must not be described as working features.
*   Future methods are specified elsewhere and are **not** registered here: `profile.*` and
    `lease.*` in [ARCH/35](35-PROFILES-AND-CAPABILITY-LEASES.md) (`DESIGNED`), the real
    `provider.invoke` / `invocation.get` / `invocation.cancel` in
    [ARCH/34](34-RUNTIME-INVOCATION-RECEIPTS.md) (`DESIGNED`), and `policy explain` /
    `audit --ci` in [ARCH/36](36-ENTERPRISE-POLICY-AND-AUDIT.md) (`DESIGNED`).

---

## 4. Bridge MCP Tool Surface (In-Agent Access)

When an agent host boots the LiteSPM Bridge via stdio, the shim advertises **12** bounded tools
(`internal/bridge/shim.go`; list at `tools/list`, dispatch at `DispatchTool`).

```text
┌──────────────────────┬──────────────────────────────────────────────────────────────┐
│ Bridge MCP Tool      │ Operational Signature, Role & Dispatch Status                │
├──────────────────────┼──────────────────────────────────────────────────────────────┤
│ search_catalog       │ (query, kinds?, limit?) -> results        → catalog.search   │
│ get_extension        │ (id, version?) -> listing detail          → catalog.get_item │
│ prepare_install      │ (id, version?) -> plan                    → resolver.prepare_plan │
│ request_install      │ (planId, approvalToken?) -> result        → install.execute  │
│ list_installed       │ (kind?, enabled?) -> installed panel      → tools.list       │
│ search_capabilities  │ (query, limit?) -> summaries               → capabilities.search │
│ describe_capability  │ (capabilityId) -> detail                   → capabilities.describe │
│ load_skill           │ (skillId, version?) -> SKILL.md body       → skills.load_body │
│ read_skill_resource  │ (skillId, path) -> resource content        → skills.read_resource │
│ invoke_capability    │ (capabilityId, arguments) -> tool result   → provider.invoke  │
│ get_invocation       │ (invocationId) -> status                   → invocation.get   │
│ cancel_invocation    │ (invocationId) -> bool                     → invocation.cancel │
└──────────────────────┴──────────────────────────────────────────────────────────────┘
```

Dispatch status (per [STATUS.md](../STATUS.md) §1/§4):

*   **Resolve:** `search_catalog` (`shim.go:282`), `get_extension` (`:305`), `prepare_install`
    (`:323`), `list_installed` (`:359`), `load_skill` (`:404`), `read_skill_resource` (`:433`).
*   **Reach but cannot complete:** `request_install` (`shim.go:341`) forwards to `install.execute`,
    which supplies no `ArchiveSource`/`TreeSource`; the engine rejects it
    (`internal/install/engine.go:204-207`). **An agent cannot install anything through
    `/marketplace` today.**
*   **Daemon answers `-32601`:** `search_capabilities` (`:370`), `describe_capability` (`:388`),
    `invoke_capability` (`:452`), `get_invocation` (`:475`), `cancel_invocation` (`:491`) — the
    shim forwards, the daemon rejects with the reasons in §3.2.
*   **Standalone (no daemon) mode:** every tool fails closed with an explicit error rather than
    fabricating results (`internal/bridge/shim.go:274-280`).

Two behavioural rules survive any status change:

*   **Progressive Disclosure:** tools return minimal structured tokens; skill bodies and capability
    schemas load only on request.
*   **Fail-Closed Invocations:** the gate that fails an unapproved or schema-drifted tool closed
    and requests approval lives in the policy engine — see
    [ARCH/05 §5](05-SECURITY.md#5-capability-schema-drift-defense) for that (currently unreachable,
    `IMPLEMENTED`) check. `invoke_capability` itself does not reach it yet: the daemon answers
    `-32601` for every invocation today (§3.2).

---

## 5. Standardized Machine Error Envelope

Errors surfaced by the CLI, and the payloads Bridge MCP tools place in their error results, use the
`domain.LPSMError` envelope (`internal/domain/errors.go:37-49`), which matches
`schemas/errors.schema.json`:

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

*   `code`, `message`, `category`, `retryable` are always present; `correlationId` and `causeCode`
    are optional (`omitempty`) and are rarely populated today — the example above is illustrative
    of the envelope, not a captured response.
*   Over IPC the transport error is `ipc.RPCError{code, message, data}`
    (`internal/ipc/protocol.go:45-50`); reserved codes include `-32601` (method not found),
    `-32001` unauthorized, `-32002` plan stale, `-32003` schema drift, `-32004` rate limited.
    Bridge tools re-serialize `LPSMError` into the MCP error result
    (`internal/bridge/shim.go:590-605`).
*   CLI process exit codes are `0` (success), `1` (failure), `2` (usage error, e.g.
    `cmd/litespm/main.go:818`), plus the per-category `doctor` codes `10/20/30/40/50/60/70`
    (`cmd/litespm/main.go:925-982`) — owned by [ARCH/20](20-ERRORS-AUDIT-DOCTOR.md).
