# API and package contracts

All interfaces below are proposed contracts. Keep public catalog reading separate from local install authority.

## Static catalog API (v1)

Static JSON is the initial public API; same-origin HTTPS URLs make it usable by a website, CLI, SDK, or other agent.

```text
GET /v1/catalog/index.json
GET /v1/catalog/shards/{kind}/{category}.json
GET /v1/items/{encoded-stable-id}.json
GET /v1/items/{encoded-stable-id}/versions/{version}.json
GET /v1/metadata.json
```

Requirements:

- Every item response includes `schemaVersion`, stable ID, kind, source, upstream reference, version/ref, digest where available, compatibility facts, provenance, and `updatedAt`.
- Mutable “latest” is a convenience display pointer only. Install plans resolve to an exact version/ref/digest.
- Responses include ETag/Last-Modified and cache policy; clients cache valid prior metadata and can continue showing installed packages offline.
- Catalog JSON never contains credentials, private source data, or undocumented authentication values.
- Shards are bounded; search first downloads compact indexes and then fetches detail as needed.
- Stable schema changes are additive; breaking changes use `/v2` and overlapping support.

## InstallPlan

The resolver and local client share a serializable plan. The plan is not itself authorization to execute.

```json
{
  "schemaVersion": 1,
  "listingId": "skills.example/review-pr",
  "kind": "skill",
  "version": "1.2.0",
  "source": { "type": "git", "url": "https://example.invalid/repo", "ref": "<immutable-ref>" },
  "digest": "sha256:<digest>",
  "target": { "manager": "litepsm", "scope": "user" },
  "files": ["skills/review-pr/SKILL.md"],
  "effects": [{ "type": "write-files", "paths": ["<resolved-local-skill-dir>"] }],
  "requestedAccess": [],
  "requiresUserApproval": true
}
```

The local client resolves safe LitePSM-store paths. A host target is added only for a declared native-only component. Public metadata must never supply arbitrary absolute destination paths, shell strings to execute, or secret values.

## Future dynamic Catalog API

If static querying becomes insufficient, a Cloudflare Worker may expose equivalent read routes and account-scoped write routes for publisher/private catalog operations. Public query endpoints remain read-only. Do not add server-side MCP invocation or credentials to this API.

## SDKs

### `@litepsm/catalog-client` (later)

Thin typed client for search, item details, source resolution, and InstallPlan display. It must not read machine secrets or write agent config.

### Local client library (later)

An embedded wrapper over the `litepsm` executable or local protocol. It may request local installs only through the same plan/approval path as CLI. Do not create parallel installer logic in every SDK.

### OpenAPI/schema

Publish JSON Schema for listing, source, compatibility, and InstallPlan. Generate language bindings only after the schema and CLI behavior stabilize. A TS SDK is convenience, not the canonical source of truth.

## Hosted Discovery MCP

An optional remote MCP endpoint can serve catalog discovery without authentication for public data (subject to abuse limits). Read-only tools:

```text
search_extensions(query, kinds?, source?, compatible_client?, limit?)
get_extension(id, version?)
prepare_install(extension_id, version?, client?, scope?) -> InstallPlan + human-readable summary
```

It must not expose `install`, `run`, `connect`, `call_tool`, credential write, or arbitrary fetch tools. It is public discovery only and has no machine access.

## Local LitePSM Bridge MCP

The local Bridge is registered once per supported host by `litepsm setup <client>`. Ordinary later installs update LitePSM's local store, not every agent's MCP configuration. The Bridge runtime is local; it may launch selected local MCP providers or connect to selected remote providers. Hosted LitePSM endpoints never proxy tool traffic.

The Bridge's small stable surface may include:

```text
search_catalog(query, kinds?, limit?)
get_extension(id, version?)
prepare_install(id, version?, components?) -> constrained InstallPlan
install_from_plan(plan_id) -> requires local user confirmation
list_installed(kind?, enabled?)
search_capabilities(query, limit?)
describe_capability(capability_id) -> provider schema + access facts
load_skill(skill_id, version?) -> skill instructions as untrusted content
call_capability(capability_id, arguments) -> provider result
```

`install_from_plan` is unavailable unless the host has a reliable user-confirmation path; otherwise the Bridge returns a CLI command for the user to run. `call_capability` accepts only a locally installed and enabled capability ID, validates arguments against the resolved provider schema, enforces local scope and action policy, and never accepts arbitrary commands, URLs, destinations, or secret values. Risky operations require an explicit local approval path; if one cannot be provided, the operation is denied or exposed through the provider's native host integration instead. The Bridge must keep unselected provider schemas out of the agent's default tool list and search/load capability details on demand.

## Source adapter interface

Build-time source adapter (not an untrusted in-process runtime plugin):

```text
source_type
validate_source_config
fetch_index(cursor)
fetch_item(upstream_id, version?)
normalize(raw) -> Listing + provenance
resolve_artifact(listing, version) -> source locator + immutable digest
```

Adapters run in CI with bounded network access and strict time/size limits. No arbitrary extensions can register executable ingestion code in the hosted catalog service during v1.
