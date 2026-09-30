# High-level design

## Summary

LitePSM separates the **hosted catalog** from the **local extension manager and runtime**. The hosted surface serves public catalog data. A local client installs items into a LitePSM-managed store and, once per supported agent, registers one local LitePSM Bridge. The Bridge makes selected LitePSM-managed capabilities available to that agent. Secrets and execution stay on the user's device or with the selected upstream provider; the hosted market does not receive credentials or tool traffic.

```text
                 PUBLIC CONTROL / DISCOVERY PLANE
┌────────────────────────────────────────────────────────────┐
│ Private GitHub source repo                                 │
│  schemas · curated records · adapter definitions · site    │
└────────────────────────────┬───────────────────────────────┘
                             │ CI validates and builds
                             ▼
┌────────────────────────────────────────────────────────────┐
│ Cloudflare Pages (public)                                   │
│ website · static catalog API · search shards · release data│
└───────────────┬───────────────────────────────┬────────────┘
                │ HTTPS metadata                │ upstream link/digest
                ▼                               ▼
       LitePSM client on device          Publisher / registry / Git
                │                               │
       ┌─────────────┴──────────────┐
       ▼                            ▼
 LitePSM-managed store       Native-only components
 skills · MCP · bundles     explicit host adapter/export
       │                            │
       ▼                            ▼
 Local LitePSM Bridge       Selected agent/client
       │   ▲
       │   └── one-time host MCP registration
       ▼
 Selected local or remote MCP provider

Credentials: local OS secret store → local client/agent → provider as required.
LitePSM hosted services do not receive credentials or proxy tool calls.
```

## Components

### 1. Catalog source repository

The authoritative repository may be private. It contains schemas, curated/approved listing records, source adapter configuration, generated public catalog data, the website source, and client source. It must not contain credentials. The public deployment exposes only the built website and intended public metadata.

### 2. Catalog builder

Build-time code validates records, deduplicates only when identity evidence supports it, emits public JSON indexes and per-item records, and includes freshness/source timestamps. Source ingestion can run on a schedule or by reviewed pull request. The builder never executes MCP servers or plugin scripts.

### 3. Public catalog and website

Cloudflare Pages serves static files from a private GitHub repository. The web experience is public and read-only for launch. Static catalog files are the service contract, so search and resolution work without a database or always-running process.

### 4. LitePSM Client and local store

The local CLI/client downloads public metadata and package artifacts, verifies identity/digests, previews changes, installs supported components into a versioned LitePSM-managed local store, and records a local lock. It owns local installation effects. It does not need to rewrite an agent's configuration for each MCP server, skill, or compatible bundle.

### 5. LitePSM Bridge and host adapters

The LitePSM Bridge is a local MCP server/runtime configured once per agent through a small, versioned host adapter. After setup, the adapter does not need to edit the agent's configuration for every extension. The Bridge discovers LitePSM-managed skills and connected MCP providers, applies local enablement/permission policy, and exposes a bounded capability interface to the agent. It may launch local MCP processes or connect directly to selected remote MCP providers. Calls are mediated only on the user's device; they are never proxied by LitePSM's hosted market.

Host adapters have a narrow purpose: register/update/remove the single LitePSM Bridge entry, preserve unrelated settings, verify supported client versions, and provide a fallback setup snippet when safe automation is unavailable. Extension adapters handle components that genuinely require a host-native plugin or skill installation. Those are exceptions, not the per-extension path for all MCPs and skills.

### 6. Catalog API / SDK / Discovery MCP

Initially the hosted “API” is static HTTP JSON. Later an API-compatible Worker may add search, accounts, private catalogs, or publisher operations. A hosted Discovery MCP may offer public search/inspect/plan only. It has no machine access. The separately installed local Bridge can access the local store and configured providers under local policy.

“Accessible to future agents” means LitePSM offers stable integration surfaces (MCP, HTTPS API/SDK, and documented client adapters). It cannot make itself automatically available in an agent that does not support remote/local MCP, custom APIs, or a compatible installation adapter. Each host integration must be verified independently.

## Hosting and data flow

### Launch: static-only

- Private GitHub repository is the source of truth.
- Cloudflare Pages builds from that private repository and publishes the website plus catalog JSON.
- Public packages remain at their upstream locations where practical; LitePSM stores references, versions, and digests rather than mirroring code.
- The `litepsm` local client/Bridge can be distributed as a binary and/or npm package; npm is a bootstrap/distribution channel, not the catalog backend. The Bridge is installed once into each supported agent, not once per extension.
- No database, login, marketplace account, Worker, R2 bucket, or credential storage is required.

### Later: dynamic service only when needed

Add Cloudflare Worker + D1 for publisher accounts, private registries, review/moderation state, or dynamic query needs. Use R2 only if LitePSM deliberately mirrors artifacts. Keep runtime hosting and secret custody out of this service. Each added service feature requires a threat model and data-retention contract.

## Trust boundaries

1. Upstream listing content is untrusted data; it cannot command the LitePSM service or local client.
2. Installing a package is a local side effect and requires a preview plus user approval.
3. Starting an MCP provider or executing hooks/scripts is a separate local execution decision from downloading files. The Bridge only starts providers selected and enabled in local LitePSM state.
4. LitePSM's hosted API and Discovery MCP are not capability grants. The local Bridge is a capability entry point, so it must enforce local allowlists, scope, action policy, and audit rules rather than trusting a model's choice of nested provider/tool.
5. An advertised “compatible” or “verified” label must name the tested host, version, test date, and evidence.

## Availability and fallback

The client should cache catalog metadata and preserve installed package locks. If the public catalog is unavailable, already-installed Bridge capabilities continue to work from local state. New public search/resolution is unavailable until the catalog can be fetched or a configured source can be queried directly. If an agent is unsupported by a safe host adapter, LitePSM offers the website/CLI and native per-item export path without pretending the one-time Bridge setup succeeded.

## Scaling

Thousands of records are small enough for a generated index plus category/search shards and per-item manifests. Clients cache shards with ETag/Last-Modified and fetch package payloads only after the user chooses install. Search can start as client-side filtering; a dynamic search backend is unnecessary until measured performance says otherwise.
