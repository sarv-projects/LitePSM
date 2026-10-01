# ARCH-29 — Connector System: Local Proxy Execution & Credential Custody

Status: **design record, not implemented.**
Scope: the `connector` type deferred to v2 in [26 — Ecosystem IA & Package Model](26-ECOSYSTEM-IA-PACKAGE-MODEL.md) §3.4.1.
Supersedes: the rationale "no portable v1 contract" for deferring `connector`. This document supplies that contract's shape; implementation remains v2.

---

## 1. Why this document exists

`connector` is the only v1-deferred type that is not a file format. `rule`, `hook`, `tool` and `lsp` are all *files someone drops in a directory*; a connector is **authenticated runtime state with a lifecycle**. It has an authorization flow, a token that expires, a scope grant, and a refresh policy. Treating it as "another package type" would be a category error, which is why it was deferred.

Four reference architectures were studied before proposing anything. This document records what was taken, what was refused, and what does not exist.

---

## 2. The question that decides the design

> If all credentials are stored locally at runtime, do we not have to worry about credential problems?

**No. Locality removes custody risk and adds an exfiltration risk.**

The concrete attack:

1. A Slack token sits in the vault.
2. A connector exposes `slack → chat.postMessage` to the agent.
3. If that token is reachable from anything the agent can read — an environment variable in the tool's process, a tool argument, a tool result, a transcript, a log line — then **any prompt injection reaches the Slack token**.

Moving that token from a vendor database to `~/.litepsm` does not close this. It relocates the blast radius from someone else's infrastructure to the user's filesystem and accounts, which is a strictly worse outcome for the user even if it is a better outcome for the operator.

Both Nango and Composio resolve this the same way: **the agent never holds the credential.** Nango proxies through `POST https://api.nango.dev/proxy/<endpoint>` with a `Connection-Id` handle. Composio proxies through `POST /tools/execute/{tool_slug}` with a `connected_account_id`. In both cases the raw token exists only on the executing side.

The conclusion for a local-first tool is therefore not "store it locally, done". It is:

> **Local proxy execution.** The agent names a connection; a local daemon holds the credential and makes the upstream call. The agent only ever sees request and response data.

That is Composio's cloud architecture with "cloud" replaced by loopback. The *shape* transfers; the *custody* inverts.

---

## 3. Reference architectures: what was taken and what was refused

### 3.1 Licensing posture comes first

This is not a footnote. Three of the four references cannot be embedded in a redistributable desktop product:

| Project | License | Can we ship it? | Evidence |
|---|---|---|---|
| Nango | **Elastic License 2.0** | **No** | "You may not provide the software to third parties as a hosted or managed service". `packages/providers/providers.yaml` — the ~1,000-slug catalog — lives inside the ELv2 repo and is **not** separately licensed. Free self-host is `docker-compose` but gates syncs, actions, MCP, webhooks, RBAC and audit trail behind Enterprise. |
| n8n | **Sustainable Use License v1.0** | **No** | Grant is "own internal business purposes or non-commercial/personal use". `.ee.` files are excluded from the SUL entirely. Embedding n8n in a shipped product is not covered by the grant. |
| Zapier | **`"license": "UNLICENSED"`** | **No** | `zapier-platform-core`, `-schema` and `-cli` all carry UNLICENSED with no LICENSE file. Proprietary, not redistributable. |
| Composio | **MIT** (SDK/repos) | SDK yes | The MIT repo contains **no toolkits, no tool schemas, no backend**. The 1,500 toolkit implementations, managed OAuth apps and execution engine are hosted-only. |

**Consequence:** we take *design ideas*, never code or catalog data. Any future connector catalog we ship must be authored in this repository under this project's license. The Nango provider YAML schema is the closest thing to a reusable artifact and it is not separable — so it is read as a specification, not copied.

