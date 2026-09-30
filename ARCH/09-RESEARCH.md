# Research ledger

Research date: 2026-09-30. Upstream behaviors can change; recheck linked primary sources when implementing an adapter. The references below support design patterns and source-format claims. They do not authorize copying source code or imply that every feature is accessible through a public API.

## Upstream registries and formats

### Model Context Protocol Registry

- [Official Registry](https://registry.modelcontextprotocol.io/) — public server discovery.
- [Registry API reference](https://registry.modelcontextprotocol.io/docs) — documented API endpoints and schemas.
- [Registry introduction](https://blog.modelcontextprotocol.io/posts/2025-09-08-mcp-registry-preview/) — describes the registry as a public server catalog/API intended as a primary source that sub-registries can build on.
- Use: source adapter for MCP server metadata; never infer hosted execution or credential handling from a listing.

### Agent Skills

- [Agent Skills specification](https://agentskills.io/specification) — portable `SKILL.md` format and directory structure.
- [skills.sh CLI](https://www.skills.sh/docs/cli) — `npx skills add` installation path.
- [skills.sh API](https://www.skills.sh/docs/api) — documented search/detail API, stable IDs, install URLs, hashes, and duplicate marker.
- Use: documented source adapter and local Agent Skills installer; popularity counts remain ranking metadata, not verification.

### Claude plugin marketplaces

- [Claude Code marketplace docs](https://code.claude.com/docs/en/plugin-marketplaces) — Git/URL/local marketplace sources and manifest workflow.
- [Anthropic plugin marketplace repo](https://github.com/anthropics/claude-plugins-official) — official Git-hosted plugin catalog.
- [Claude plugin usage help](https://support.claude.com/en/articles/13837440-use-plugins-in-claude) — curated sources and Git repository addition paths.
- Use: parse public Git marketplace manifests or a source supplied by a user. No global discovery API is assumed. Private sources stay subject to the user's own Git access.

### Codex / portable Agent Plugins

- [Package your plugin](https://developers.openai.com/plugins/build/plugins) — portable `plugin.json`, skills, MCP config, optional assets/hooks, and repository/local marketplace formats.
- [Plugin architecture](https://developers.openai.com/plugins/concepts/plugins) — plugins combine skills, MCP servers, and optional UI; ChatGPT and Codex share a public directory, while local/repository marketplaces are separate sources.
- [MCP plugins API guide](https://developers.openai.com/api/docs/guides/agents-api/tools/plugins) — example plugin/MCP configuration and local package use.
- Use: parse portable format and repo marketplaces. Do not assume a public third-party API for the universal directory; add an adapter only through documented access.

### Grok plugin marketplaces

- [Grok Build plugin guide](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/09-plugins.md) — Git/local marketplaces, `.grok-plugin/marketplace.json`, plugin component index, trust model, and supported source formats.
- [Official Grok plugin marketplace repository](https://github.com/xai-org/plugin-marketplace) — index format and pinned remote commit SHAs.
- Use: Git manifest adapter; preserve SHA pinning. Test Claude-format compatibility per feature rather than claiming whole-product parity.

## Comparable product flows (patterns only)

### Agent MCP client support and setup surfaces

- [Codex MCP setup](https://developers.openai.com/learn/docs-mcp) — Codex supports adding MCP servers by CLI or config file; the same configuration is used by CLI and IDE extension.
- [Claude Code MCP](https://code.claude.com/docs/en/mcp) — supports adding and scoping MCP servers through its CLI/configuration.
- [Cursor MCP](https://docs.cursor.com/context/model-context-protocol) — supports local and remote MCP, configuration files, and an extension API.
- [OpenCode MCP servers](https://dev.opencode.ai/docs/mcp-servers/) — supports configured local and remote servers and exposes connected tools to the model.
- [Grok Build MCP guide](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/07-mcp-servers.md) — supports local stdio and remote HTTP MCP servers.
- [Gemini CLI MCP setup](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/tutorials/mcp-setup.md) — supports MCP servers through user/project settings.
- Use: evidence that a one-time local MCP registration is a plausible integration surface for these hosts. It does **not** establish a universal install API or identical approval/elicitation behavior. Each adapter and confirmation path must be verified against the supported version.

- [MCP Market Hub](https://mcpmarket.com/hub) — versioned skills, MCP-backed toolkits, client plugin sync, team sharing. Its [plugin install docs](https://docs.mcpmarket.com/docs/plugins/installing-a-plugin) say install commands pipe a script to shell but can be inspected. Its [custom MCP deployment docs](https://docs.mcpmarket.com/docs/mcp-servers/deploying-a-custom-mcp-server) describe hosted deployments. Adapt toolkits/versioning/inspection; LitePSM keeps installation/runtime local.
- [ahel catalog](https://ahel.ai/catalog) and [connector docs](https://ahel.ai/install?client=openai) — broad multi-kind catalog, one MCP endpoint, server-side credential attachment. Use as evidence the unified discovery/gateway concept exists; retain LitePSM's different credential/execution boundary.
- [Glama](https://glama.ai/) — MCP directory, tool inspection, hosting, and MCP gateway. Adapt deep per-server discovery; don't require its gateway.
- [Smithery CLI package](https://www.npmjs.com/package/smithery?activeTab=readme) — CLI verbs for search/add/list/inspect/call and a separate skill install path. Adapt discover/manage command ergonomics; local LitePSM owns installation.

## Hosting references

- [Cloudflare Pages Git integration](https://developers.cloudflare.com/pages/get-started/git-integration/) — supports public and private GitHub/GitLab source repositories.
- [Cloudflare Pages limits](https://developers.cloudflare.com/pages/platform/limits/) — current static asset/build limits.
- [Cloudflare Pages Functions pricing](https://developers.cloudflare.com/pages/functions/pricing/) and [Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/) — dynamic endpoints consume Workers quotas; defer them until needed.
- [GitHub Pages](https://docs.github.com/en/pages/getting-started-with-github-pages/what-is-github-pages) and [visibility behavior](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/setting-repository-visibility) — private-source publication is plan-dependent; changing a GitHub Free repo to private can unpublish Pages. Cloudflare Pages is the proposed host for private-source/public-output.

## Research rules

- Prefer documented APIs and primary docs.
- If only a Git marketplace manifest exists, ingest that manifest rather than scraping an application UI.
- Keep “marketplace can list it,” “client can parse it,” “client can install it,” and “LitePSM tested it” as separate facts.
- Recheck docs and source revisions at adapter implementation time; this ledger is a research snapshot, not an eternal guarantee.
