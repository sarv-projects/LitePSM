# Research Ledger & Ecosystem Ground Truth

**Snapshot Date:** 2026-09-30 (external facts as researched)
**In-repo status last checked:** 2026-10-05 against [`STATUS.md`](../STATUS.md) and the tree at `ff0a1db` plus uncommitted Phase-0 documentation work.

Upstream specifications, client interfaces, and protocol revisions evolve rapidly. This ledger documents verified facts from primary sources that govern LiteSPM's architectural requirements.

> **Scope.** This is an *informative* document ([ARCH/00-INDEX](00-INDEX.md)). It records external facts and the consequences drawn from them; it makes **no delivery claims**. The *In-repo status* lines below say where each consequence actually stands today, using the fixed vocabulary defined in [`STATUS.md`](../STATUS.md) (`DESIGNED` / `IMPLEMENTED` / `WIRED` / `TESTED` / `VERIFIED` / `SHIPPED`). Where the two disagree, `STATUS.md` wins.

---

## 1. Model Context Protocol (MCP)

### 1.1 The 2026-07-28 Protocol Revision
*   **Stateless Request Architecture:** The historical stateful handshake (`initialize` followed by `initialized`) has been retired for stateless operations. Individual JSON-RPC requests carry protocol versioning, client identity, and capability negotiation within a top-level `_meta` field. This enables requests to be handled across independent, stateless instances behind load balancers.
*   **Streamable HTTP Transport:** Recommended primary transport replacing legacy Server-Sent Events (SSE). Utilizes a unified HTTP endpoint where client requests are sent via HTTP POST, and server responses stream via chunked or request-scoped bodies.
*   **Header Mirroring:** Key routing metadata (`Mcp-Method`, `Mcp-Name`) is mirrored into standard HTTP request headers, enabling reverse proxies and load balancers to route calls without deep packet payload inspection.
*   **Centralized Subscriptions:** Replaced fragmented `resources/subscribe` with a unified `subscriptions/listen` RPC.
*   **Server Discovery:** Optional `server/discover` RPC enabling clients to query supported protocol versions and capabilities upfront.
*   **Architecture Consequence:** LiteSPM must speak a **dual-protocol matrix**: modern stateless `2026-07-28` and legacy `2025-11-25`.
*   **In-repo status:** the matrix constants exist as `ProtocolModern2026` / `ProtocolLegacy2025` (`internal/mcpclient/types.go:14-15`), and `cmd/litespm/main.go:47` pins `ProtocolVersion = "2026-07-28"`. Two things this ledger previously implied are **not** true of the tree: LiteSPM does **not** depend on an official MCP SDK (`go.mod` has no MCP dependency), it implements both profiles itself in `internal/mcpclient` — which is `IMPLEMENTED` with zero production importers ([STATUS.md](../STATUS.md) §4); and the stateless `server/discover` RPC and SSE/chunked response reading are not implemented (the modern client POSTs one request per call and reads a complete body, `internal/mcpclient/client_2026.go:51-128`). The stdio shim that hosts actually boot is `WIRED` ([ARCH/14](14-BRIDGE-PROVIDER-MCP.md) §1).

### 1.2 Official MCP Registry & `server.json`
*   **Source Reference:** `https://registry.modelcontextprotocol.io/docs`
*   **Metadata Scope:** Standardized `server.json` catalog format indexing packages across multiple package ecosystems (npm, PyPI, Cargo, OCI, NuGet, MCPB) and remote Streamable HTTP endpoints.
*   **Architecture Consequence:** Registry metadata is strictly discovery data. The presence of a record does not imply that the user's workstation has the runtime prerequisites to execute the server.
*   **In-repo status:** `IMPLEMENTED` as an ingestion adapter only — `internal/source/mcp_registry.go` (`MCPRegistryAdapter`, source id `builtin:mcp-registry`, registered in `internal/source/sources.go:33-36` with `Format: "server.json"`). Per [STATUS.md](../STATUS.md) §2 the 8 source adapters have **test-only callers** and there is no `catalog build` CLI command, so nothing ingests the registry into a published release today. The discovery-only consequence is unchanged.

