# LitePSM Market

LitePSM is a provider-neutral catalog and local manager for agent plugins, MCP servers, and skills. Connect a supported agent to the local LitePSM Bridge once; then install and manage compatible items through LitePSM instead of repeating per-extension configuration in each agent.

**Credentials and execution stay on the user's machine or with the selected provider. LitePSM's hosted market does not store credentials or proxy tool calls; its optional Bridge runs locally and mediates only user-enabled capabilities under local policy.**

## Product name

- Product and repository: **LitePSM** (`litePSM` as the requested directory name).
- Public-facing catalog: **LitePSM Market**.
- PSM means **Plugins, Skills, and MCP**. Connector listings may resolve to MCP packages in the first release; a separate native connector runtime is out of scope.

## How people use it

- Browse and inspect public listings on LitePSM Market.
- Set up the LitePSM Bridge once for a supported agent with `litepsm setup <client>`.
- Install and manage supported skills and MCP providers in LitePSM's local store; use host-specific installation only for components that require native plugin features.
- Let the agent search and use enabled items through its LitePSM Bridge. Install requests still require local approval; unsupported approval flows fall back to the CLI.
- Use the catalog SDK/API to add LitePSM discovery to another product.

## Hosting direction

The source repository may remain private. The public catalog and website are generated deployable assets. Initially, keep catalog records in the repository and publish static JSON and the site through Cloudflare Pages. No application server or database is required for public discovery. Add a dynamic API only when private catalogs, accounts, or publishing workflows require it.

See [ARCH](ARCH/00-INDEX.md) for the proposed architecture and [TODO.md](TODO.md) for delivery stages. This repository begins as a design proposal; architecture text does not imply implementation.
