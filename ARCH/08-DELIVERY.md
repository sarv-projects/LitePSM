# Delivery plan

Every phase starts **proposed**. A phase becomes implemented only when code exists; verified only when a repeatable check passes on a recorded revision/environment; accepted only with a review/acceptance record.

## Phase 0 — Product and contracts

- Confirm LitePSM name/domain/npm identity.
- Finalize listing, source, compatibility, version/digest, and InstallPlan schemas.
- Define initial policy for public catalog ingestion, stale listings, duplicates, and “tested” badges.
- Choose first client adapter targets and minimum supported versions.

Acceptance evidence: decision records approved; schema examples validate; source/version and credential boundaries are unambiguous.

## Phase 1 — Private source repo and public static catalog

- Private repository with source schemas, curation inputs, CI build, and public-site assets.
- Cloudflare Pages deploys from that private repository to a public endpoint.
- Public JSON index, per-kind/category shards, item records, cache validators, and public source links.
- Manual/reviewed updates first; no accounts, API database, or ingestion jobs that execute upstream code.

Acceptance evidence: anonymous clients fetch search/index/detail JSON; a public deployment reveals only intended static output; catalog build rejects invalid records and preserves source provenance.

## Phase 2 — Local CLI and LitePSM-managed skill store

- Ship CLI binary and one npm wrapper if packaging/name is available.
- `search`, `info`, `plan install`, `install`, `list`, `remove`, and `doctor` for the LitePSM-managed skill store.
- Keep skill loading separate from installation; return selected skill text through the local Bridge only when requested.
- Lock exact source/version/digest; preview writes; stage, verify, atomically install; no script execution.

Acceptance evidence: reproducible skill fixture installs into a temporary user scope; malicious archive/path cases are refused; removal changes only LitePSM-owned files.

## Phase 3 — One-time host setup and local Bridge

- Implement `litepsm setup <client>` for the first host adapter(s); register one local LitePSM Bridge MCP entry per supported agent.
- Preserve unrelated host settings and make setup idempotent; do not edit host MCP config for every provider.
- Parse official MCP Registry metadata and supported generic MCP manifests; store selected providers in LitePSM-managed local state.
- Design the Bridge's bounded, on-demand capability discovery, local schema validation, action policy, and approval behavior.
- Separate one-time host setup from provider install, provider start, authentication, and tool invocation.

Acceptance evidence: config merge/rollback fixtures for each client version; repeated setup is idempotent; installing another provider does not modify host config; credentials absent from host config/log/state; provider start and risky calls are governed by explicit local policy.

## Phase 4 — Plugin formats and federation

- Add portable plugin schema and adapters for agreed Claude-, Codex-, and Grok-compatible marketplace manifest formats.
- Add documented skills APIs/Git and MCP Registry adapters.
- Add user-supplied Git marketplace sources and connector-as-MCP listings.
- Build compatibility matrix; retain raw format and report unsupported components.

Acceptance evidence: pinned public fixtures parse and resolve to exact upstream refs; schema fixtures show correct per-host component support; source failures do not publish stale data as current.

## Phase 5 — Agent access

- Publish static Catalog API contract and optional hosted read-only `litepsm-discovery` MCP (`search`, `inspect`, `prepare_install`).
- Add local Bridge tools for installed-item search, on-demand skill loading, capability description/invocation, and constrained install-plan approval where a host supports reliable confirmation.
- Provide host-specific one-time setup instructions, capability limits, and a CLI fallback when in-agent confirmation is unavailable.

Acceptance evidence: agents can search public entries and locally installed capabilities; hosted discovery cannot access machine state; local Bridge rejects uninstalled capabilities, arbitrary paths/commands, invalid schemas, and unapproved effects; each agent has evidence for one-time registration and confirmation behavior.

## Phase 6 — SDK and publisher workflow

- Release typed catalog SDK after API schema stabilizes.
- Add reviewed publisher submissions and private catalog support only after data retention, auth, moderation, and threat model are approved.
- Consider Worker/D1 for dynamic metadata. Consider R2 only for deliberate package mirroring.

Acceptance evidence: API compatibility suite, publisher auth tests, audit/data-retention decisions, and documented hosting cost/limits.

## Deferred

- Native connector implementations for each SaaS.
- Running MCP servers or hooks in LitePSM cloud.
- Marketplace telemetry/rankings.
- Automatic updates enabled by default.
- Claims of complete coverage of private or proprietary vendor directories.
- Beautiful production website beyond the first usable search/inspect/install surface.
