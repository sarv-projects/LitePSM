# Client and installation design

## Entry points

Every entry point resolves to the same listing/version and install-plan contract. Supported agents are connected once to the local LitePSM Bridge; installing an ordinary skill or MCP provider afterward does not add another MCP entry to each agent's configuration.

There is no universal protocol that makes a market automatically appear inside every agent. LitePSM provides MCP and API discovery for hosts that support them, plus native adapters where a host has a documented install surface. Other hosts receive a normal public catalog URL and generic package/config export until a supported integration exists.

| Entry point | Role | First-release behavior |
|---|---|---|
| LitePSM Market website | Public discovery | Search, filter, inspect source/version/components/compatibility, choose target client, show install command or package download. No account required for public entries. |
| `litepsm` CLI | Local install authority and agent setup | Search/info/install/list/status/update/remove/doctor plus one-time `setup <client>`; previews affected paths and provider launch details. Can run from a native binary or npm package. |
| Static catalog API | Programmatic discovery | Read-only index and item resolution; no credentials or install mutation. |
| Catalog SDK | Product integration | Typed client for search, item inspection, and install-plan display. It does not carry secrets or run tools. |
| LitePSM Bridge (local MCP) | In-agent access | One host registration exposes catalog search, installed capability discovery, skill loading, and policy-checked invocation through the local runtime. It does not expose arbitrary filesystem writes or bypass approvals. |
| Hosted Discovery MCP | Optional public discovery | Read-only `search`, `inspect`, and `prepare_install` tools. It cannot install locally or invoke third-party tools. |
| Host adapter | One-time host setup | Detects the client and version, registers the single Bridge entry, preserves unrelated settings, or reports manual setup instructions. |
| Extension adapter | Native-only package handling | Used only when a component needs a host-native plugin/skill surface that the Bridge cannot provide. |

No special website account is required to consume public records. Accounts may be introduced later for publisher management/private catalogs; that account credential is separate from downstream service credentials.

## CLI distribution and commands

The CLI is the main cross-client installation entry point. A native binary is the canonical distribution; npm can provide convenient `npx` and global installation for Node users.

Illustrative command contract (package name/commands are proposed, not published):

```sh
litepsm search "postgres" --kind mcp
litepsm info mcp:publisher/server
litepsm setup codex                 # once per supported agent
litepsm plan install skill:publisher/skill
litepsm install skill:publisher/skill
litepsm list
litepsm update skill:publisher/skill --to 2.1.0
litepsm remove skill:publisher/skill
litepsm doctor [--client codex]
```

For npm users:

```sh
npx @litepsm/cli search "postgres" --kind mcp
npx @litepsm/cli setup codex        # once per supported agent
npx @litepsm/cli install skill:publisher/skill
```

`setup <client>` is the only routine per-agent configuration step: it adds one LitePSM Bridge MCP entry and, where required, a small LitePSM bootstrap skill/instruction. That entry must invoke a locally installed, version-pinned executable; do not configure agents to download or run an unpinned `@latest` package on every launch. `install`, `update`, and `remove` operate on the LitePSM-managed store by default, not on each agent's config. `plan` is read-only. Mutations require the local client to show and receive confirmation unless the user configured a narrow explicit policy. Avoid shell-pipe installers; the CLI/Bridge package is the bootstrap dependency.

## Core flows

### Skill install

1. Resolve listing and immutable skill version/digest.
2. Fetch from upstream or LitePSM's static metadata endpoint; bound archive size/file count and reject path traversal/symlink escapes.
3. Show source, files, scripts, install target, collision, and update policy.
4. On approval, stage in a temp directory, verify digest, then atomically move under the LitePSM-managed skill store.
5. The Bridge exposes skill metadata and loads selected skill text only when requested. A host-native export is optional and explicit, for clients whose native skill activation is preferred or required.
6. Record local lock metadata: listing ID, source, version/ref, digest, enabled state, and installed path.
7. Never run bundled scripts during installation. Skills are instructions/data; they can still influence agent behavior and require source review.

