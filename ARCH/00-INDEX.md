# Architecture index

## Status

All documents in this initial architecture are **proposed**. No service, client, installer, catalog, integration, or security guarantee is implemented by these documents.

## Document owners

| Document | Owns |
|---|---|
| [01 — Product contract](01-PRODUCT.md) | Purpose, users, goals, non-goals, and product language |
| [02 — HLD](02-HLD.md) | System boundaries, deployment, and major components |
| [03 — Catalog and source federation](03-CATALOG-SOURCES.md) | Metadata model, upstream sources, adapters, compatibility, and ingestion |
| [04 — Client and installation](04-CLIENT-INSTALL.md) | CLI, web, SDK, discovery MCP, adapters, install/update/remove flows |
| [05 — Security and credentials](05-SECURITY.md) | Credential locality, install trust, provenance, and runtime boundaries |
| [06 — API and package contracts](06-API-CONTRACTS.md) | Static catalog API, optional service API, SDK and discovery MCP contracts |
| [07 — Decisions](07-DECISIONS.md) | Settled design choices and open decisions |
| [08 — Delivery plan](08-DELIVERY.md) | Staged build order and acceptance evidence |
| [09 — Research ledger](09-RESEARCH.md) | Pinned source links and the facts each one supports |

## Terms

- **Listing:** normalized discovery metadata that points to an upstream source.
- **Artifact:** the actual skill/plugin package or MCP distribution obtained from its publisher/source.
- **Install:** verified local placement/configuration performed by the user's LitePSM client.
- **LitePSM Bridge:** local MCP server/runtime, registered once with a supported agent, that exposes installed and enabled LitePSM-managed capabilities under local policy.
- **Host adapter:** a narrow integration that registers or removes the one LitePSM Bridge entry in an agent's documented configuration format.
- **Extension adapter:** an integration that installs a component into an agent-native location when the component cannot be used through the Bridge.
- **Toolkit:** a named selection of skills and MCP tools; initially a LitePSM client-side composition, not a hosted proxy endpoint.
- **Connector:** a user-facing service integration listing. Initially it resolves to an MCP server or plugin; it does not imply a native LitePSM SaaS connector runtime.
- **Registry source:** an upstream catalog or repository from which LitePSM reads listings.

Ordinary skills and MCP providers are managed in LitePSM's local store after host setup. Host-specific configuration per extension is reserved for native-only components or hosts without a verified Bridge adapter.

## Architecture authority

The user-approved decisions in `07-DECISIONS.md` and security boundary in `05-SECURITY.md` govern implementation. If a source ecosystem does not expose a documented feed/API, LitePSM must not assume one exists or scrape a private directory. Mark that adapter unavailable or require a user-supplied marketplace source.
