# Ecosystem IA & Package Model

A normative decision record for reshaping the public LitePSM Market website and the catalog package model against the 2026 agent-extension ecosystem. This document is grounded in what the repository implements today; target-state contracts are labelled as targets and must not be presented as shipped behavior.

---

## 1. Status & Precedence

*   **Status:** Normative for the website information architecture, the public package-model vocabulary, and the source-adapter matrix. Informative for ecosystem observations and roadmap sequencing.
*   **Precedence:** Follows [25 — Web Frontend UI](25-WEB-FRONTEND-UI.md) and [10 — Domain Model](10-DOMAIN-MODEL.md). Where this document conflicts with `25` on navigation, routing, or detail-page shape, this document is the updated contract for the next milestone; `25` remains authoritative for the frozen stack (Next.js App Router, `output: 'export'`, Tailwind, Radix primitives, client-side search).
*   **Data/state authority unchanged:** [05 — Security](05-SECURITY.md) and [10 — Domain Model](10-DOMAIN-MODEL.md) retain authority over security and canonical identifiers. Nothing here weakens the one-bridge-per-host, Plan-bound, approval-gated install boundary.
*   **Honesty rule (binding):** any field this document specifies but the current dataset cannot populate MUST render as `Not published`, `Unknown`, or an explicit absence — never as an inferred, defaulted, or fabricated value. See §12.4.
*   **Index registration:** Adding this document to [00 — Index](00-INDEX.md) is a follow-up edit owned by the maintainer; this file does not modify the index.

---

## 2. Ecosystem Snapshot (Informative)

The set of objects a user can now install into an agent client has outgrown a single protocol or vendor format. Across the 2026 ecosystem the installable object types commonly encountered are:

| # | Object type | What it is | Typical portable form |
|---|---|---|---|
| 1 | MCP server | Tool/resource provider speaking MCP | `server.json` / `mcp.json` entry |
| 2 | Agent skill | Progressive-disclosure instructions + assets | `SKILL.md` directory |
| 3 | Agent plugin | Portable bundle of skills + MCP servers | `plugin.json` + `skills/` + `mcp.json` |
| 4 | Vendor plugin | Vendor-specific extension bundle | `.claude-plugin/`, `.grok-plugin/`, etc. |
| 5 | Connector | First-party auth/data bridge | Vendor-specific manifest |
| 6 | Subagent | Delegatable agent definition | Markdown/JSON agent definition |
| 7 | Mode / profile | Behavioural preset for an agent | Config fragment |
| 8 | Slash command | Named prompt entrypoint | Command file |
| 9 | Prompt template | Reusable prompt artifact | Markdown/text |
| 10 | Rule | Persistent instruction/policy for the agent | Rules file |
| 11 | Hook | Lifecycle event handler | Hook manifest |
| 12 | Workflow | Multi-step automation graph | Workflow definition |
| 13 | Automation | Triggered task/cron | Automation config |
| 14 | Native tool | Built-in agent tool (not portable) | Agent-internal |
| 15 | CLI | Executable tool surface | npm/PyPI/binary |
| 16 | LSP server | Language intelligence server | Language-server package |
| 17 | Provider adapter | Model/provider integration | Provider config |
| 18 | ACP agent | Agent speaking the Agent Client Protocol | `agent.json` |
| 19 | IDE extension | Editor extension package | `package.json`/`vsix` |
| 20 | Theme | Visual theme | Theme package |
| 21 | Canvas | Visual/interactive surface | Asset bundle |
| 22 | Policy pack | Security/governance rules | Policy document |
| 23 | Context / docs | Retrieval corpus / documentation | Markdown corpus |
| 24 | Bundle | Meta-package of the above | Any of the above |

Two 2026 developments are first-class for this design:

*   **Agent Plugins 1.0.0** is a vendor-neutral packaging standard (published 2026-08-06 by a multi-vendor Technical Steering Committee) for a directory containing a closed `plugin.json` manifest, `skills/`, and `mcp.json`. It standardises *packaging only*: it defines no install mechanism, distribution protocol, permission model, sandboxing, trust, or provenance. Its v1 component types are deliberately limited to **Agent Skills and MCP servers**; commands, hooks, agents, rules, and LSP servers are explicitly out of scope until their formats converge. A compatible client validates `plugin.json`, then validates each component independently.
*   The **ACP Registry** is a curated, CI-validated registry of **ACP-compatible agents** served as `registry.json`. It is a registry of *agents* (implementations), not of tools or packages, and each `agent.json` carries a `distribution` block (`binary` / `npx` / `uvx`). It is therefore a distinct object class from MCP servers, skills, and plugins, and must not be merged into a "tool registry" concept.

**Consequence:** the ecosystem is a many-to-many graph (one package can contain many component types; one component type can be sourced from many registries). Any top-level model that hard-codes one object type or one registry will misclassify most of the ecosystem and will require a breaking reshape again.

---

## 3. Core Architectural Decision: Neutral `Package` / `Capability`

### 3.1 Decision

