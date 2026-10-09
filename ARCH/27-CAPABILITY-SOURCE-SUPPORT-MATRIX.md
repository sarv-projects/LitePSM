# Capability & Source Support Matrix

A research-backed reference describing, for each of the eight v1 capability types and for each catalog source, (a) what the object actually is, (b) where it comes from, (c) how agent hosts consume it, and (d) whether this repository implements it today. The compiled `ListingKind` enum has **nine** members (the eight v1 targets plus the deprecated `connector`); the v1 *target* taxonomy is eight. Implementation status is derived by reading the code, not from intent.

This document is the companion to [26 — Ecosystem IA & Package Model](26-ECOSYSTEM-IA-PACKAGE-MODEL.md). `26` defines the normative target model (`type` / `source` / `compatibility` axes) and its milestone vocabulary. This document is a **status snapshot**: it describes reality and names the exact files/functions that back each claim. It introduces no new normative contract.

---

## 0. Scope, honesty rule, and reading the status labels

*   **Grounding rule (binding):** a capability type is `Working` only when a compiled code path exists and is exercised by a passing test in `internal/`. `Partial` means some layer exists (typically metadata ingestion) but the end-to-end path does not. `Not implemented` means no code path exists. Where a type has no catalog rows, that is stated as an absence, never inferred.
*   **No fabricated evidence:** a passing unit test is not runtime acceptance. No Windows/host runtime acceptance record exists for the types below; see §4.
*   **Concurrent work:** `internal/` and `ARCH/26` are owned by a parallel workstream. During this investigation the ACP adapters (`internal/source/acp_registry.go`, `internal/agent/`) and the domain kind constants landed in the working tree and were subsequently committed as `09ba4cb feat(agent,taxonomy): ACP agent adapter and registry ingestion`. That change is reflected here and explicitly labelled Partial / In progress (§1.4, §2, §4), because ingestion and launch-spec resolution are not the full install/launch path.
*   **Snapshot provenance:** repository state read 2026-10-01; ecosystem facts verified live on the same date.

Status legend used throughout:

| Label | Meaning |
|---|---|
| **Working** | Compiled code path exists in `internal/` and is covered by a passing test. |
| **Partial** | Some layer exists (e.g. metadata ingestion) but the full path (fetch, materialise, launch, mediate) is incomplete. |
| **Partial / In progress** | Another workstream is actively landing this; present in the working tree, not yet complete or committed. |
| **Not implemented** | Only type constants may exist; no adapter, parser, or runtime path. |

---

## 1. The v1 Capability Types

The v1 target taxonomy is a closed enum of eight members — `mcp | skill | plugin | agent | rule | hook | tool | lsp` ([ARCH/26 §3.4](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)). The compiled `ListingKind` enum in `internal/domain/models.go` has **nine** values, adding the deprecated/deferred `connector` (`KindConnector`, v2 per [ARCH/26 §3.4.1](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)); that value MUST NOT surface as a v1 filter or type. `ComponentKind` additionally carries `command`, `agent-definition`, and `asset`. Domain constants are groundwork, not an implementation.

### 1.1 `mcp` — Model Context Protocol server

**(a) Definition & format.** An MCP server is a tool/resource provider that speaks the Model Context Protocol, an open JSON-RPC 2.0 protocol over `stdio` or Streamable HTTP (with legacy SSE). Discovery metadata uses the portable `server.json` schema (name, description, `packages[]` with `registryType` of `npm|pypi|cargo|oci|nuget|mcpb`, and/or `remotes[]` with `url` + `transport` + `authType`). Authoritative spec: <https://modelcontextprotocol.io/specification/2026-07-28>; registry metadata schema: <https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/draft/server.schema.json>.