An additional finding worth recording: Nango's refresh daemon runs **at least once every 24 hours**, specifically to dodge provider inactivity revocation. That is correct for a multi-tenant server guaranteeing uptime and **wrong for a laptop** — it burns battery and network to protect a credential nobody used. See §5.4.

### 3.2 Adopted, with attribution

| Idea | Source | Why it transfers |
|---|---|---|
| Three-layer model: **provider slug → integration → connection** | Nango | Separates a static capability+auth descriptor from a user's instance of it from one end user's credentials. Each layer has a different lifetime. |
| Declarative provider schema with a **verification probe** | Nango | `providers.yaml` declares auth mode, credential fields and a live verification request. Connect-time validation is the cheapest failure detection available. |
| `connection_config` vs `metadata` split | Nango | User-supplied inputs (workspace id, region) are a different trust class from data we store about the connection. |
| **Health as derived state, not an enum** | Nango | `last_refresh_success/failure`, `refresh_attempts`, `refresh_exhausted` plus a typed `errors[]`. Gives the green/amber/grey indicator in the project README a real source of truth instead of a guess. |
| Errors as `{type, log_id}` pointers, never inline messages | Nango | Keeps sensitive detail out of API responses while remaining debuggable. |
| **SSRF policy: denylist/allowlist modes, `blockPrivateIps`, `blockLinkLocal` (incl. `169.254.169.254`), `maxRedirects`, DNS-pinning that re-validates resolved IPs on every hop** | Nango | This is the single most transferable security asset found, and it matters *more* locally than remotely: a desktop agent proxies from the user's own network and LAN. |
| Refresh **recovery** webhook semantics, not just failure | Nango | Recoverable expiry should be reported as recoverable. |
| Reconnect-**in-place**, preserving metadata and synced data | Nango | Re-auth overwrites secrets. Delete-and-recreate is data loss. |
| **`restrictToSupportedNodes` enforced at decrypt time** | n8n | Authorization belongs in the vault's decrypt call, not the UI. A component that can decrypt can read; one that cannot cannot leak. n8n added this to close a hole where its HTTP Request node could decrypt every credential type. |
| **`sensitiveOutputFields` — always redacted, never revealable** | n8n | Deny-by-default redaction on the *output* side too. A secret that leaks via a tool result is still a leak. |
| **Two-layer envelope**: instance key wraps a per-record DEK; `keyId:ciphertext` prefix; per-record 32-byte salt; `hkdfSync('sha256', key, salt, 'n8n-encryption-v1', 32+12)`; AES-256-GCM; framed `version(1)‖salt(32)‖tag(16)‖ciphertext` | n8n | Reference implementation of a correct envelope. The `FORMAT_VERSION` byte plus a documented HKDF info string ("changing this invalidates every ciphertext") is a discipline worth copying. Rotation becomes lazy re-sealing instead of a re-encrypt-all migration. |
| Credential **references, never values**, in anything a component can read | n8n | Workflows store `credRef`. Secrets live in exactly one place. n8n explicitly warns against literal secrets in node parameters. |
| `VersionedNodeType` + pinned `typeVersion`; **malformed ≠ legacy** | n8n | A manifest declaring an unparseable API version is corrupt, not ancient. The runtime must not guess. |
| Per-credential **"allowed HTTP request domains"** | n8n | A GitHub token must never reach `evil.com`, even if the component is compromised. |
| **`authentication.test` required** in every integration definition | Zapier | Force a live verification call at connect time. |
| Fixed, small **auth-type enum** with strict per-type config (`additionalProperties: false`) | Zapier | Bounded surface. Every new auth type is a reviewed host change, not arbitrary component code. |
| `autoRefresh` + 4xx policy: on refresh failure mark **stale**, stop retrying, hold dependent work, surface reconnect | Zapier | Retry-forever on a 4xx is a footgun. |
| `:censored:` log redaction keyed off a per-field secret flag | Zapier | Correlatable without being readable. |
| semver policy: MAJOR for required-field or auth-scheme changes | Zapier | From the opposite direction to n8n; same discipline. |
| **auth-config / connected-account split**, `PRIVATE` by default, explicit opt-in sharing | Composio | Right default posture. The ACL is honestly labelled experimental there; we ship it PRIVATE with no sharing in v1. |
| **Capability-scoped keys** — raw proxy is a *separate permission* from named tools | Composio | Least privilege, and proxy access is reviewable on its own. |
| **Proxy-execute guardrails**: same scheme + same eTLD+1; reject cross-domain; refuse caller-supplied `Authorization` | Composio | Kills the two obvious exfiltration paths: attacker-chosen base URL and header override. Their docs call this "an intentional security boundary, not a quota". |
| **Callback identity verification** against OAuth session fixation | Composio | A concrete attack class that is rarely handled. |
| Typed per-error status mapping (403 = account lacks permission, 410 = deprecated) | Composio | Actionable, model-facing errors instead of string soup. |
| MCP Registry `server.json` credential vocabulary (`environment_variables[].is_secret`, `remotes[].headers[].isSecret`) | MCP Registry | The only **published JSON Schema** for "an MCP server and the credentials it needs". Reusing its spellings means an MCP host can consume our file untranslated. |
| RFC 9728 / RFC 7591 / RFC 8414 / RFC 8707 field names | IETF | Using standard names is how we avoid inventing auth vocabulary. |