The top-level public and internal abstraction is a neutral **`Package`** (user-facing synonym: **`Capability`**). A package is described by three **orthogonal axes** plus an **install adapter**:

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Package                                                                        │
│                                                                                │
│  type          What the package IS  (mcp | skill | plugin | agent | ...)       │
│  source        Where it came from   (registry | git | npm | pypi | oci | ...)  │
│  compatibility Which hosts/runtimes/OSes can use it, with evidence level       │
│  install       How it is materialised and launched (adapter strategy)          │
└──────────────────────────────────────────────────────────────────────────────┘
```

*   **`type`** — a closed, versioned taxonomy (§3.4). Determines icon, tags, filters, and the component semantics the detail page explains.
*   **`source`** — the ingestion family and its upstream identity (§5). Determines provenance, freshness, and "Available from".
*   **`compatibility`** — host/runtime/platform support, each with an **evidence level** (`verified` / `declared` / `unknown`). Never a single opaque score.
*   **`install` adapter** — the per-type materialisation strategy owned by the resolver/install engine (e.g. `mcp-stdio`, `npm-package`, `pypi-package`, `oci-image`, `git-tree`, `skill-copy`, `acp-npx`). This is distinct from `source` (metadata ingestion) and from `compatibility` (claims). It must not be conflated with either.

### 3.2 Explicitly rejected: "MCP is the top-level object"

MCP is one `type` and one `source` family, not the root. Making MCP the root would:
*   misclassify skills, portable plugins, ACP agents, connectors, and editor extensions;
*   couple the catalog schema to one protocol revision ([09 — Research](09-RESEARCH.md) §1.1 documents the dual-protocol matrix already required);
*   force every future object type through an MCP-shaped hole.

### 3.3 Explicitly rejected: "Plugin is the top-level object"

"Plugin" is overloaded: it denotes (a) the vendor-neutral Agent Plugins bundle, (b) vendor-private plugin formats, and (c) a curated multi-component toolkit. Using it as the root would:
*   mislabel MCP-only and skill-only entries as "plugins";
*   merge packaging (`type: plugin`) with provenance (`source`) and with curation (`collection`), collapsing three axes into one word;
*   duplicate the existing `plugin` listing kind, which already sometimes means "bundle of skills".

### 3.4 Internal taxonomy (normative list)

`type` is a closed enum with **eight** v1 members:

```text
mcp | skill | plugin | agent | rule | hook | tool | lsp
```

*   `mcp` — Model Context Protocol server (local stdio or remote HTTP).
*   `skill` — portable `SKILL.md` + resources, loaded on demand.
*   `plugin` — a bundle/packaging unit that may contain skills, mcp, rules, hooks, tools, lsp.
*   `agent` — an ACP-style agent runtime that can itself be installed/launched, and/or an agent definition.
*   `rule` — persistent instruction/guidance (`AGENTS.md`, cursor rules, etc.).
*   `hook` — program run on agent/tool lifecycle events.
*   `tool` — a native (non-MCP) tool extension.
*   `lsp` — language server definition consumed by an agent/host.

Notes:
*   `plugin` means a *bundle*; its child components carry their own `type` (`skill`, `mcp`, `hook`, `agent`, `lsp`, ...).
*   `agent` covers both installable agent executables (including ACP agents) and delegated subagent definitions.
*   `tool`/`lsp` are reserved v1 types for objects already visible in the ecosystem snapshot; the catalog may hold zero rows for some initially. An empty type is preferable to a fabricated classification.
*   `type` must remain additive-only within a schema major version. Removing or renaming a type requires a major-version decision recorded in [07 — Decisions](07-DECISIONS.md) and [00 — Index](00-INDEX.md).

### 3.4.1 Deferred to v2

The following values appeared in earlier drafts of this taxonomy. They are documented for continuity only and are **NOT** implemented in v1; they **MUST NOT** appear as first-class filters or types in the v1 website or CLI (§7.2, §10).

| Deferred value | Rationale |
|---|---|
| `connector` | First-party auth/data bridges remain vendor-specific; no portable v1 contract or catalog data. |
| `mode` | Behavioural presets are config fragments, not independently installable packages. |
| `command` | Slash commands are components carried by a plugin/skill, not a top-level package type. |
| `prompt` | Prompt templates overlap with skills and lack a stable portable manifest. |
| `workflow` | Multi-step automation graphs have no converged v1 format. |
| `automation` | Triggered/cron tasks belong to the scheduler/runtime, not the package catalog. |
| `cli` | Executable tool surfaces are materialised by an install adapter, not classified as a package type. |
| `provider` | Model/provider integrations are a runtime concern, not a discoverable package type. |
| `policy` | Security/governance rule packs have no v1 schema or ingestion adapter. |
| `context` | Retrieval corpora / documentation overlap with sources and need a dedicated model. |
| `ui` | Visual surfaces (themes, canvases, editor extensions) are out of the v1 discovery scope. |
| `agent-runtime` | Folded into `agent` for v1; a separate runtime type can be split out in v2 if formats diverge. |

The `agent-runtime` value is deferred as a *distinct* type only; v1 expresses installable agents through `type: agent` (§6.1).

### 3.4.2 v1 type support status (data reality)

Which v1 types are backed by real rows in the current 5,814-item catalog (`web/data/catalog.json`: 4,079 `mcp`, 1,103 `skill`, 632 `plugin`):

| v1 type | v1 catalog data | Rendering rule |
|---|---|---|
| `mcp` | Yes — 4,079 rows | Normal listing |
| `skill` | Yes — 1,103 rows | Normal listing |
| `plugin` | Yes — 632 rows | Normal listing |
| `agent` | No rows | Render as "no packages yet"; never fabricate entries |
| `rule` | No rows | Render as "no packages yet"; never fabricate entries |
| `hook` | No rows | Render as "no packages yet"; never fabricate entries |
| `tool` | No rows | Render as "no packages yet"; never fabricate entries |
| `lsp` | No rows | Render as "no packages yet"; never fabricate entries |

The five zero-row types remain valid enum members and filters; selecting them must produce an explicit empty state ("no packages yet"), not a synthesized or placeholder entry.

### 3.5 Target v2 record shape (not yet implemented)

```json
{
  "schemaVersion": 2,
  "id": "mcp:official-mcp-registry:io.modelcontextprotocol/filesystem",
  "slug": "io-modelcontextprotocol-filesystem",
  "type": "mcp",
  "name": "Filesystem MCP Server",
  "summary": "…",
  "source": {
    "sourceId": "official-mcp-registry",
    "sourceFamily": "official-registry",
    "upstreamId": "io.modelcontextprotocol/filesystem",
    "url": "https://registry.modelcontextprotocol.io/…"
  },
  "compatibility": {
    "hosts": [
      { "hostId": "claude-code", "status": "compatible", "evidence": "declared" }
    ],
    "runtimes": ["node"],
    "platforms": ["win", "mac", "linux"]
  },
  "install": { "adapter": "mcp-stdio", "command": "npx", "args": ["-y", "…"] },
  "signals": {
    "usage": "not-published",
    "security": "not-published",
    "updatedAt": null
  }
}
```

This shape is a **target contract**. It MUST NOT be emitted or rendered as if it were populated until the producing adapter and the builder actually supply the values.

---

## 4. Gap Analysis vs Current Repository

The current implementation is a v1 discovery catalog. The gaps below are factual, with the source of truth named.

### 4.1 Kind enum vs target type taxonomy

| Concern | v1 reality | Evidence | Gap vs target |
|---|---|---|---|
| Top-level classification | Nine-value enum: `{plugin, mcp, skill, connector, agent, rule, hook, tool, lsp}` | `internal/domain/models.go` (`KindPlugin`, `KindMCP`, `KindSkill`, `KindConnector`, `KindAgent`, `KindRule`, `KindHook`, `KindTool`, `KindLSP`) | Target `type` enum of eight v1 values (§3.4), with deferred values in §3.4.1; `connector` exists in Go but is a deferred v2 value and never appears in the shipped dataset (5814 rows carry only `mcp`/`skill`/`plugin`) |
| Web kind set | Three values: `"mcp" \| "skill" \| "plugin"` | `web/lib/telemetry.ts` (`Listing["kind"]`) | Web drops `connector` and the five zero-row v1 types; must align to the type taxonomy |
| Shipped dataset kinds | 4,079 `mcp`, 1,103 `skill`, 632 `plugin` (5,814 total) | `web/data/catalog.json` | No rows for the other v1 types (`agent`, `rule`, `hook`, `tool`, `lsp`) despite `ComponentKind` supporting some; deferred values (§3.4.1) are not tracked in v1 |
| Component kinds | `skill, mcp-provider, hook, command, agent-definition, agent, rule, tool, lsp, asset` | `internal/domain/models.go` (`ComponentKind`) | Component kinds cover all eight v1 types plus legacy `command`/`agent-definition`/`asset`, but are not surfaced to web |

### 4.2 Dataset fields vs target

`web/data/catalog.json` rows carry exactly (5,814 rows verified 2026-10-02):

```text
id, name, slug, kind, summary, category, publisher{name,verified,url},
stars (present on all rows, null on all rows — no popularity signal),
version, command, args, transport, runtime (mcp rows),
skillSource (skill rows), compatibleHosts (plugin rows),
installHint (441 rows)
```

| Signal | v1 present? | Evidence | Required treatment |
|---|---|---|---|
| `sources[]` (multi-source) | **No** — single implicit origin, no `sources[]` | `web/data/catalog.json` keys; `Listing` interface | Add `source` (target) / `sources[]` (multi-origin); render "Available from" |
| `tools[]` | **No** — `tools?` declared in the TS interface, 0 rows carry it | `web/lib/telemetry.ts`; key-frequency scan of `catalog.json` | Populate only from real MCP schema discovery; otherwise `Not published` |
| `effects[]` | **No** — `effects?` declared, 0 rows carry it | `web/lib/telemetry.ts`; `catalog.json` scan | Populate only from real `EffectDeclaration`; otherwise `Unknown` |
| usage / installs | **No** | no field in dataset or domain | `Not published` until a real telemetry source exists |
| `updatedAt` / maintenance | **No** — only `version`; no dates at all | `catalog.json` keys | `Not published`; do not infer from stars |
| security | **No** dedicated signal | `catalog.json` keys | Separate provenance signals (§7.4), not one score |
| stars | **Absent as a signal** — `stars` key is present on all 5,814 rows but `null` on all rows; 0 non-null values | `web/data/catalog.json` key-frequency scan; `scripts/build_full_catalog.py` (sets `"stars": None`, no popularity ranking) | Keep as **not published**; never present stars as a trust/usage signal (§12.4) |
| `categories` | Single flat `category` string, 63 distinct values | `catalog.json`; `web/lib/telemetry.ts` (`categoryFacets`) | Replace with hierarchical categories + tags |
| `testedHosts` | String array of display names (6–7 values, identical for many rows) | `catalog.json`; `scripts/build_full_catalog.py` | Replace with per-host compatibility facts carrying evidence level |
| `publisher.verified` | Boolean, set by the builder | `catalog.json` | Replace with separate source-verified / publisher-verified signals |

### 4.3 Trust signal status (fixed — verify, do not regress)

All eight compiled source adapters set `VerificationSummary.Level = "unverified"`:

*   `internal/source/mcp_registry.go`
*   `internal/source/skills.go`
*   `internal/source/claude_marketplace.go`
*   `internal/source/openai_plugin.go`
*   `internal/source/grok_marketplace.go`
*   `internal/source/codex_marketplace.go`
*   `internal/source/cursor_marketplace.go`
*   `internal/source/acp_registry.go`

This is the correct honest default: no signature check runs at ingestion. Per the honesty rule (§12.4), `unverified` MUST NOT be surfaced as a verification claim. A regression test (`TestOverridesAgainstRegistry` plus `acp_registry_test.go` asserting `"unverified"`) pins this; any adapter emitting `signature_verified` without a real check fails the strict honesty audit.

### 4.4 Routing, navigation, and categories

| Concern | v1 reality | Evidence | Gap vs target |
|---|---|---|---|
| Detail route | `/package/?slug=<key>` query route (static-export compatible; `slug` when unique, else full `id`) | `web/app/package/page.tsx`; links via `listingHref()` in `web/lib/catalog.ts` | Target keeps query-route shape; `slug` uniqueness validated at build time (§6.3) |
| Primary nav | Header renders `Explore / Agents / Categories / Trending` | `web/components/navigation/Header.tsx` (`PRIMARY_NAV`) | Implemented routes exist for all four; target nav adds `Collections`, `Sources`, `Security`, `Docs`, `Download` (§6) |
| Existing routes | `/`, `/explore`, `/package`, `/categories`, `/agents`, `/trending` | `web/app/` contains `page.tsx`, `explore/`, `package/`, `categories/`, `agents/`, `trending/`, `layout.tsx`, `globals.css` | Target ~16 routes (§6); `/collections`, `/sources`, `/publishers`, `/security`, `/docs`, `/download` remain missing |
| Categories | Flat `CategoryRail` pills; top 16–18 by frequency; 12 hard-coded labels in doc | `web/components/hero/CategoryRail.tsx`; `web/lib/telemetry.ts`; `ARCH/25` §3.2 | Replace with real hierarchical categories and `/categories/<slug>` |
| Filters | `kind` + `category` only | `web/lib/useCatalogSearch.ts` options; `web/app/page.tsx` | Target power filters (§7.2) |
| Detail tabs | Single long page with host config + related items | `web/app/package/page.tsx` | Target tabs Overview/Setup/Compatibility/Configuration/Files/Versions/Security/Reviews |
| Per-capability host snippet | `nativeSnippet(host, slug, command, args)` emits a direct native per-package config | `web/lib/hosts.ts` | Contradicts one-bridge-per-host; remove/relabel (§10, §11) |

---

## 5. Source Adapter Matrix (Normative Target)

Source adapters perform metadata ingestion only and never execute package code ([03 — Catalog Sources](03-CATALOG-SOURCES.md), [17 — Source/Artifact/Runtime Adapters](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md)). Artifact downloading (`internal/artifact/extractor.go`) and runtime materialisation remain separate concerns and are unaffected by this section.

| Source | Discovery mechanism | Adapter in `internal/source` today | v1 status |
|---|---|---|---|
| Official MCP Registry | Registry API / `server.json` | `mcp_registry.go` (`MCPRegistryAdapter`) | **Implemented** (metadata parse; npm/PyPI/Cargo/OCI/NuGet/MCPB packages + remotes) |
| Agent Skills / Skills.sh | `agentskills.io` directory + Git `SKILL.md` | `skills.go` (`AgentSkillsAdapter`) | **Implemented** (frontmatter parse; `allowed-tools` informational only) |
| Claude marketplace (incl. arbitrary Git marketplaces) | `.claude-plugin/marketplace.json` | `claude_marketplace.go` (`ClaudeMarketplaceAdapter`) | **Implemented** (flexible author/category/source shapes; skill bundles → skill components, `lspServers` → lsp components, opaque bundles → asset; `command` sources rejected). Registered: `git:anthropics-skills`, `git:claude-plugins-official`, `git:knowledge-work-plugins` |
| OpenAI Codex marketplaces | `.agents/plugins/marketplace.json` | `codex_marketplace.go` (`CodexMarketplaceAdapter`) | **Implemented** (declared `policy.authentication` recorded; opaque bundles → asset). Registered: `git:openai-plugins` (both `marketplace.json` and `api_marketplace.json`) |
| Cursor marketplaces | `.cursor-plugin/marketplace.json` | `cursor_marketplace.go` (`CursorMarketplaceAdapter`) | **Implemented** (opaque bundles → asset). Registered: `git:cursor-plugins` |
| OpenAI Codex plugins | `plugin.json` | `openai_plugin.go` (`OpenAIPluginAdapter`) | **Implemented** for the `plugin.json` shape; note the legacy `openai/skills` surface is deprecated and must not be treated as the forward path |
| Grok Build marketplaces | `.grok-plugin/marketplace.json` | `grok_marketplace.go` (`GrokMarketplaceAdapter`) | **Implemented** (flexible owner/source shapes; pinned commit SHA → `ImmutableRef`; opaque bundles → asset). Registered: `git:xai-plugin-marketplace` |
| GitHub repos + Releases | REST API / release assets / raw tree | — | **Missing** |
| npm | Registry API / dist-tags | — | **Missing** as first-class discovery (only reachable transitively via MCP Registry `registryType: npm`) |
| PyPI | JSON API / PEP 691 | — | **Missing** as first-class discovery (transitive via MCP Registry) |
| Docker / OCI | Registry catalog / manifests | — | **Missing** as first-class discovery (transitive via MCP Registry `oci`) |
| Cursor directory feed (cursor.directory) | Directory feed | — | **Missing** (the `.cursor-plugin/marketplace.json` manifest shape is implemented above) |
| GitHub Copilot plugins | Plugin manifest / marketplace | — | **Missing** |
| Gemini CLI extensions | Extension manifest | — | **Missing** |
| OpenCode (npm plugins) | npm package + `opencode` config convention | — | **Missing** |
| Cline (MCP marketplace + skills) | Marketplace API + skills feed | — | **Missing** |
| Roo | Marketplace/config feed | — | **Missing** |
| Zed extensions | Extension registry / `extension.toml` | — | **Missing** |
| ACP registry | `registry.json` of *agents* (`agent.json`) | — | **Missing** (distinct object class: agents, not tools) |
| VS Code Marketplace | Marketplace API | — | **Missing** |
| Open VSX | Registry API | — | **Missing** |
| Local filesystem | Directory scan / manifest | — | **Missing** as a discovery source (the runtime local-dir path exists for installs) |
| Arbitrary Git marketplace | User-supplied repo URL + marketplace manifest | — | **Missing** as a general source (Claude/Grok adapters cover their specific manifests only) |
| Agent Plugins 1.0.0 (`plugin.json` + `skills/` + `mcp.json`) | Fixed-directory bundle | — | **Missing** as an explicit portable-bundle adapter |

**v1 status legend:** `Implemented` = a compiled, non-dynamic adapter exists in `internal/source`. `Missing` = no compiled adapter exists; the row is a target, not a claim.

Normative constraints carried forward:
*   Source adapters stay statically compiled into the binary ([07 — Decisions](07-DECISIONS.md) D-014).
*   A source adapter must not download package bytes or execute scripts.
*   Agent Plugins is a packaging format with no trust or install semantics; ingesting it must not imply safe execution, and its two component types must map to `type: skill` and `type: mcp`.

---

## 6. Website Information Architecture (Normative)

### 6.1 Primary navigation

```text
Explore · Agents · Categories · Collections · Trending · Docs
```

`Agents` here means the *consumer* clients a package is compatible with (e.g. Claude Code, Codex, OpenCode, Cline). It is not the ACP-registry object class; ACP agents are packages with `type: agent` and are reachable from `Explore`.

### 6.2 Routes

| Route | Purpose (one line) |
|---|---|
| `/` | Curated, search-first landing that answers "what do you want your agent to do?" |
| `/explore` | Power-filter and sort surface over the full package set |
| `/package/<slug>` | Canonical detail page for a single package (identity, compatibility, install, tabs) |
| `/categories` | Hierarchical category index |
| `/categories/<slug>` | Packages within a category subtree |
| `/agents` | Index of supported consumer hosts with per-host readiness |
| `/agents/<id>` | Host detail: config path, bridge setup, compatible packages |
| `/sources` | Index of ingestion sources and their freshness/health |
| `/sources/<id>` | Source detail: mechanism, snapshot status, item count, provenance |
| `/publishers/<name>` | Publisher profile and their packages |
| `/collections` | Curated, editorial groups across types and sources |
| `/collections/<slug>` | A single curated collection |
| `/trending` | Time-windowed ranking (only when a real signal exists; otherwise an empty-state) |
| `/security` | Provenance/trust model explained in plain language |
| `/docs` | Authoring, publishing, compatibility, and CLI documentation |
| `/download` | LitePSM binary/CLI download and install instructions |

### 6.3 Detail-page serving model

The site is a static export (`web/next.config.ts`: `output: "export"`, `trailingSlash: true`). Detail pages are served as **query routes**:

```text
/package/?slug=<canonical-slug>
```

The HTML shell is one static file; the client resolves `slug` (via `useSearchParams`) against the catalog. This is the current `/package/?slug=` mechanism in `web/app/package/page.tsx` and keeps the export file count flat.

**Build-size risk (explicit):** the shipped dataset has **5,814 items**. Per-item static pages (`generateStaticParams` over all items) would add at least one HTML file per item, plus assets. The catalog builder is expected to grow. Cloudflare Pages is limited to **20,000 files per project** ([09 — Research](09-RESEARCH.md) §7). Per-item static pages are therefore **not adopted** unless a static-param budget is explicitly approved (e.g. prerender only the top N by a real signal, with the rest served via the query route). This document rejects unbounded `generateStaticParams` for the full catalog.

Identity note: `slug` must be globally unique. v1 `slug` values are derived per-source and may collide. The target contract uses a canonical slug (or the full package `id`) as the lookup key and validates uniqueness at build time.

---

## 7. Page Specifications

### 7.1 `/` — curated, sparse, search-first

*   One dominant element: the search field, with a job-based prompt: **"What do you want your agent to do?"**
*   Job-based chips (intent, not technology): e.g. `Query a database`, `Browse the web`, `Review a PR`, `Edit documents`, `Ship a release`. Each chip maps to a saved filter set and routes to `/explore?...`.
*   At most 2–3 curated rows (e.g. Editor's picks, New this week *only if a real recency signal exists*, Popular across types). No infinite walls of cards.
*   Telemetry badge remains bound to `/v1/current.json` and must show `offline`/fallback state rather than a fake "live" indicator (already the behavior in `web/lib/telemetry.ts`).
*   Progressive disclosure: casual layout by default; the full filter surface lives on `/explore`.

### 7.2 `/explore` — power filters

Filter groups:

| Group | Options |
|---|---|
| Package type | Any of the eight v1 `type` values (§3.4); deferred values (§3.4.1) are **not** v1 filters |
| Agent compatibility | Each supported host |
| Source / registry | Per §5 (e.g. Official MCP Registry, GitHub, npm, ACP Registry) |
| Official vs community | Derived from `source` + publisher verification |
| Verified | Source-verified / publisher-verified (separate toggles) |
| Auth required | `none` / `oauth2` / `api_key` / `unknown` |
| Local / remote | stdio/local vs remote endpoint |
| Runtime | node / python / docker / binary / … |
| Platform | win / mac / linux |
| License | SPDX (only when present; otherwise excluded from facet) |
| Popularity | Stars (illustrative only, clearly labelled) |
| Recently updated | Only when `updatedAt` is genuinely present |

Sort: **relevance · trending · most installed · recently updated · newest**.

Rules:
*   Facets with no real data are omitted, not shown with fabricated zeroes.
*   `most installed`/`trending` are disabled with an explanatory empty state until a real usage signal exists (§12.4).

### 7.3 `/package/<slug>` — detail

Identity block (npm-style): icon/avatar, canonical name (monospace), publisher, type tag, category tag, one-line summary, `Available from` source list (§9), stars (labelled illustrative), version.

Compatibility matrix: rows = consumer hosts, columns = status (`verified` / `compatible` / `unknown`) and evidence (`declared` / `tested` / `none`). This is the primary differentiator and must be visible without scrolling past the fold on desktop.

Install block: the correct install command (`litepsm install <id>`), plus the **one** host bridge snippet selected by host + OS (never a per-package native config; see §10).

Tabs:
`Overview · Setup · Compatibility · Configuration · Files · Versions · Security · Reviews`

*   `Overview` — sanitized description/README.
*   `Setup` — install + host bridge registration.
*   `Compatibility` — matrix detail with evidence provenance.
*   `Configuration` — env vars, secrets required, args (only when known).
*   `Files` — bundle contents (skills, mcp, rules, hooks, tools, agents, lsp) when the adapter exposes them.
*   `Versions` — version history with immutable refs when available.
*   `Security` — separate provenance signals (§7.4).
*   `Reviews` — user reviews; **empty state** until a backend exists (the site is static-only today, so this tab is a deliberate, clearly-labelled placeholder or is omitted entirely until storage/API exists).

LitePSM compatibility checklist: an explicit list of what LitePSM can and cannot do for this package (e.g. `install`, `launch provider`, `manage secrets`, `unsupported component types`), derived from `SupportedByLitePSM` and install-adapter support.

### 7.4 Trust & provenance UX

Never a single mystery score. Present **separate, individually sourced signals**:

| Signal | Meaning | Unknown state |
|---|---|---|
| Source verified | The ingestion source asserts control of the listing | `Unknown` |
| Publisher verified | The publisher identity is confirmed | `Unknown` |
| Checksum | An immutable digest is published and checked | `Not published` |
| Schema | Tool schemas were fetched and fingerprint-pinned | `Not published` |
| Effects | Declared filesystem/network/process effects and their provenance | `Unknown` |
| Secrets | Required credentials listed | `Not published` |
| Runs executable | The package launches a process (vs data-only) | `Unknown` |

Each signal links to `/security` for the definition. Absence is rendered as an explicit unknown state, never as a green check.

---

## 8. Card Specification

Restrained, Docker/Cursor-style; information-dense, no marketing chrome.

```text
┌───────────────────────────────────────────────────────────┐
│ [icon]  package-name                 ★ 12.4k  (illustr.) │
│         by publisher                                      │
│         One to two line summary of what it does.          │
│         [type] [category]                                 │
│         Compatible: ▪ ▪ ▪ ▪                              │
└───────────────────────────────────────────────────────────┘
```

Rules:
*   Elements: icon/avatar, name, publisher, 1–2 line description, **exactly two tags** (type + category), host-compatibility chips, stars/installs.
*   **No oversized install CTA.** Clicking the card opens `/package/?slug=…`. Install lives on the detail page.
*   Package names and commands render in monospace.
*   Popularity is labelled illustrative until a real usage source exists.
*   The whole card is one keyboard-focusable link with a visible focus ring (WCAG 2.2).

---

## 9. LitePSM Differentiator: Compatibility-First

The category has no shortage of package lists. LitePSM's defensible difference is answering **"which of my agents can actually use this?"**.

*   Cards surface host-compatibility chips as a first-class element (not buried metadata).
*   The detail page leads with the compatibility matrix.
*   The detail page exposes **`Available from`**: the list of sources (`sourceId`, family, upstream link) the package is discoverable from. Multi-source packages show every origin.
*   Compatibility carries an evidence level so `declared` is never shown as `verified`.

---

## 10. Explicit Separation: Public Website vs `/marketplace` TUI

Two different product surfaces with different jobs. Neither is collapsed into the other.

| | Public website (LitePSM Market) | `/marketplace` CLI/TUI |
|---|---|---|
| Audience | Anonymous discovery, evaluation, sharing | Installed user on their workstation |
| Job | Discover · browse · compare · trust · publish | Search · install · configure · update · remove |
| State | Read-only, static, no account required to browse | Stateful local daemon, Plan/Approval gated |
| Trust surface | Provenance signals and compatibility | Local verification, effects, approvals, audit |
| Routing | URL routes (§6) | In-agent commands and panels |

Normative rules:
*   Do not shrink website pages into TUI panels; the TUI is command-oriented and token-frugal.
*   Do not expand the static website into an install engine; installation stays on the local daemon ([07 — Decisions](07-DECISIONS.md) D-001, D-007).
*   The website may *show* the install command and host bridge snippet, but never performs installation.

---

## 11. Change Ledger

Organised by change class. Each row: **item · why · target milestone** (M1 website IA, M2 data model + source adapters, M3 trust/security signals, M4 publisher/collections, M5 TUI, v2 post-v1 deferred capability types).

### 11.1 Design

| Item | Why | Milestone |
|---|---|---|
| Adopt neutral `Package`/`Capability` with `type`/`source`/`compatibility` axes | Ecosystem is many-to-many; avoids another breaking reshape | M2 (model), M1 (vocabulary) |
| Compatibility-first card and detail layout | The one durable differentiator | M1 |
| Separate provenance signals instead of one score | Honest, auditable trust UX | M3 |
| Hierarchical categories + tags | 63 flat categories are unusable as navigation | M1 (route), M2 (data) |
| Query-route detail pages instead of per-item static pages | 5,814 items vs 20k file limit | M1 |
| Website and TUI as distinct surfaces | Different state models and audiences | M1/M5 |

### 11.2 Add

| Item | Why | Milestone |
|---|---|---|
| Routes: `/explore`, `/package`, `/categories(+/<slug>)`, `/agents(+/<id>)`, `/sources(+/<id>)`, `/publishers/<name>`, `/collections(+/<slug>)`, `/trending`, `/security`, `/docs`, `/download` | IA in §6 | M1 |
| Implement the eight v1 `type` values (`mcp`, `skill`, `plugin`, `agent`, `rule`, `hook`, `tool`, `lsp`) | §3.4 | M2 |
| Extend the `type` enum with the v2-deferred values (`connector`, `mode`, `command`, `prompt`, `workflow`, `automation`, `cli`, `provider`, `policy`, `context`, `ui`, `agent-runtime`) | §3.4.1; explicitly **not** v1 | v2 |
| `source` object with `sources[]` for multi-origin | "Available from" and provenance | M2 |
| `compatibility` matrix with per-host evidence level | Differentiator; replaces `testedHosts` | M2 |
| Install adapter registry (per-type strategy) | Decouple install from source/type | M2 |
| Source adapters for GitHub, npm, PyPI, OCI, Skills.sh, Cursor, Copilot, Gemini CLI, OpenCode, Cline, Roo, Zed, ACP Registry, VS Code Marketplace, Open VSX, local filesystem, arbitrary Git, Agent Plugins | §5 | M2 |
| Publisher/collection entities and pages | Editorial + attribution | M4 |
| `/security` trust explainer | Make signal definitions explicit | M3 |

### 11.3 Refactor

| Item | Why | Milestone |
|---|---|---|
| Replace `ListingKind` `{plugin,mcp,skill,connector,agent,rule,hook,tool,lsp}` with `type` taxonomy | Misclassification | M2 |
| Replace flat `category` string with hierarchical categories + tags | Navigation | M2 |
| Replace `testedHosts: string[]` with compatibility facts | Evidence level required | M2 |
| Rework Header nav to `Explore · Agents · Categories · Collections · Trending · Docs` | Current nav is `Explore / Agents / Categories / Coverage→/trending/`; `Collections`, `Sources`, `Security`, `Docs`, `Download` remain missing | M1 |
| Rework `CategoryRail` (fixed pills) into category entry points | Real hierarchy | M1 |
| Rework detail page into tabbed layout per §7.3 | Depth without a wall of text | M1 |
| Rework search from lexical `matchesQuery` + kind/category to filtered power search | §7.2 | M1/M2 |

### 11.4 Update

| Item | Why | Milestone |
|---|---|---|
| `web/lib/telemetry.ts` `Listing` interface to target v2 fields | Keep web model truthful | M2 |
| Catalog builder output to emit v2 shape (`type`/`source`/`compatibility`) | Data must lead UI | M2 |
| `ARCH/25` navigation and detail references to match this document | Doc drift | M1 |
| `ARCH/00-INDEX` to register this document | Discoverability | Done (registered with 26–30) |
| Stars absent: render `Not published` | `stars` is `null` on all 5,814 rows; no popularity signal exists | M3 |
| `VerificationSummary` stays `unverified` unless evidenced | Fixed — all 8 adapters emit `unverified`; regression-pinned, do not regress | M3 |

### 11.5 Remove

| Item | Why | Milestone |
|---|---|---|
| `/item?id=` route and all links to it | Replaced by `/package/?slug=` (no `/item` route exists) | Done |
| Flat 12-label category rail | Replaced by real hierarchical categories | M1 |
| Per-capability (as opposed to per-host) install snippets, i.e. `nativeSnippet(host, slug, command, args)` and the "Direct Native" toggle | Contradicts one-bridge-per-host (D-010); bypasses the daemon | M1 |
| Single Boolean `publisher.verified` as a trust badge | Replaced by separate source/publisher verification signals | M3 |
| `signature_verified` defaults from source adapters | Fixed — adapters emit `unverified`; keep regression test | Done |
| Any "Runs executable" green state without evidence | Honesty rule | M3 |
| `Reviews` tab or any feature requiring a backend | Site is static-only; don't fake it | M1 |

*If no per-capability install snippet remains after inspection, the removal row is satisfied and should be recorded as such rather than re-applied.* The row is now satisfied: `nativeSnippet` and the "Direct Native" toggle were removed from `web/lib/hosts.ts` and `web/app/package/page.tsx`, and the detail route renders only the single per-host `litepsm` bridge entry.

---

## 12. Phased Roadmap & Acceptance

### M1 — Website IA

*   Deliver: nav rename, §6 routes as static query/data pages, `/package/?slug=`, tabbed detail shell, compatibility-first card; `/item` already removed, implemented nav links (`Explore/Agents/Categories/Coverage`) verified.
*   Acceptance (observable evidence):
    *   `cd web && npx tsc --noEmit` exits 0.
    *   `cd web && npm run build` exits 0 and produces `web/out/`.
    *   Route list: the export contains a directory per implemented route (`out/explore/index.html`, `out/package/index.html`, `out/categories/index.html`, `out/agents/index.html`, `out/trending/index.html`), `out/item/` is absent, and the remaining §6 routes (`sources`, `collections`, `security`, `docs`, `download`, `publishers`) are tracked as missing.
    *   No nav link 404s.

### M2 — Data model + source adapters

*   Deliver: `Package` v2 shape, the eight-value v1 `type` taxonomy (§3.4), `source`/`sources[]`, `compatibility`, install adapter registry, new compiled source adapters.
*   Acceptance:
    *   `go build ./...` and `go test ./...` pass in the repo root.
    *   Builder emits v2 records; a schema/round-trip test proves every emitted record validates against the v2 contract.
    *   Each newly compiled adapter has a fixture-backed test (no network at test time).
    *   Web consumes v2 without fabricating absent fields.
    *   Slug uniqueness check fails the build on collision.

### M3 — Trust / security signals

*   Deliver: separate provenance signals, `/security`, evidence levels, remove unsupported `signature_verified` defaults.
*   Acceptance:
    *   Every trust signal renders one of `verified`/`declared`/`unknown`/`not-published`; no signal renders green without an evidence source.
    *   A test asserts adapters do not hard-code `signature_verified`.
    *   Manual review of a sample package page shows checksum/schema/effects as `Not published` when absent.

### M4 — Publisher / collections

*   Deliver: `/publishers/<name>`, `/collections(+/<slug>)`, editorial curation.
*   Acceptance:
    *   `tsc --noEmit` and `next build` pass; the route list includes the new surfaces.
    *   Collections reference only existing package IDs (build-time validation).

### M5 — TUI

*   Deliver: `/marketplace` search/install/configure/update/remove parity with the website's vocabulary, without collapsing the two surfaces (§10).
*   Acceptance:
    *   `go test ./...` passes.
    *   A documented manual run shows `/marketplace` search and install flows using the same `type`/`source`/`compatibility` vocabulary as the website.

### v2 (post-v1) — Deferred capability types

*   Deliver: promote one or more of the §3.4.1 deferred values (`connector`, `mode`, `command`, `prompt`, `workflow`, `automation`, `cli`, `provider`, `policy`, `context`, `ui`, `agent-runtime`) to first-class types only after a portable format, an ingestion adapter, and real data exist.
*   Acceptance:
    *   Each promoted value has a compiled source adapter and at least one real catalog row.
    *   The enum change is recorded as an additive schema-major decision in [07 — Decisions](07-DECISIONS.md).
    *   The eight v1 types remain stable; no v1 surface regresses.

### 12.4 Binding rule: do not fabricate usage/security data

No surface may display, sort by, or rank on a usage, install-count, download, security, audit, or maintenance signal that is not produced by a real, attributable source. Where the dataset has no such field:

*   omit the facet, or
*   render an explicit `Not published` / `Unknown` empty state.

Stars present in the v1 dataset are **illustrative only** (the builder seeds them from hard-coded maps), must be labelled as such, and must not be promoted to a trust or usage signal. Review counts, "verified" checkmarks, and security grades are forbidden until a real source exists.

---

## 13. Open Questions (Tracked, Not Decided Here)

*   Canonical slug algorithm (global slug vs full `id`) and its collision policy.
*   Whether any recency signal can be sourced honestly (GitHub release timestamps are a candidate, but only the GitHub adapter could supply them).
*   ACP registry ingestion scope: agents are an object class distinct from packages; confirm whether they belong in the same catalog or a sibling index.
*   Host list for the compatibility matrix must be derived from `internal/host/*.go` (cline, pi-agent, grok-build, claude-code, codex, opencode) and kept in sync with `web/lib/hosts.ts`.