### MCP install

1. Resolve the upstream package/config and transport; show executable, arguments, endpoints, environment variable **names**, requested scopes, and filesystem/network implications.
2. Choose local stdio vs direct remote endpoint. The local Bridge may manage either provider; LitePSM's hosted service never hosts or proxies MCP calls.
3. Authenticate directly with the chosen provider using local OAuth or secret-store references where supported. Keep secrets out of LitePSM's host registration and public catalog.
4. On approval, install the provider record into the LitePSM-managed store. Do not edit agent configuration again.
5. Start/probe only after explicit approval; report negotiated server identity and tools without invoking action tools as a test. Enable selected capabilities separately under local policy.

### Plugin/toolkit install

1. Inspect components and per-host compatibility. Do not assume a plugin is portable because its skills are.
2. Resolve dependencies to pinned versions and show the complete plan.
3. Install portable components into the LitePSM-managed store and expose supported skills/MCP capabilities through the Bridge.
4. Use a native client adapter only for components that require a host-native plugin surface; otherwise keep the package managed centrally. Report partial compatibility.
5. Install selected toolkit members; never include every tool from a source by default.
6. Start or enable hooks only in a separate approval step with declared events and effects. Hooks remain host-specific and may require native installation.

### Agent-requested discovery/install

An agent can query a public Discovery MCP/API or the local Bridge and produce an `InstallPlan` with listing ID, exact version/digest, selected components, and requested access. A local install can be initiated by the CLI or Bridge only through the same user-confirmed plan path. If a host does not provide reliable confirmation/elicitation, the Bridge returns the exact CLI command instead of silently installing. The hosted Discovery MCP has no machine path or secret access.

After one-time setup, the local Bridge can expose a small, bounded tool surface (`search_catalog`, `list_installed`, `search_capabilities`, `load_skill`, and policy-checked `call_capability`). It must not publish every installed provider's tool schema to every request. Capability results are selected on demand; invocation arguments are validated against the provider schema locally. Writes, external communication, destructive actions, and credential changes are denied or require approval according to local policy.

## Client adapters

Each adapter has an explicit capability declaration:

```text
client_id, supported_versions
MCP config format + merge semantics for the one Bridge entry
bridge registration / refresh / removal support
native skills/plugins capabilities where available
auth setup guidance
can_configure / can_export / instructions_only
trust requirements and restart/reload behavior
```

Initial development should prioritize a small declared matrix, then expand from evidence. Proposed initial targets: Codex, Claude Code, Grok Build, OpenCode, plus generic MCP JSON/config export. Validate one-time Bridge registration first. Do not claim GUI/web surfaces are installable merely because their CLI counterpart is.

Adapters must preserve unrelated settings, create backups, avoid overwriting user-managed entries, and surface name collisions. All source/package formats stay intact unless a documented transformation is explicitly selected.

## Update, pin, remove

- Install pins a version and digest in a local lock record.
- Updates are user-initiated with a file/config diff and permission-delta summary by default.
- Optional auto-update can be enabled per package/source only after a later decision; never silently change executable MCP configuration or hooks.
- Remove only LitePSM-owned files/config sections. If ownership is uncertain or the user's file has diverged, stop and offer a manual cleanup plan.
- Disabling and uninstalling are distinct for hosts that support both states.

## Local state

Per user, outside the project by default:

```text
state/
  installs.lock       # immutable package identity/version/digest and enabled state
  providers/          # MCP provider config without secret values
  bridge/             # local policy, audit records, and bridge version state
  sources.json        # configured catalog URLs; no credentials
  backups/            # bounded pre-edit client config backups
```

The host Bridge registration points to a pinned local LitePSM executable/package version. Secrets are references into an OS secret store, not values in this state tree. Project-scoped installs are explicit and show files added to the repository. Native-only per-agent installs record the host and path in the lock file.
