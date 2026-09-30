# Architecture decisions

## Accepted for this proposal

### D-001 — Neutral shared product, local execution

LitePSM is usable by HorizonCode, AgentCowork, and future agents. The hosted service provides discovery/metadata. A client-side manager performs installs and runs an optional local Bridge. LitePSM does not host extensions, credentials, or tool execution in the cloud.

### D-002 — Product and market names

Use **LitePSM** for the product/client/service and **LitePSM Market** for the public catalog. `PSM` expands to Plugins, Skills, and MCP. Directory slug is `litePSM` per request; confirm domain/npm availability before publication.

### D-003 — Client-side downstream credentials

Downstream service credentials live in the local operating-system secret store and are never uploaded to LitePSM. The downstream provider may receive them when the user invokes its service. A provider-hosted credential gateway is an explicitly distinct opt-in integration, not LitePSM's default mode.

### D-004 — Static catalog first

Use a private GitHub source repository, CI-generated public JSON, and Cloudflare Pages as the initial public host. No backend/database is required for public listings. Add dynamic services only to satisfy a defined feature need.

### D-005 — Federate existing sources; do not hand-build every listing

Use official/documented APIs and public Git manifests, preserve attribution and upstream IDs, and keep source adapters replaceable. Do not scrape private product directories or invent registry APIs. A target of 500–1,000 means federated searchable metadata, not 1,000 bespoke integrations or verified packages.

### D-006 — Formats remain distinct

Normalize metadata for discovery, but retain original package format and source semantics. Client adapters install compatible parts; unsupported components are reported rather than silently translated or discarded.

### D-007 — Installation requires a local plan and approval

Hosted discovery is read-only. Install/update/remove plans show affected files/config and permissions. The local LitePSM client owns effects. Download, LitePSM-managed placement, one-time Bridge configuration, provider start, and tool call remain distinct steps.

### D-008 — No automatic update by default

Pin installed version/digest. Show an update diff and permission delta and require user approval by default. Future policy-based auto-update requires its own decision.

### D-009 — Cross-agent access is integration-based, not automatic

LitePSM will expose stable MCP/API/SDK surfaces and client adapters so future agents can integrate. It will not claim direct access in a host that lacks a compatible protocol or install path. Each agent surface must be tested and documented separately.

### D-010 — One-time host bridge, centrally managed extensions

For supported MCP-capable agents, LitePSM registers one local LitePSM Bridge per agent. Skills and supported MCP providers are installed into a LitePSM-managed local store and made available through that Bridge, avoiding one agent-config edit per extension. Host-native plugin features remain adapter-specific and may require native installation. The Bridge is local, user-controlled, and subject to its own capability allowlist, action policy, and approval requirements; this decision does not authorize unrestricted generic tool execution.

## Open decisions before implementation

- Public npm package name and release identity; `@litepsm/cli` is only illustrative.
- First client adapter matrix and supported version ranges.
- Bridge execution details: exact capability discovery and approval UX must be proven for each host. If a host cannot provide reliable confirmation for installs or risky actions, use the local CLI/native host path rather than silent in-agent mutation.
- Whether the Discovery MCP is available from initial launch or after static catalog/API validation.
- Catalog contribution and moderation policy; initial plan is reviewed catalog records/automated source ingestion, not public direct writes.
- Signing format/key custody for LitePSM-generated metadata and artifacts; upstream digests are mandatory where available, but do not imply publisher identity.
- Whether connectors remain an informational category in v1 or are omitted from the UI until MCP-backed entries can be clearly distinguished.
- Compatibility badge evidence requirements and who can publish “tested” reports.
- Product/domain/npm name availability and trademark review before public launch.