### 3.3 Explicitly refused

| Refused | Source | Why |
|---|---|---|
| Hosted proxy as the mandatory execution path | Nango, Composio | A local-first tool inverts custody: our vault *is* the store. |
| ELv2 / SUL / UNLICENSED code | all three | Not redistributable in a desktop product. See §3.1. |
| 24-hour background refresh daemon | Nango | Server-uptime protection. Wrong on battery. See §5.4. |
| Kubernetes + Postgres + Redis + Elasticsearch + S3 topology | Nango, n8n | A desktop app is one process. |
| `N8N_ENCRYPTION_KEY` shared across a worker fleet via env | n8n | One process. Master key belongs in the OS keychain; a leaked env var must not be sufficient. |
| **Untyped `data` blob as the only credential representation** | n8n | Works only because a UI reads the field schema back. Without typing we lose field-level policy, audit and selective decryption. Prefer per-field AEAD. |
| Encrypting the entire credential as one blob | n8n | No per-field crypto boundary, so decrypt-any means decrypt-all. |
| `type: 'custom'` auth where the component picks its own injection point | Zapier | Third-party code choosing where a secret lands is the vulnerability class, not a feature. |
| Untyped `bundle.authData` | Zapier | Safe only because nothing inspects it. A vault must be typed and policy-checkable. |
| Optional cross-origin credential forwarding | n8n | Make it structurally impossible instead. |
| Cloud webhook ingress for triggers | Nango, Composio | No public ingress exists for a local daemon; inbound events need a durable poller instead. |
| Managed OAuth apps + hosted consent pages | Nango, Composio | Requires their client_id/secret. We need PKCE + loopback or device code. |
| Publishing untyped `bundle`/DAG engines | n8n | The agent is the loop. A second orchestration engine is explicitly forbidden by the project architecture. |

---

## 4. Proposed architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ AGENT PROCESS  (claude, codex, opencode, … via the LitePSM bridge)       │
│                                                                          │
│   tool call = (connected_account_id, operation, arguments)               │
│   ✔ sees the account handle                                            │
│   ✘ never opens the vault                                               │
│   ✘ never receives a raw token                                          │
└──────────────────────────────┬───────────────────────────────────────────┘
                               │  loopback / unix socket, short-lived
                               │  per-call capability (single-tool scope)
                               ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ LITEPSM DAEMON  — the only credential decryption authority               │