---

## 2. Agent Skills Specification

*   **Source Reference:** `https://agentskills.io/specification`
*   **Format:** A skill is a directory containing a mandatory `SKILL.md` file with YAML frontmatter and Markdown instruction content, accompanied by optional `scripts/`, `references/`, and `assets/`.
*   **Progressive Disclosure:** To preserve LLM context windows, skills are loaded in three tiers:
    1.  *Discovery:* Name and description only.
    2.  *Activation:* Full `SKILL.md` body.
    3.  *Execution:* Supporting files on demand.
*   **`allowed-tools` Experimental Status:** The `allowed-tools` frontmatter field is an experimental proposal intended to declare pre-approved tools. In practice, many agent implementations do not enforce this restriction.
*   **Architecture Consequence:** LiteSPM treats `allowed-tools` strictly as informational metadata. It **never** treats this field as an automatic authorization grant.
*   **In-repo status:** consequence `WIRED` for skills — `skills.list` / `skills.load_body` / `skills.read_resource` are registered in `cmd/litespm/main.go:1464-1616` and reached through the bridge tools `load_skill` / `read_skill_resource`. The parser `internal/skills/loader.go` never reads `allowed-tools`, so the field cannot grant anything ([ARCH/27 §1.2](27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md) records the parser's honest gaps). Authorization itself comes from `internal/policy` on install and skills paths (`WIRED`, [STATUS.md](../STATUS.md) §1).

---

## 3. Claude Code Plugin Marketplaces

*   **Source Reference:** `https://code.claude.com/docs/en/plugin-marketplaces`
*   **Format:** Git repositories containing `.claude-plugin/marketplace.json` defining a list of plugins.
*   **Source Types:** Plugins can specify sources including `github`, `git-subdir`, `archive`, `npm`, and `command`.
*   **Execution Hazard of `command` Sources:** The `command` source type executes an arbitrary local shell script to build or fetch the plugin.
*   **Architecture Consequence:** During catalog ingestion, executing arbitrary publisher commands is a severe vulnerability. LiteSPM strictly prohibits and rejects `command` sources in v1.
*   **In-repo status:** consequence `IMPLEMENTED` in the adapter and not reachable end-to-end — `internal/source/claude_marketplace.go:90-93` skips any plugin whose source `IsCommand()` (`internal/source/flex.go:131-134`), and `internal/source/flex.go:116` only normalizes known source kinds. Because the adapters are test-only ([STATUS.md](../STATUS.md) §2), the invariant holds by construction today; keep it under test when an ingest caller lands.

---

## 4. OpenAI Portable Agent Plugins

*   **Source Reference:** `https://developers.openai.com/plugins/build/plugins`
*   **Format:** A root `plugin.json` manifest combining skills (`skills/`), MCP servers (`mcp.json`), hooks, and optional assets.
*   **Architecture Consequence:** LiteSPM decomposes portable plugins into their constituent components, normalizing portable skills and MCP servers while preserving proprietary OpenAI metadata under `extensions.com.openai`.
*   **In-repo status:** decomposition is `IMPLEMENTED` in `internal/source/openai_plugin.go` (test-only callers). The *preservation* half of the consequence is `DESIGNED` only: no adapter writes an `extensions.com.openai` namespace, and `Component.HostExtensions` (`internal/domain/models.go:278`) is populated by no adapter today. Do not present preserved proprietary metadata as a feature.

---

## 5. Grok Build Plugin Marketplaces

*   **Source Reference:** `https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/09-plugins.md`
*   **Format:** Marketplaces defined by `.grok-plugin/marketplace.json` combining distinct component kinds (commands, agents, hooks, MCP, LSP).
*   **Pinning Requirement:** Remote plugin sources can be pinned to full 40-character commit SHAs.
*   **Architecture Consequence:** Full Git commit SHAs are required for reproducibility; mutable branches or tags cannot serve as immutable installation targets.
*   **In-repo status:** the pin check is `IMPLEMENTED` in the adapter (`isFullCommitSHA`, `internal/source/grok_marketplace.go:184`, `CommitSHA` at `:39`), again with test-only callers ([STATUS.md](../STATUS.md) §2). Enforcing pinning at *resolve → install* time is `DESIGNED` — the resolver consumes `ImmutableRef` values but nothing yet rejects a mutable ref ([ARCH/13](13-RESOLVER-INSTALL-ENGINE.md)).

---

## 6. Supply-Chain Provenance & Metadata Verification

*   **The Update Framework (TUF):** `https://theupdateframework.io/`
    *   Industry standard for secure software update systems, offering proven protection against key compromise, rollback attacks, and freeze attacks.
    *   *Decision:* When signed catalog releases are introduced, LiteSPM will adopt TUF rather than inventing a custom signature envelope.
    *   **In-repo status:** `DESIGNED`. Nothing TUF-shaped exists; [ARCH/36 §4](36-ENTERPRISE-POLICY-AND-AUDIT.md) is the design record, and it is explicitly downstream of publishing a release tree ([ARCH/31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)). The release tree **is** published and serves live (verified 2026-10-05, [STATUS.md](../STATUS.md) §2), but it is unsigned, so no catalog trust root can be layered on yet.
*   **Sigstore / Cosign:** `https://docs.sigstore.dev/cosign/`
    *   Standard for keyless and key-based artifact signing and verification.
    *   *Decision:* LiteSPM supports Cosign artifact digest verification where upstreams publish verification evidence.
    *   **In-repo status:** `DESIGNED`. Signing work is specified in [ARCH/36 §5](36-ENTERPRISE-POLICY-AND-AUDIT.md) (cosign-signed releases → SBOM → provenance). Today releases and packages are unsigned, `SECURITY.md` publishes `SHA256SUMS.txt` only, and `litespm self-update` verifies **SHA-256 only** with no signature check ([STATUS.md](../STATUS.md) §1, §5). SBOM generation itself is `DESIGNED` in [ARCH/32 §4.1](32-MANIFEST-LOCK-INTEROP.md).

---

## 7. Static Hosting & Cloudflare Pages Limits

*   **Limits:** Cloudflare Pages supports up to 20,000 files per project and up to 25 MiB per individual asset.
*   **Shipped dataset size:** the deployed catalog is **5,814 items** (`web/data/catalog.json`, a single ~3.4 MB blob: 4,079 `mcp`, 1,103 `skill`, 632 `plugin` — [ARCH/26 §4.2](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)), not the 500–1,000 records this ledger previously assumed.
*   **Implication for LiteSPM:** 5,814 records leave ample headroom under the 20,000-file ceiling, but the ceiling is binding on *per-item* output: emitting one static file per item (pages, shards, or `items/*.json`) consumes roughly a third of the budget before any asset, and the catalog is expected to grow. Consequences:
    *   The shard / `items/` / `index.json` tree drawn in [ARCH/18 §1-§2](18-CATALOG-BUILDER-RELEASE-SEARCH.md) is `DESIGNED` and **not emitted** — `CompileRelease` writes four files (`v1/current.json` + `v1/releases/<id>/{listings,versions,manifest}.json`, [STATUS.md](../STATUS.md) §2), and the deployed site is served from the single bundled `web/data/catalog.json`.
    *   Per-item static pages are therefore rejected in favour of the query-route detail page ([ARCH/26 §6.3](26-ECOSYSTEM-IA-PACKAGE-MODEL.md) cites this section as the source of the 20,000-file figure).
    *   Sharding by kind and category remains the plan to prevent single-file bloat, gated on a measured trigger ([STATUS.md](../STATUS.md) §2, "Catalog deltas / shards / FTS index").
