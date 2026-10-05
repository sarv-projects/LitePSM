# Research Ledger & Ecosystem Ground Truth

**Snapshot Date:** 2026-09-30  
Upstream specifications, client interfaces, and protocol revisions evolve rapidly. This ledger documents verified facts from primary sources that govern LiteSPM's architectural requirements.

---

## 1. Model Context Protocol (MCP)

### 1.1 The 2026-07-28 Protocol Revision
*   **Stateless Request Architecture:** The historical stateful handshake (`initialize` followed by `initialized`) has been retired for stateless operations. Individual JSON-RPC requests carry protocol versioning, client identity, and capability negotiation within a top-level `_meta` field. This enables requests to be handled across independent, stateless instances behind load balancers.
*   **Streamable HTTP Transport:** Recommended primary transport replacing legacy Server-Sent Events (SSE). Utilizes a unified HTTP endpoint where client requests are sent via HTTP POST, and server responses stream via chunked or request-scoped bodies.
*   **Header Mirroring:** Key routing metadata (`Mcp-Method`, `Mcp-Name`) is mirrored into standard HTTP request headers, enabling reverse proxies and load balancers to route calls without deep packet payload inspection.
*   **Centralized Subscriptions:** Replaced fragmented `resources/subscribe` with a unified `subscriptions/listen` RPC.
*   **Server Discovery:** Optional `server/discover` RPC enabling clients to query supported protocol versions and capabilities upfront.
*   **Architecture Consequence:** LiteSPM must use an official MCP SDK and support a **dual-protocol matrix**: modern stateless 2026-07-28 and legacy 2025-11-25.

### 1.2 Official MCP Registry & `server.json`
*   **Source Reference:** `https://registry.modelcontextprotocol.io/docs`
*   **Metadata Scope:** Standardized `server.json` catalog format indexing packages across multiple package ecosystems (npm, PyPI, Cargo, OCI, NuGet, MCPB) and remote Streamable HTTP endpoints.
*   **Architecture Consequence:** Registry metadata is strictly discovery data. The presence of a record does not imply that the user's workstation has the runtime prerequisites to execute the server.

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

---

## 3. Claude Code Plugin Marketplaces

*   **Source Reference:** `https://code.claude.com/docs/en/plugin-marketplaces`
*   **Format:** Git repositories containing `.claude-plugin/marketplace.json` defining a list of plugins.
*   **Source Types:** Plugins can specify sources including `github`, `git-subdir`, `archive`, `npm`, and `command`.
*   **Execution Hazard of `command` Sources:** The `command` source type executes an arbitrary local shell script to build or fetch the plugin.
*   **Architecture Consequence:** During catalog ingestion, executing arbitrary publisher commands is a severe vulnerability. LiteSPM strictly prohibits and rejects `command` sources in v1.

---

## 4. OpenAI Portable Agent Plugins

*   **Source Reference:** `https://developers.openai.com/plugins/build/plugins`
*   **Format:** A root `plugin.json` manifest combining skills (`skills/`), MCP servers (`mcp.json`), hooks, and optional assets.
*   **Architecture Consequence:** LiteSPM decomposes portable plugins into their constituent components, normalizing portable skills and MCP servers while preserving proprietary OpenAI metadata under `extensions.com.openai`.

---

## 5. Grok Build Plugin Marketplaces

*   **Source Reference:** `https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/09-plugins.md`
*   **Format:** Marketplaces defined by `.grok-plugin/marketplace.json` combining distinct component kinds (commands, agents, hooks, MCP, LSP).
*   **Pinning Requirement:** Remote plugin sources can be pinned to full 40-character commit SHAs.
*   **Architecture Consequence:** Full Git commit SHAs are required for reproducibility; mutable branches or tags cannot serve as immutable installation targets.

---

## 6. Supply-Chain Provenance & Metadata Verification

*   **The Update Framework (TUF):** `https://theupdateframework.io/`
    *   Industry standard for secure software update systems, offering proven protection against key compromise, rollback attacks, and freeze attacks.
    *   *Decision:* When signed catalog releases are introduced, LiteSPM will adopt TUF rather than inventing a custom signature envelope.
*   **Sigstore / Cosign:** `https://docs.sigstore.dev/cosign/`
    *   Standard for keyless and key-based artifact signing and verification.
    *   *Decision:* LiteSPM supports Cosign artifact digest verification where upstreams publish verification evidence.

---

## 7. Static Hosting & Cloudflare Pages Limits

*   **Limits:** Cloudflare Pages supports up to 20,000 files per project and up to 25 MiB per individual asset.
*   **Implication for LiteSPM:** For an initial catalog of 500–1,000 records, the partitioned shard structure (under 2,000 generated JSON files) is well within platform thresholds. Sharding by kind and category prevents single-file bloat.