│                                                                          │
│  1. authorize      plugin ∈ grant list? scope covers this operation?      │
│  2. pin            URL derived from the toolkit's base URL, never args   │
│  3. egress policy  deny private/link-local, cap redirects, re-resolve    │
│  4. resolve        vault.release(credRef, field) → single plaintext value│
│                    decrypt scoped to one field, one call, then zeroize  │
│  5. inject         Authorization header, set only here                  │
│  6. call           HTTPS to the provider                                 │
│  7. log            {connector, account, field, host, outcome} — never    │
│                    values                                               │
│  8. respond        provider payload to the agent; refresh if 401          │
└──────────────────────────────┬───────────────────────────────────────────┘
                               │ HTTPS, credential attached
                               ▼
                        third-party API
```

### 4.1 The boundary is enforced by process isolation, not convention

The lesson from Composio's own local sandbox is the warning: they inject `COMPOSIO_API_KEY` into the sandbox `env`, so "any code or output in the sandbox can read it". A local daemon that decrypts in-process while also running agent-supplied tool code has the same hole.

Therefore:

* Vault decryption lives in a component the agent process **cannot** reach.
* A tool call is a **scope, not a login**: the agent gets a short-lived, per-call, single-tool capability, not a long-lived bearer.
* Decryption is **single-use**. A crashed or hung call cannot be replayed.

### 4.2 Data model

Two tables, mirroring the auth-config / connected-account split:

```
auth_config          (the blueprint — per toolkit)
  id, toolkit_slug, scheme ∈ {oauth2, api_key, bearer, basic},
  scopes {read[], write[]}, oauth_client_ref, enabled