**(b) Where published/indexed.** The Official MCP Registry (<https://registry.modelcontextprotocol.io>, preview, v0.1 API) hosts `server.json` metadata that points at npm / PyPI / Cargo / OCI / NuGet packages or remote endpoints. It is a metaregistry intended for downstream aggregators, not for direct host consumption (<https://modelcontextprotocol.io/registry/about>). Community marketplaces (Cline, Cursor, Glama, mcpservers.org) re-index it.

**(c) How hosts consume it.** A host records a server entry in its config file, then launches it or connects to it at runtime. Shapes vary by host: `"mcpServers": { "name": { "command": ..., "args": [...] } }` (Claude Code `/mcpServers`, Cline, Pi Agent), `mcp` / `mcp.servers` with `"type": "local"` and a combined string-array command (OpenCode), and `[mcp_servers.<name>]` TOML tables (Codex, Grok Build). The active transport is the MCP protocol itself.

**(d) Repository status — Working (metadata + bridge); Partial end-to-end.**
*   `internal/source/mcp_registry.go` — `MCPRegistryAdapter.Ingest` parses `server.json` as an array or a single object, maps `registryType` to `domain.ArtifactType` (`npm→ArtifactNPM`, `pypi→ArtifactPyPI`, `cargo→ArtifactCargo`, `oci→ArtifactOCI`, `mcpb→ArtifactMCPB`), emits `KindMCP` listings with `ComponentMCPProvider` components and a `RuntimeDescriptor`; remotes become `remote-server` components with an endpoint. Tested by `TestMCPRegistryAdapterIngest` in `internal/source/source_test.go`.
*   `internal/bridge/shim.go` — the stdio shim exposes the 12 canonical tools over JSON-RPC (`initialize` advertises `protocolVersion: "2026-07-28"`).
*   `internal/host/*.go` — each adapter injects a single `litespm` bridge entry into the host's MCP config (`claudecode.go`, `cline.go`, `codex.go`, `grokbuild.go`, `opencode.go`, `piagent.go`).
*   `internal/mcpclient`, `internal/provider` provide the client and supervisor layers.

**Honest gaps.** The source adapter consumes caller-supplied `rawFeed` bytes; it performs no HTTP fetch of the registry (that lives elsewhere or not yet). `CompatibilitySummary` is always empty and `VerificationSummary.Level` is hard-coded `"unverified"`. There is no signature/attestation check and no runtime acceptance record.

### 1.2 `skill` — Agent Skill (`SKILL.md`)

**(a) Definition & format.** A skill is a directory whose only required file is `SKILL.md`: YAML frontmatter plus a Markdown instruction body, optionally alongside `scripts/`, `references/`, and `assets/`. `name` and `description` are required; `license`, `compatibility`, `metadata`, and the experimental `allowed-tools` are optional. Loading is progressive-disclosure: metadata first, body on activation. Authoritative spec: <https://agentskills.io/specification>.

**(b) Where published/indexed.** The `agentskills.io` directory, the community `skills.sh` index (<https://skills.sh>), GitHub repositories, and host-specific skill folders (Cline ships `github.com/cline/skills`; Claude, Codex, Copilot, Gemini, and Cursor each define discovery directories).

**(c) How hosts consume it.** Skill folders are copied into a host skill directory (for example `.claude/skills/`, `.agents/skills/`, `.gemini/skills/`, `.cline/skills/`, `.cursor/skills/`) and loaded by the host at runtime through progressive disclosure. The `allowed-tools` field is informational in the spec and confers no execution rights.

**(d) Repository status — Working (metadata) / Partial (runtime).**
*   `internal/source/skills.go` — `AgentSkillsAdapter.Ingest` parses a caller-supplied `map[filename][]byte`, and `ParseSkillMarkdown` extracts frontmatter (`name`, `title`, `description`, `author`, `version`, `categories`, `keywords`, `triggers`, `tools`) and the body. Emits `KindSkill` + `ComponentSkill`. Tested by `TestAgentSkillsAdapterIngest` in `internal/source/source_test.go`.
*   `internal/skills/` implements progressive disclosure; `bridge/shim.go` exposes `load_skill` and `read_skill_resource`.
*   The source doc `ARCH/03-CATALOG-SOURCES.md` §3 states `allowed-tools` is informational only, matching the spec.

**Honest gaps.** `ParseSkillMarkdown` is a hand-rolled line scanner, not a YAML parser: it cannot handle nested maps, block scalars, quoted colons, or duplicate keys, and it ignores unknown/optional fields such as `license` and `allowed-tools`. Version defaults to `"1.0.0"` when absent. Documents are passed in; there is no HTTP/Git fetch in the adapter. `tools:` is captured as `ToolsUsed` but remains informational.

### 1.3 `plugin` — packaging bundle

**(a) Definition & format.** A plugin is a packaging unit that bundles components. There are two families:
*   **Vendor-neutral:** Agent Plugins 1.0.0 — a directory with a closed `plugin.json` manifest, `skills/`, and `mcp.json` (v1 component scope is deliberately limited to Agent Skills and MCP servers). Spec: <https://agent-plugins.org/specification>.
*   **Vendor-specific:** Claude Code `.claude-plugin/plugin.json` + `.claude-plugin/marketplace.json` (<https://code.claude.com/docs/en/plugins/overview>); Grok Build `.grok-plugin/marketplace.json` with full-commit-SHA pinning (<https://github.com/xai-org/plugin-marketplace>); GitHub Copilot `.github/plugin.json` (<https://docs.github.com/en/copilot/concepts/agents/about-plugins>); Gemini CLI `gemini-extension.json` (<https://google-gemini.github.io/gemini-cli/docs/extensions/>); OpenCode TypeScript/npm plugins (<https://opencode.ai/docs/plugins/>); Cursor plugins (<https://cursor.com/docs/plugins>).

**(b) Where published/indexed.** Claude marketplaces (any Git repo with `.claude-plugin/marketplace.json`), the xAI plugin marketplace, the Cursor Marketplace and `cursor.directory`, the GitHub Copilot marketplace, Gemini CLI's extension gallery, npm (OpenCode), and the VS Code / Open VSX extension registries.

**(c) How hosts consume it.** The host reads a marketplace manifest and installs the plugin directory; the plugin's children (`skills/`, `.mcp.json`, `hooks/`, `agents/`, rules, LSP) are then loaded by the host through their own mechanisms. Agent Plugins 1.0.0 standardises packaging only — not install, trust, or execution.

**(d) Repository status — Working (three vendor adapters) / Partial overall.**
*   `internal/source/claude_marketplace.go` — `ClaudeMarketplaceAdapter.Ingest` parses `.claude-plugin/marketplace.json` (flexible author/category/source shapes), emits `KindPlugin` with skill/lsp/asset components as declared, and **strictly rejects** source type `command` (test `TestClaudeMarketplaceAdapter` asserts the malicious entry is dropped; `TestClaudeMarketplaceRealShapes` covers skill bundles and LSP maps).
*   `internal/source/grok_marketplace.go` — `GrokMarketplaceAdapter.Ingest` parses `.grok-plugin/marketplace.json` (flexible owner/source shapes; pinned commit SHA → `ImmutableRef`), emits `KindPlugin` (test `TestGrokMarketplaceAdapter`, `TestGrokMarketplaceRealShape`).
*   `internal/source/codex_marketplace.go` — `CodexMarketplaceAdapter.Ingest` parses `.agents/plugins/marketplace.json` (test `TestCodexMarketplaceAdapter`).
*   `internal/source/cursor_marketplace.go` — `CursorMarketplaceAdapter.Ingest` parses `.cursor-plugin/marketplace.json` (test `TestCursorMarketplaceAdapter`).
*   `internal/source/sources.go` — checked-in registry of known upstream sources (`KnownSources`, `LookupSource`); test `TestKnownSources` asserts the six git marketplace entries validate as `SourceID`.
*   `internal/source/openai_plugin.go` — `OpenAIPluginAdapter.Ingest` parses a `plugin.json` with `name_for_human` / `name_for_model` / `skills` / `mcp_servers` and decomposes it into `ComponentSkill` and `ComponentMCPProvider` (test `TestOpenAIPluginAdapter`).

**Honest gaps.** No adapter exists for **Agent Plugins 1.0.0**, Cursor, GitHub Copilot, Gemini CLI, or OpenCode plugins. The Claude and Grok adapters collapse each plugin into a single `ComponentSkill` and do not decompose real children (mcp, hooks, agents, lsp). The `openai_plugin.go` shape (`name_for_human` / `name_for_model`) is the **legacy OpenAI plugin manifest**; per `ARCH/26 §5` the legacy `openai/skills` surface is deprecated and must not be treated as the forward path. No plugin installation/materialisation exists in the source layer (correctly so — source adapters ingest metadata only).

### 1.4 `agent` — installable agent runtime and/or agent definition

**(a) Definition & format.** Two related objects:
*   **ACP agent:** an installable agent that speaks the Agent Client Protocol (JSON-RPC over stdio). Registry entries define a `distribution` of `npx`, `uvx`, and/or per-platform `binary` archives. Authoritative: <https://agentclientprotocol.com/rfds/acp-agent-registry>.
*   **Agent definition / subagent:** a delegatable specialist, usually Markdown + YAML frontmatter (`name`, `description`, `tools`, `model`) in `.claude/agents/` or an equivalent host directory.

**(b) Where published/indexed.** The ACP Registry — a CI-validated `registry.json` at <https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json> (41 agents observed on 2026-10-01, including OpenAI, Gemini CLI, GitHub Copilot, OpenCode, Cursor, Goose, and Grok Build entries). Agent definitions are published in Git repos and host plugin bundles.

**(c) How hosts consume it.** For an ACP agent, the client resolves the `distribution` for the current platform target (`darwin-aarch64`, `darwin-x86_64`, `linux-aarch64`, `linux-x86_64`, `windows-aarch64`, `windows-x86_64`), downloads a binary or runs `npx`/`uvx`, then speaks ACP over stdio. Agent definitions are loaded by the host from its agents directory.

**(d) Repository status — Partial / In progress.** A parallel workstream landed this during the investigation:
*   `internal/agent/types.go` — `Target` constants (including `windows-aarch64` and `windows-x86_64`), `Distribution` (`Npx`/`Uvx`/`Binary map[Target]BinaryTarget`), `Agent`, `Registry`, `LaunchSpec`.
*   `internal/agent/acp.go` — `ParseRegistry`, `MatchTarget(goos, goarch)`, `HostTarget`, `Supports`, and `Resolve` (resolution order npx → uvx → platform binary). Covered by `internal/agent/agent_test.go` (passes).
*   `internal/source/acp_registry.go` — `ACPAgentAdapter.Ingest` normalizes the registry into `KindAgent` listings with `ComponentAgent`, `ArtifactRef`s (npm/pypi/archive+SHA-256) and runtime requirements. Covered by `internal/source/acp_registry_test.go` using `fixtures/source/acp/registry.json` (41 agents; passes).

**Per the explicit instruction for this document, the `agent` type is marked Partial / In progress, not Working.** The metadata ingestion and launch-spec *resolution* are implemented and tested; the **download → verify → extract → materialise → supervise** path and any host runtime acceptance are not yet wired. There is no adapter for host agent *definitions* (subagents). No catalog rows exist for `agent` today.

### 1.5 `rule` — persistent instruction/policy

**(a) Definition & format.** A rule is persistent guidance attached to an agent's context. Cursor project rules are `.cursor/rules/**/*.mdc` with an `.mdc` metadata header (formats: always / auto-attached / agent-requested / manual); newer Cursor versions also document folder-based `RULE.md`. Authoritative: <https://cursor.com/docs/rules>. Adjacent formats include `AGENTS.md`, GitHub Copilot's `com.github.copilot/rules/`, and editor extension rule folders.

**(b) Where published/indexed.** Cursor Marketplace and `cursor.directory` (<https://cursor.directory>), community awesome-lists, and Git repos committed alongside code.

**(c) How hosts consume it.** The host reads rule files at session start or at runtime and attaches them to context (glob- or description-triggered). No protocol is involved; it is a filesystem + prompt-injection mechanism.

**(d) Repository status — Not implemented.** Only the domain constants `domain.KindRule` and `domain.ComponentRule` exist in `internal/domain/models.go` (added by the parallel `models.go` change). There is no `internal/source/*rule*.go`, no `.mdc`/`RULE.md` parser, no host rule-path detection in `internal/host/*.go`, and no catalog rows.

**Honest gaps.** No ingestion adapter, no format parser, no host-discovery path, no install/materialisation. The type is a reserved catalog slot, not a capability.

### 1.6 `hook` — lifecycle event handler

**(a) Definition & format.** A hook is a program run on an agent/tool lifecycle event. Claude Code hooks are configured under a top-level `"hooks"` key in `settings.json` (or a plugin's `hooks/hooks.json`), keyed by event (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Notification`, …) with matcher groups and handlers of type `command`, `http`, or `mcp_tool`. Authoritative: <https://code.claude.com/docs/en/hooks>. GitHub Copilot and VS Code agent plugins also ship `hooks.json` (<https://code.visualstudio.com/docs/agent-customization/agent-plugins>).

**(b) Where published/indexed.** Inside plugin bundles and host repos; there is no standalone hook registry.

**(c) How hosts consume it.** The host executes the handler at the matching lifecycle event and consumes its JSON output (decision control, additional context, output rewriting).

**(d) Repository status — Not implemented.** `domain.ComponentHook` and `domain.KindHook` exist in `internal/domain/models.go`. `internal/policy/engine.go` defines `EffectHookExecute = "hook.execute"` and classifies it in the effect taxonomy, so a hook effect can be reasoned about by the policy engine — but there is no hooks-manifest adapter, parser, or install/execution mediation path.

**Honest gaps.** No `hooks.json`/`settings.json` adapter, no hook execution supervisor, no catalog rows. The effect classification exists ahead of the capability, which is the correct direction but is not an implementation.

### 1.7 `tool` — native (non-MCP) tool extension / MCP tool

**(a) Definition & format.** A tool is an invocable function. In MCP it is discovered via `tools/list` and invoked via `tools/call`, each with `name`, `description`, and `inputSchema` (JSON Schema). Authoritative: <https://modelcontextprotocol.io/specification/2026-07-28> (Tools chapter). Non-MCP tools are host-specific (for example an OpenCode plugin registering `tool({...})` through `@opencode-ai/plugin`, or a VS Code command contributed by an extension).

**(b) Where published/indexed.** MCP tools are published transitively by the servers that expose them; non-MCP tools are published inside host plugins/extensions. There is no standalone tool registry.

**(c) How hosts consume it.** At runtime: MCP `tools/list` → `tools/call`; or a host plugin API that registers the tool in-process.

**(d) Repository status — Partial (runtime) / Not implemented (as a listing type).**
*   `internal/domain/models.go` defines `KindTool` / `ComponentTool`, and `CapabilityRecord` carries `InputSchemaJSON` + `SchemaFingerprint` (SHA-256 of canonicalized schema; see [ARCH/10 §3.2](10-DOMAIN-MODEL.md)).
*   `internal/bridge/shim.go` exposes `search_capabilities`, `describe_capability`, and `invoke_capability`; `internal/mcpclient` discovers tools; `internal/policy` gates invocation; the shim exposes 12 tools total.
*   `internal/agent/acp.go` names a `"binary"` strategy but that is unrelated to the `tool` type.

**Honest gaps.** There is **no source adapter for `tool` as a catalog listing** and no catalog rows. Non-MCP tool registration (OpenCode/Copilot/VS Code tool contributions) is not ingested. Schema fingerprinting exists in the domain model but is not surfaced by any `source` adapter.

### 1.8 `lsp` — language server

**(a) Definition & format.** A language server is a process that speaks the Language Server Protocol (JSON-RPC 2.0 over stdio/pipe/socket) to provide diagnostics, completion, hover, and related intelligence. Authoritative: <https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/> (latest major is 3.18; 3.17 is the commonly implemented revision). Server definitions travel inside host packages: Grok plugins can bundle LSPs (<https://docs.x.ai/build/features/skills-plugins-marketplaces>), Zed extensions declare language servers in `extension.toml` (<https://zed.dev/docs/extensions/developing-extensions>), and VS Code extensions contribute them via `package.json`.

**(b) Where published/indexed.** Zed extension registry, VS Code Marketplace / Open VSX, plugin bundles, and language-specific package registries (npm, PyPI, Cargo).

**(c) How hosts consume it.** The host resolves a server binary/command, launches it, and speaks LSP over stdio. Zed compiles a WASM adapter that returns a `language_server_command`. No catalog protocol is involved.

**(d) Repository status — Not implemented.** Only `domain.KindLSP` and `domain.ComponentLSP` exist in `internal/domain/models.go`. There is no LSP adapter, no `extension.toml`/`package.json` language-contribution parser, and no LSP launch/materialisation path. No catalog rows.

**Honest gaps.** No ingestion, no host detection, no runtime. Reserved type only.

---

## 2. Package Sources

Source adapters live in `internal/source` and are statically compiled. Per [ARCH/03](03-CATALOG-SOURCES.md) and [ARCH/17](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) they perform **metadata ingestion only** and never download bytes or execute code. **Interface applicability:** `internal/source/adapter.go` declares `Adapter` as `SourceID()` + `Ingest(ctx, snapshotID)`. Only three adapters satisfy it verbatim (`MCPRegistryAdapter`, `AgentSkillsAdapter`, `ACPAgentAdapter`). The five marketplace adapters (`Claude`, `Codex`, `Cursor`, `Grok`, `OpenAIPlugin`) accept an **extra `rawManifest []byte`** because the build-pipeline caller supplies the already-fetched manifest; they implement the same behaviour but not the two-method interface. See [ARCH/17 §2.1](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md).

The roster below matches the required source list. **Known to exist today in `internal/source`: `mcp_registry.go`, `skills.go` (Agent Skills), `claude_marketplace.go`, `openai_plugin.go`, `codex_marketplace.go`, `cursor_marketplace.go`, `grok_marketplace.go`, `sources.go` (checked-in source registry) — plus `acp_registry.go`, which landed concurrently (see §1.4). Everything else is Not implemented *as a compiled adapter*. Two directory sources (rows 21–22) bypass the adapter interface entirely and are walked by the Python dataset producer (`scripts/build_full_catalog.py`) through the snapshot layer instead; both are registered in `sources.go` as `feed:` ids with no manifest paths.

| # | Source | Mechanism | v1 status |
|---|---|---|---|
| 1 | **Official MCP Registry (API)** | REST API / `server.json`; namespace-authenticated metaregistry | **Working — metadata** (`mcp_registry.go`, `MCPRegistryAdapter`) |
| 2 | **GitHub repos + Releases** | REST API, release assets, raw tree; `git ls-remote` for pins | **Not implemented** |
| 3 | **npm** | Registry API (<https://registry.npmjs.org>), dist-tags | **Not implemented** as first-class discovery; reachable transitively (`registryType: npm` in MCP Registry, ACP `npx` artifacts) |
| 4 | **PyPI** | JSON API (<https://pypi.org/pypi/&lt;name&gt;/json>) / PEP 691 | **Not implemented** as first-class discovery; transitive (`registryType: pypi`, ACP `uvx`) |
| 5 | **Docker / OCI** | Registry catalog + manifests (OCI Distribution Spec) | **Not implemented** as first-class discovery; transitive (`registryType: oci`) |
| 6 | **Agent Skills (agentskills.io / skills.sh)** | Directory + Git `SKILL.md` | **Working — metadata** (`skills.go`, `AgentSkillsAdapter`). Note: the skills.sh content in the published dataset arrives via the producer walk (row 21), not this adapter |
| 7 | **Claude marketplace (incl. arbitrary Git marketplaces)** | `.claude-plugin/marketplace.json` in any Git repo | **Working — metadata** for the Claude manifest shape (`claude_marketplace.go`; `command` sources rejected). Registered: `git:anthropics-skills`, `git:claude-plugins-official`, `git:knowledge-work-plugins`. A *general* arbitrary-Git source is **Not implemented** |
| 8 | **OpenAI Codex plugins** | `plugin.json` portable manifest; Codex marketplaces at `.agents/plugins/marketplace.json` | **Working — metadata** for both shapes (`openai_plugin.go` parses `name_for_human`/`name_for_model`; `codex_marketplace.go` parses the marketplace shape with declared auth policy). Registered: `git:openai-plugins` (both `marketplace.json` and `api_marketplace.json`). The legacy `openai/skills` catalog is **deprecated** and must not be treated as the forward path; Agent Plugins 1.0.0 is not implemented |
| 9 | **Cursor marketplace + cursor.directory** | `.cursor-plugin/marketplace.json` Git manifest | **Working — metadata** for the manifest shape (`cursor_marketplace.go`). Registered: `git:cursor-plugins`. cursor.directory feed is **Not implemented** |
| 10 | **GitHub Copilot plugins** | `.github/plugin.json` + `marketplace.json` | **Not implemented** |
| 11 | **Gemini CLI extensions** | `gemini-extension.json` + extension gallery | **Not implemented** |
| 12 | **OpenCode (npm plugins)** | npm package + `opencode.json` plugin array | **Not implemented** |
| 13 | **Cline MCP marketplace + skills** | Marketplace API + skills feed | **Not implemented** |
| 14 | **Roo** | Marketplace / config feed (`mcp_settings.json`, `custom_modes.yaml`) | **Not implemented** |
| 15 | **Zed extensions** | Extension registry + `extension.toml` | **Not implemented** |
| 16 | **ACP registry** | `registry.json` of ACP agents (`npx`/`uvx`/`binary` distributions) | **Partial / In progress** (`internal/source/acp_registry.go` + `internal/agent/`, landed concurrently; metadata + launch-spec resolution tested, download/materialise/launch not wired) |
| 17 | **VS Code Marketplace** | Marketplace API (`package.json` contributions, `.vsix`) | **Not implemented** |
| 18 | **Open VSX** | Registry API (<https://open-vsx.org/api>) | **Not implemented** |
| 19 | **Local filesystem** | Directory scan / manifest discovery | **Not implemented** as a *discovery* source. The runtime install path supports `ArtifactLocalDir` (`domain.ArtifactLocalDir`, `internal/install`), but nothing scans the filesystem to discover packages |
| 20 | **Arbitrary Git marketplace** | User-supplied repo URL + a supported manifest | **Not implemented** generally; the Claude, Codex, Cursor, and Grok adapters parse only their specific manifests |
| 21 | **skills.sh (directory walk)** | Sitemap (`sitemap-skills-*.xml`) + public skill listing pages; page `SoftwareApplication` JSON-LD description (description meta fallback), each page snapshot-recorded. Producer refuses the paths currently disallowed by `robots.txt` (`/api/`, `/internal/`, `/debug-security/`, `/search`). | **Working — metadata (producer path only; no Go adapter)** — `feed:skills-sh` in `sources.go`; rows `discovery_only`; a page without a usable published description is skipped and counted, never summarized by the build |
| 22 | **MCPServers.org (directory walk)** | Sitemap (en paths) + CDX-indexed **Wayback Machine** page replays; producer-walked with per-fetch snapshots. The site is never fetched directly (robots) | **Working — metadata (producer path only; no Go adapter)** — `feed:mcpservers-org` in `sources.go`; rows `discovery_only`; uncaptured pages and captures lacking title+description are skipped and counted |
| + | **Agent Plugins 1.0.0** (`plugin.json` + `skills/` + `mcp.json`) | Fixed-directory portable bundle | **Not implemented** (included because `plugin` depends on it; maps to `type: skill` + `type: mcp` per [ARCH/26 §5](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)) |

**Status legend:** `Working — metadata` = a compiled adapter exists and is covered by a passing test. `Partial` = some layer exists. `Not implemented` = no compiled adapter; the row is a target.

Normative constraints carried forward from `ARCH/26 §5`: adapters stay statically compiled; an adapter must not download bytes or execute scripts; ingesting Agent Plugins must not imply safe execution.

---

## 3. Summary Matrix

| Type | Format | Primary sources | Host consumption | Repo status | Milestone |
|---|---|---|---|---|---|
| **mcp** | `server.json` + MCP JSON-RPC (stdio / Streamable HTTP) | Official MCP Registry API; community marketplaces | `mcpServers` / `mcp` JSON entry or `[mcp_servers.*]` TOML; runtime MCP protocol | **Working** (`mcp_registry.go`, `bridge/shim.go`, `host/*.go`, `mcpclient`) | M2 |
| **skill** | Directory with `SKILL.md` (YAML frontmatter + body; `scripts/`, `references/`, `assets/`) | agentskills.io, skills.sh, Git repos, host skill folders | Copied to host skill dir; progressive disclosure at runtime | **Partial** (`skills.go`, `internal/skills`, shim `load_skill`) | M2 |
| **plugin** | Agent Plugins 1.0.0 `plugin.json`; vendor `.claude-plugin/`, `.grok-plugin/`, `.github/plugin.json`, `gemini-extension.json`, npm TS | Claude/Codex/Cursor/Grok marketplaces, Copilot, Gemini, OpenCode, npm | Host reads marketplace manifest, installs bundle, loads children | **Partial** (`claude_marketplace.go`, `codex_marketplace.go`, `cursor_marketplace.go`, `grok_marketplace.go`, `openai_plugin.go`) | M2 |
| **agent** | ACP `registry.json` + `distribution` (npx/uvx/binary per target); subagent Markdown+YAML | ACP Registry (`cdn.agentclientprotocol.com`), Git repos | Spawn ACP process over stdio; host loads agent definitions | **Partial / In progress** (`agent/acp.go`, `source/acp_registry.go`) | M2 |
| **rule** | `.cursor/rules/*.mdc` (+ `RULE.md`), `AGENTS.md`, host rules folders | Cursor Marketplace, cursor.directory, Git repos | Attached to context at session start or on trigger; no protocol | **Not implemented** (constants only) | M2 (type) / v2 (adapter) |
| **hook** | `hooks/hooks.json`; `settings.json` `"hooks"` keyed by event | Plugin bundles; no standalone registry | Host executes handler on lifecycle event, consumes JSON output | **Not implemented** (constants only; `EffectHookExecute` classified) | M2 (type) / v2 (adapter) |
| **tool** | MCP `tools/list` `inputSchema`; host plugin tool APIs | MCP servers (transitive); host plugins | Runtime `tools/list` + `tools/call`; or in-process plugin registration | **Partial** runtime (`CapabilityRecord`, shim, `mcpclient`, `policy`); **Not implemented** as a source type | M2 (type) / M2+ (adapter) |
| **lsp** | LSP JSON-RPC 2.0 (stdio/pipe/socket) | Zed extensions, VS Code Marketplace / Open VSX, plugin bundles, npm/PyPI/Cargo | Host resolves command/launches server and speaks LSP over stdio | **Not implemented** (constants only) | M2 (type) / v2 (adapter) |

Milestone labels reuse the vocabulary in [ARCH/26 §11](26-ECOSYSTEM-IA-PACKAGE-MODEL.md): `M2` = data model + source adapters; `v2` = deferred capability-type adapters pending a convergent portable format. **Provenance of the labels:** ARCH/26 §11.2 explicitly assigns the eight v1 `type` values to M2 and enumerates GitHub/npm/PyPI/OCI/Cursor/Copilot/Gemini/OpenCode/Cline/Roo/Zed/ACP/VS Code/Open VSX/local/arbitrary-Git/Agent-Plugins adapters to M2. It does **not** name adapters for `rule`, `hook`, or `lsp`; those `v2` adapter labels (and the `tool` adapter note) are this document's inference, not a contract, and MUST NOT be presented as scheduled work.

---

## 4. Verification Status

### 4.1 Commands used and observed results (2026-10-02, repo `/home/sarvesh/business_Dev/liteSPM`)

| Command | Observed result |
|---|---|
| `ls internal/source` | `acp_registry.go`, `acp_registry_test.go`, `adapter.go`, `claude_marketplace.go`, `codex_marketplace.go`, `cursor_marketplace.go`, `flex.go`, `grok_marketplace.go`, `mcp_registry.go`, `openai_plugin.go`, `skills.go`, `source_marketplace_test.go`, `source_test.go`, `sources.go` |
| `go test ./internal/source/... ./internal/agent/... ./internal/domain/...` | `ok` for all three packages (exit 0) |
| `go vet ./internal/...` | no output (exit 0) |
| `go build ./...` | no output (exit 0) |
| `go version` | `go1.26.0 linux/amd64` |
| `git status --short` | `M ARCH/00-INDEX.md`, `M ARCH/26-ECOSYSTEM-IA-PACKAGE-MODEL.md` (docs upgrade in progress; ACP adapters committed) |
| `python3` census of `fixtures/source/acp/registry.json` | 41 agents (`version`/`agents`/`extensions` keys) |

The ACP adapter test reads `../../fixtures/source/acp/registry.json`. At first observation that fixture and the ACP adapters were untracked; they were committed as `09ba4cb` shortly afterwards, and `go test ./internal/source/... ./internal/agent/...` re-run against the committed tree still returned `ok` (exit 0).

### 4.2 What these checks do and do not prove

*   The passing tests prove the **metadata parsers and launch-spec resolution behave as asserted on fixed fixtures**. They do not prove any host can install, launch, or mediate a real capability.
*   **No runtime acceptance test exists for the types that have no v1 data** — `agent` (as a full install/launch path), `rule`, `hook`, `tool` (as a source type), and `lsp`. None has catalog rows, and none has an end-to-end host execution test. Per [ARCH/08 §2](08-DELIVERY.md) and the readiness-gating rule, these remain unverified even where domain constants or partial code exist.
*   `mcp`, `skill`, and the three vendor `plugin` adapters have unit-tested metadata ingestion but **no Windows/host runtime acceptance record**.

### 4.3 ACP note (per instruction)

The ACP registry at <https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json> defines each **agent** with a `distribution` block whose values are `npx` and/or `uvx` package distributions and/or per-platform `binary` targets for `darwin-aarch64`, `darwin-x86_64`, `linux-aarch64`, `linux-x86_64`, `windows-aarch64`, and `windows-x86_64`. A parallel workstream is implementing the ACP adapter; the source adapter (`internal/source/acp_registry.go`) and host-target launch resolution (`internal/agent/acp.go`) are present and tested, but the download/materialise/supervise path is not complete. The `agent` type is therefore recorded as **Partial / In progress**, **not Working**.

---

## 5. Honest gaps summary

1.  **Five of eight types are not implemented as capabilities:** `rule`, `hook`, `lsp`, and `tool` (as a source type) have no adapter; `agent` has ingestion + launch resolution but no full install/launch path. Their domain constants are groundwork, not function.
2.  **Fifteen of the twenty listed sources are Not implemented**, including every editor/marketplace source except the MCP registry, Agent Skills, Claude, and Grok (plus the in-progress ACP registry and the partial legacy OpenAI adapter).
3.  **The three vendor plugin adapters under-decompose:** Claude and Grok emit a single `ComponentSkill`; only `openai_plugin.go` decomposes skills and MCP servers, and it targets the deprecated legacy manifest shape rather than Agent Plugins 1.0.0.
4.  **The skill frontmatter parser is not a YAML parser** and silently drops unsupported fields.
5.  **Source adapters consume caller-supplied bytes** for MCP, Skills, and ACP; the fetch layer is external to the adapter.
6.  **No runtime acceptance record exists** for any capability type on any host; unit tests on fixed fixtures are the ceiling of current evidence.
