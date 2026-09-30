# Product contract

## Product

**LitePSM Market** is the discovery surface for agent plugins, skills, and MCP servers. **LitePSM Client** is the local manager that resolves, verifies, installs, and updates those items for a user's chosen agent. LitePSM is designed for HorizonCode, AgentCowork, and other compatible agents; it is not owned by a single host product.

## Problem

Capabilities are spread across MCP registries, skill repositories, Git-hosted plugin marketplaces, and product-specific plugin directories. Their installation formats differ. Users need a single place to search and inspect them, plus a local installation path that does not require sending credentials or tool traffic through a marketplace service.

## Product goals

1. Search a federated catalog without requiring an account for public listings.
2. Preserve the upstream source, package format, version, and provenance of each listing.
3. Connect a supported agent to LitePSM once, then manage supported skills and MCP providers centrally through a local Bridge.
4. Keep credentials and extension execution on the user's machine or with the selected upstream provider, outside LitePSM's hosted service.
5. Let agents query LitePSM through a read-only API or discovery MCP endpoint.
6. Add registries by implementing source adapters, not by manually reimplementing each integration.
7. Make compatibility and verification claims evidence-based and specific to a client/version.

## Non-goals for the first release

- Hosting MCP calls, secrets, or user OAuth sessions in LitePSM's cloud. A user-controlled local Bridge may mediate local/remote provider calls on the user's device.
- Running arbitrary plugin hooks, MCP servers, or scripts in LitePSM's cloud.
- Building a native API connector for every SaaS service.
- Mirroring every upstream artifact or claiming every catalog record is safe, tested, or compatible.
- Automatically installing or enabling capabilities because an agent requested them.
- Guaranteeing seamless installation into clients that have no public local install/configuration interface.

## Product identity

- Product/repository: **LitePSM**.
- Public website: **LitePSM Market**.
- Expansion: **Plugins, Skills, MCP**. Connectors are a discovery category and may point to those package kinds; a future native connector system needs an explicit architecture decision.

## Main user paths

### Person

Browse the market → connect the agent to the LitePSM Bridge once → inspect source, compatibility, requested access, and version → install into the LitePSM-managed store → approve provider start and requested access → authenticate directly with the provider if needed. Native-only plugin components may require a host-specific install.

### Agent

Search/inspect via the local Bridge or hosted Discovery API → present a concrete install proposal → receive user approval → ask the local Bridge/client to verify and install into the LitePSM-managed store. If the host cannot provide reliable approval, show the CLI command instead.

### Publisher

Publish a supported manifest or an upstream marketplace entry → validate metadata and package references → expose a source link and version/digest → optionally request review for a LitePSM compatibility badge.

## Product language

The normal user should see “Connect,” “Install,” “Update,” “Remove,” and “Installed,” with useful service/package names. Terms such as transport, source adapter, registry normalization, and credential broker belong in developer documentation or detail views, not the primary marketplace flow.