connected_account    (one user's binding to one auth_config)
  id, auth_config_id, toolkit_slug, visibility = PRIVATE,
  status ∈ {ACTIVE, EXPIRED, DISABLED, FAILED, REVOKED},   -- see §5.4
  expires_at, non_secret_metadata,
  credential_blob  -- AEAD, per-field, keyed (account_id ‖ field_id)
```

"Read the credential" is a **distinct capability** from "read connection metadata". Multiple accounts per toolkit (work/personal) require explicit selection — never implicit fallback.

---

## 5. Open design questions, recorded not resolved

### 5.1 Minimum viable connector definition

Adopt MCP Registry `server.json` credential vocabulary and RFC 9728/7591/8414/8707 field names rather than inventing a parallel scheme. Proposed v1 fields:

```jsonc
{
  "schemaVersion": 1,                     // ours; no standard covers this
  "id": "reverse-dns/name",               // S2 "name"
  "title": "…", "description": "…",
  "version": "1.0.0",                     // exact SemVer — S2 rejects ranges
  "repository": { "url": "…", "source": "github" },
  "reach": { "mcp": { … } },              // exactly one of mcp | openapi | integration
  "auth": {
    "schemes": [{
      "type": "oauth2",                   // OpenAPI SecurityScheme names: oauth2|apiKey|http|mutualTLS
      "is_secret": true,                  // S2 KeyValueInput vocabulary
      "scopeModel": "groups",             // groups | coarse | capabilities
      "scopes": { "read": [ … ], "write": [ … ] },
      "clientMetadata": { /* RFC 7591 fields */ },
      "resourceMetadataUrl": "https://…/.well-known/oauth-protected-resource"
    }]
  },
  "inputs": [                             // S2 Input/KeyValueInput, renamed
    { "name": "WORKSPACE_ID", "is_required": true, "is_secret": false,
      "description": "…", "choices": [ … ] }
  ],
  "annotations": { "readOnlyHint": …, "destructiveHint": …, "openWorldHint": … }
}
```

`schemaVersion` and `reach` have no upstream equivalent and are ours; everything else maps onto a published schema or RFC.

### 5.2 Read and write consent are separate

Request the read scope group first; treat write as a **distinct consent step**. Never bundle them into one authorization. Store the granted scope set beside the token and re-prompt only when an operation needs an ungranted scope. This follows OpenWOP RFC 0095 §B.4 and both connector platforms' own practice.

### 5.3 The vault is the only OAuth client

On connect: fetch `/.well-known/oauth-protected-resource` and **validate that `resource` equals the identifier we requested** (RFC 9728 §3.3 — the anti-impersonation check), then `/.well-known/oauth-authorization-server`, then RFC 7591 dynamic client registration, falling back to a user-supplied client id. PKCE S256 mandatory. Our `client_id`/`client_secret` live in the vault, never in a connector file. Tokens never appear in query strings (MCP authorization spec; Anthropic's `static_headers` guidance says the same).

### 5.4 Health is derived, and refresh is lazy

Model `last_refresh_success`, `last_refresh_failure`, `refresh_attempts`, `refresh_exhausted` plus a typed `errors[]` — not an enum someone has to remember to update.

Refresh **on credential access**, adopting Nango's own advice to "fetch the token just before you use it", plus one refresh-and-replay on a 401. No background daemon. `refresh_exhausted` is what makes the UI demand re-auth instead of retrying forever.

### 5.5 Triggers are a different problem from actions

Actions are clean local proxy calls. Inbound events need either provider-side polling with a durable cursor or a tunnelled ingress, and **that boundary changes where the credential must be readable** — a background poller, not the agent loop. Design this before building the action path. Recommending: **actions first, triggers deferred**, because a poller holding credentials is a materially larger attack surface.

---

## 6. What does not exist

Stated explicitly so we do not claim conformance to a standard that isn't one:

1. **There is no IETF, W3C or OASIS "connector manifest" standard.** Searches return vendor blogs and unrelated drafts. The nearest artefacts are OpenWOP RFC 0095 (single-project, Apache-2.0, no independent implementers) and Pipedream/Apideck schemas (Pipedream ships code in its repo; Apideck publishes an OpenAPI spec for Apideck's *own* API, not for third-party connectors).
2. **No standard secret-*reference* / vault-handle type.** Nothing published says "put `vault://<opaque-id>` here".
3. **No standard joining credential *declaration* to credential *acquisition*** in one portable file. OpenAPI declares schemes, RFC 7591 registers a client, MCP transports them; nobody joins them.
4. **No standard for the static-API-key case.** Anthropic's `static_headers` and MCP's `remotes[].headers[].isSecret` are the two published spellings and they disagree on casing.
5. **MCP itself has no in-protocol auth manifest.** `server.json` is a registry artifact, versioned by date, and the registry is in preview. It is not "the MCP standard for auth".
6. **No credential-rotation or re-auth UX contract.**
7. **No portable iPaaS definition format.** Workato, Make, Tray, Paragon and Merge.dev ship proprietary catalogs; this was **not** verified from primary sources for any of them.

Additionally: OpenWOP is a single project whose own site admits its evidence is first- and second-party. Use it as design inspiration. Do not describe our manifest as "OpenWOP-conformant".

---

## 7. Evidence

Sources actually fetched during this research:

* **Nango** — `api.github.com/repos/NangoHQ/nango` (note: the commonly cited `getnango/nango` does not exist), `LICENSE`, `docker-compose.yaml`, `packages/utils/lib/encryption.ts`, `packages/types/lib/connection/db.ts`, `packages/providers/providers.yaml`, `nango.dev/docs/llms.txt`, `/docs/guides/platform/security`, `/docs/guides/platform/self-hosting{,/self-managed}`, `/docs/guides/proxy-requests`, `/docs/guides/auth/{auth-guide,token-refreshing}`, `/docs/reference/backend/http-api/connections/{get,list}`.
* **n8n** — `LICENSE.md`, `packages/workflow/src/interfaces.ts`, `versioned-node-type.ts`, `nodes-api-version.ts`, `dynamic-credentials-helpers.ts`, `packages/core/src/encryption/{cipher,aes-256-gcm,interface}.ts`, `packages/@n8n/db/src/entities/{credentials-entity,shared-credentials}.ts`, `packages/cli/src/credentials/credentials.service.ts`, `packages/nodes-base/credentials/{NotionApi,AirtableOAuth2Api}.credentials.ts`, `packages/nodes-base/package.json`; docs `integrations/builtin/node-types.md`, `deploy/host-n8n/community-edition-features.md`, `.../set-a-custom-encryption-key.md`, `.../security/rotate-encryption-keys.md`, `.../use-environment-variables/credentials.md`.
* **Zapier** — `zapier-platform-schema` `lib/schemas/{AuthenticationSchema,AuthenticationOAuth2ConfigSchema,AuthenticationSessionConfigSchema,AuthenticationCustomConfigSchema,TriggerSchema,BasicOperationSchema}.js`, `zapier-platform-core` `src/{index,execute,create-app}.js` and `src/http-middlewares/after/throw-for-stale-auth.js`, all three `package.json`; docs `docs.zapier.com/llms.txt`, `integrations/build/auth.md`, `integrations/build/oauth.md`, `integrations/manage/versions.md`, `integrations/quickstart/private-vs-public-integrations.md`, `connectors/overview.md`.
* **Composio** — `api.github.com/repos/ComposioHQ/composio`, `raw.githubusercontent.com/ComposioHQ/composio/next/LICENSE`; docs `/docs/{quickstart,how-composio-works,authentication,toolkits,triggers}`, `/docs/security/token-custody`, `/docs/tools-direct/toolkit-versioning`, `/docs/sandbox/local`, `/docs/extending-sessions/custom-tools-and-toolkits`, `/reference/api-reference/{auth-configs,connected-accounts,tools}`, `/kb/guide/platform-self-hosted-helm`, `/llms.txt`.
* **Standards** — `modelcontextprotocol.io/specification/2025-06-18/basic/authorization`, `static.modelcontextprotocol.io/schemas/2025-07-09/server.schema.json`, `modelcontextprotocol.io/registry/remote-servers`, `datatracker.ietf.org/doc/html/rfc9728`, `/rfc7591`, `spec.openapis.org/oas/v3.1.1`, `developers.openai.com/{plugins/build/auth,apps-sdk/build/mcp-server}`, `claude.com/docs/connectors/building/{authentication,submission}`, `pipedream.com/docs/components/contributing/guidelines`, `developers.apideck.com/guides/vault`, `npmjs.com/package/mcp-remote`, `openwop.dev/rfcs/0095-…html`.

**Not verified:** Zapier's at-rest encryption of auth bundles (no public spec found); n8n's external-secrets architecture doc (sitemap URLs 404 — code cited instead); whether n8n's SUL "internal business purposes" covers embedding in a distributed product (a legal determination the license text does not make — do not self-certify); Workato/Make/Tray/Paragon/Merge.dev portability claims (vendor comparison marketing only); whether MCP has newer authorization revisions than 2025-06-18 (2025-11-25 and 2026-07-28 pages referenced but not fetched).

---

## 8. Status and next steps

**Implemented:** nothing. No connector code exists.

**Before implementation, in order:**

1. Record the `connector` v1 contract as a schema with a round-trip conformance test, exactly as [26](26-ECOSYSTEM-IA-PACKAGE-MODEL.md) §7 requires of every other type.
2. Prove the credential boundary with a test that asserts the agent process cannot read the vault file — a structural test, not a policy statement.
3. Build the egress policy (Nango's SSRF controls) before the first connector exists, so the first real integration cannot skip it.
4. Implement actions only. Triggers last (§5.5).

**Promotion to v1 requires:** a portable format, an ingestion adapter, and real data — the same bar §3.4.1 set and that §7 of [26](26-ECOSYSTEM-IA-PACKAGE-MODEL.md) repeats. This document supplies the format's shape. It does not supply the data.
