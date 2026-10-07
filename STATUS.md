# LiteSPM Status — Truth Baseline

This file is the **single source of truth for what LiteSPM is**. Every other
document that makes a capability claim must agree with it or link here.

The vocabulary is fixed by
[ARCH/31 §2](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide).
These six states are distinct and may **not** be collapsed:

| State | Meaning | Minimum evidence |
|---|---|---|
| `DESIGNED` | Specified in an ARCH document. No code implied. | A normative section exists |
| `IMPLEMENTED` | Functions/types exist and are reachable from a package API. | Code compiles; unit tests pass |
| `WIRED` | Reachable from a real user workflow, with real dependencies supplied by production code. | A named non-test caller exists |
| `TESTED` | Covered by an automated test that would fail if the behaviour regressed. | A test asserts the behaviour, not merely that a symbol exists |
| `VERIFIED` | An independent falsifier attempted to break the claim and could not. | Mutation/reproduction evidence |
| `SHIPPED` | Published to users through a real release channel. | A tag, a published artifact, and a live origin serving it |

**Rule.** "Files exist" is at most `IMPLEMENTED`. "Tests exist" is at most
`TESTED`. A component whose only caller is a test is `IMPLEMENTED`, not `WIRED`.
Nothing is `SHIPPED` because it compiles.

Rows below report the **highest honest state** for the user-visible
capability, with the caveat that blocks the next rung. A row that is wired but
whose only real origin is broken is `WIRED` with the breakage stated, not
`SHIPPED`.

---

## 1. Control plane

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/domain` (IDs, models, RFC 8785 JCS, `LPSM-*` errors) | `TESTED` | Imported by every subsystem; `internal/domain/domain_test.go` + `canonical_test.go`. Number canonicalization follows ECMAScript `Number::toString` exactly — it previously used Go's `'g'` float format and emitted `4.900491e06` for sizes ≥ 10⁶, which no manifest decoder accepts (caught by the origin-replica run, 2026-10-05). Note: `CanonicalizeJSON` takes `[]byte`, not `any` | Independent falsification pass |
| `internal/config` (platform paths, env, legacy adoption) | `WIRED` | `internal/config/paths.go`; legacy `LITEPSM_*` fallback `internal/config/config.go:132`; `internal/config/legacy_test.go` | — |
| `internal/state` (SQLite WAL, 22 tables, safe CAS rollback journal) | `TESTED` | `internal/state/operations.go`; recovery tests; the daemon **halts** on recovery failure with `Fatal: startup recovery failed` → **exit 1** (`cmd/litespm/main.go:1173-1175`); `70` is a *doctor* category code only (`ARCH/20` §2) | Independent falsification pass |
| `internal/ipc` (Named Pipe DACL / Unix socket 0600 + peer authentication, dispatcher) | `TESTED` | `internal/ipc/`; the `Serve`/`Stop` race is fixed and reproduced under `-race -count=50`; peer auth (register item A6 / I1) in `peer_test.go` + `transport_{linux,darwin,windows,other}.go`: same-uid accepted over a real unix socket (`SO_PEERCRED`, handshake `peerAuth.verified` + `PeerFromContext`), uid-mismatch refused with `-32001` then closed, no-mechanism connections allowed but reported `verified:false` **with a reason** (`ARCH/11` §2.2) | A genuine cross-uid connect (needs a second OS user) and the macOS/Darwin code path run only in CI, not locally |
| `internal/bridge` (12-tool stateless stdio shim) | `WIRED` | `internal/bridge/shim.go`; launched by `litespm bridge stdio --host`. Read, capability and invoke tools resolve; `request_install` completes for skills and MCP servers; see §4 for the two remaining `-32601` tools | Implement the `invocation.get` / `invocation.cancel` handlers |
| Daemon (`litespm daemon serve`) | `WIRED` | `cmd/litespm/main.go:92-96` (dispatch) and `:1121` (`runDaemonServe`); recovery runs before the listener binds | — |
| `internal/policy` (effect taxonomy, install/skills gate) | `WIRED` | Evaluated on install and skills paths | Policy hierarchy + provenance (§5) |
| `internal/secrets` (OS vault + memory/file stores) | `WIRED` for vault open; launch injection **`IMPLEMENTED`, not `WIRED`** | The daemon opens the store before serving (`cmd/litespm/main.go:1135`) and `doctor` round-trips a canary. **Secrets are *not* injected at provider launch:** `ResolveLaunchSecrets` has test-only callers (`internal/secrets/secrets_test.go`, `test/canary_test.go`) and nothing populates `LaunchSpec.SecretEnv` (`ARCH/19` §1.1) | A production caller that injects resolved secrets at provider launch |
| `internal/doctor` (10 checks, `--repair`, category exit codes) | `WIRED` | `internal/doctor/`; exit codes 10/20/30/40/50/60/70 at `cmd/litespm/main.go:935-982` | Timing instrumentation (ARCH/31 §9) |
| Self-update (`litespm self-update`) | `WIRED` | `internal/update/`; real manifest fetch, SHA-256 fail-closed, downgrade guard, atomic replace. **No signature check** | Signed releases (§6) |
| Skills lifecycle (`add` / `list` / `update` / `remove`) | `WIRED` | `cmd/litespm/skills_*.go`; atomic swap, rollback, dry-run, provenance ledger in `internal/skills` | `skills-lock.json` import/export, skill diff |
| `internal/agent` (ACP registry, `agent list` / `agent resolve`) | `WIRED` | `cmd/litespm/main.go:562` (`runAgentCommand`), `:615` (`list`), `:641` (`resolve`); `internal/agent` is the one package whose documented inventory matches the tree (ARCH/24 §21) | — |
| Host adapters (`litespm host list\|detect\|setup\|remove`) | `WIRED` | 6 bespoke + 44 generic `BridgeTarget`s (`internal/host/targets_data.go`). **MCP-entry merge only** — no instruction/skill/agent projection and no `compile`. Every JSON/JSONC config is spliced by byte offset, never re-serialized (comments, key order, indentation, unknown keys and the trailing newline survive); removal finds the entry in any documented layout (`ARCH/16` §3.7.1). **Every row and all six hand-written adapters were re-verified against live vendor documentation on 2026-10-06**; that audit corrected an invented OpenCode layout, Cline's moved settings file, Pi's undocumented fallbacks, Amp's flat container key, Crush's required `type`, four ignored home-directory overrides and more (`ARCH/30` §8.1). **Environment forwarding is per-host verified data** (`internal/envref`, 2026-10-06): `install --env NAME` writes a reference in each host's own documented syntax, refuses a host with no documented behaviour, and reports three silent-failure modes at install time; Codex forwards by name through `env_vars` because its `env` table is literal-only | Projection of instruction/skill/command components; removal of an installed MCP entry |
| Interactive setup wizard (`litespm`, `setup`, `init`) | `WIRED` | A bare `litespm` invocation launches the wizard (`cmd/litespm/main.go:51-54`), as do `setup`/`init` (`:62-63`); advisories are **compiled into the binary** (`cmd/litespm/wizard.go:138-143` — no HTTP fetch at setup time); agent auto-detection, backup and single-entry merge only | A non-interactive `setup <agent>` form; parsing of the published `advisories` array |
| Web static marketplace | `SHIPPED` | `web/` builds a Next.js static export; the live origin `https://litespm.sarveshbh-2022.workers.dev` serves the site **and its release tree** (probe 2026-10-05: `/v1/current.json` 200, `/v1/releases/rel-2026-10-05-01/{manifest,listings,versions}.json` 200, byte-identical to the committed release) | Release tagging (`ARCH/18` §1–§2 `index.json`/`shards`/`items` remain un-emitted books) |
| npm wrapper + `postinstall` installer | `IMPLEMENTED` | `npm/package.json`, `npm/scripts/install-binary.js`; verification is **fail-closed** (missing manifest, missing entry, unreadable manifest or digest mismatch all discard the download; nothing unverified is installed or executed), redirects are **HTTPS-only**, and the wrapper no longer falls back to a `litespm` found on `PATH`. Covered by `npm test`, wired into CI. `files` now lists `LICENSE`/`NOTICE`, and `release.yml` stages them (`cp LICENSE NOTICE npm/`) before `npm publish --provenance` | Resolve the missing-checksum fail-open; publish through `release.yml` |
| `LICENSE` / `NOTICE` | `IMPLEMENTED` | Root `LICENSE` (Apache-2.0) and `NOTICE` exist and are included in the npm `files` allowlist (`npm/package.json`), staged by `.github/workflows/release.yml` | Publish, then verify the tarball listing |

## 2. Catalog & source plane

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/source` (8 upstream adapters) | `IMPLEMENTED` | `internal/source/`; **test-only callers**; `litespm catalog build` exists but reads the Python-produced dataset, not these adapters | A non-test caller that ingests into a published release |
| `litespm catalog build` (dataset → release tree) | `TESTED` | `cmd/litespm/main.go` (`runCatalogBuild`, `buildCatalogRelease`): validates the dataset fail-closed (`catalogbuild.ParseDataset`), converts it, cuts/advances the release id + sequence from the released pointer, refuses release-id reuse and non-advancing sequences, and materializes a released pointer byte-for-byte (`-materialize`). Release time is pinned to second precision so provenance and pointer cannot diverge. Tests: `cmd/litespm/catalog_build_test.go`, `internal/catalogbuild/dataset_test.go` (real 5,814-row dataset incl. manifest decode at real sizes), `internal/domain/canonical_test.go` | Run in `release.yml`/deploy with `SOURCE_DATE_EPOCH` for reproducible CI cuts |
| `internal/catalogbuild` `CompileRelease` | `TESTED` | `internal/catalogbuild/compiler.go`; emits `v1/current.json` + `v1/releases/<id>/{listings,versions,manifest}.json`; non-test caller `runCatalogBuild`; reproducibility asserted in `test/catalog_e2e_test.go` | Independent falsification pass |
| `litespm catalog sync` | `SHIPPED` | `internal/catalog/client.go`: verifies pointer→manifest→listings digests (raw bytes), validates `schemaVersion`/release-id path safety, re-verifies the cache on every load; `runCatalogSync`. **Verified against the live origin 2026-10-05** from a clean data root: `catalog sync` → release `rel-2026-10-05-01`, sequence 143, 5,814 indexed; `search postgres` resolved. Automated coverage: `test/catalog_e2e_test.go` (build → serve → sync → search → offline reload) plus tamper/fail-closed cases in `internal/catalog/integrity_test.go` | Release tagging; the legacy `litepsm` Worker still serves the same frozen release and should be retired |
| Deployed web catalog data | `SHIPPED` | `web/data/catalog.json` + the stats in `web/data/release.json` are built by `scripts/build_full_catalog.py` (Python) and bundled/served by the web app; the pointer/release identity keys in `release.json` are stamped by `litespm catalog build` (single writer per key) | Close `D4`: the split (Python = dataset ingestion, Go = release/pointer publication) is implemented but not yet adjudicated |
| Catalog release tree `/v1/releases/<id>/` | `SHIPPED` | Built by `litespm catalog build`; served live from `https://litespm.sarveshbh-2022.workers.dev` (probe 2026-10-05: all four files 200, byte-identical to the committed release `rel-2026-10-05-01`, pointer `manifestDigest` matches the served manifest bytes); `scripts/deploy-pages.sh` materializes the released tree byte-for-byte (`ARCH/18` §1–§2 `index.json`/`shards`/`items` remain un-emitted by design) | Release tagging; retire the legacy `litepsm` Worker |
| Catalog deltas / shards / FTS index | `DESIGNED` | One ~3.4 MB `catalog.json` blob; no shard or FTS output | A measured trigger, then shard + delta output |

## 3. Install plane

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/resolver` (pure DFS + semver) | `WIRED` | `internal/resolver/resolver.go`; called by the `resolver.prepare_plan` handler (`cmd/litespm/main.go:1359`, `resolver.NewResolver` at `:1849`, `SavePlan` at `:1391`) — `internal/install` does **not** import it; `planHash` persisted | Lockfile-driven frozen resolve |
| `internal/artifact` (bounded spool, safe archive extraction, canonical tree digest, HTTPS artifact fetcher) | `TESTED` | `internal/artifact/artifact_test.go` (`TestSpoolDownloadBounded`, `TestExtractArchiveSafely_Zip`, `TestExtractArchiveSafely_TarGz`, `TestComputeCanonicalTreeDigestDeterminism`), `test/fuzz_hostile_archive_test.go` (zip-slip, Windows drive-letter escape, case collision, tar symlink), and — for the fetcher — `internal/artifact/fetcher_test.go` (https-only, credential refusal, SSRF ranges incl. `169.254.169.254`, redirect cap + downgrade, byte bound, unpinned/wrong digest) plus `test/archive_install_e2e_test.go` (fetch → verify → extract → CAS install). Extraction is reached from production at `internal/install/engine.go:322,345` and `internal/doctor/engine.go:312` | The 100:1 compression-ratio (zip-bomb) check is not implemented (`ARCH/05` §2.1); the **fetcher has no production caller** until the catalog carries artifact locators, and `GitTreeFetcher` is still `DESIGNED` (`ARCH/17` §4) |
| `internal/install` engine (CAS staging + commit + rollback) | `IMPLEMENTED` | `internal/install/engine.go:204` rejects an `Execute` with neither `TreeSource` nor `ArchiveSource`; unit-tested. The engine now serves only plugin installs: skills go through the skills ledger and MCP servers through host-config registration | Supply a real artifact source for plugins |
| `litespm install <id>` (CLI) | `WIRED` **for skills and MCP servers**; `IMPLEMENTED` **(cannot complete)** for plugins | Skills: `installSkillFromListing` (`cmd/litespm/install_skill.go`) — real fetch → ledger write → `InstallRecord`, verified against the live catalog. MCP: `installMCPFromListing` (`cmd/litespm/install_mcp.go`) registers the published runtime descriptor as a named entry in each target host's config through `host.InstallServerEntry` (`internal/host/entry_install.go`) — surgical splice, backup, refusal on collision (`LPSM-NAME-CONFLICT`), refusal when no host is set up (`LPSM-INSTALL-TARGET-UNAVAILABLE`); `--host` and `--force` added; verified 2026-10-06 against the live catalog on a JSON host and a TOML host. `--env <NAME>` (repeatable) records that a server needs a variable and writes a **reference**, never a value, so a credential never passes through LiteSPM; verified 2026-10-06 end to end on a reference host and on the name-list host (reference → probe → tool call returned the resolved value; unset reported `<unset>` rather than blanking or leaking the literal). Plugins still fail closed with `LPSM-ARTIFACT-UNAVAILABLE` | Plugin packaging; remote (URL) MCP entries — the catalog publishes no URL |
| `install.execute` handler + Bridge `request_install` | `WIRED` **for skills and MCP servers**; `IMPLEMENTED` **(cannot complete)** for plugins | `cmd/litespm/main.go` resolves the plan's listing and routes `kind=skill` to the skills ledger (`TestInstallExecuteSkillThroughDaemon`) and `kind=mcp` to the host-config registration above, returning the affected hosts, entry name and command. The launch line comes from the version record, which is why `catalog sync` now fetches and digest-verifies `versions.json` (`internal/catalog/versions_test.go`: fetch, tamper rejection, fail-closed with no runtime) | Plugin packaging |
| `internal/lifecycle` install transaction + symmetric removal/restore | `IMPLEMENTED` | `lifecycle.InstallTxn` wraps every host-config and skill install: file writes register LIFO compensations (pre-write backups / created-file removal) and all state rows — install, component, registration, deployment ledger — commit in one SQLite transaction (`state.DB.WithTx`). Failure-injection tests (`cmd/litespm/install_txn_test.go`) prove configs byte-identical to pre-install with no orphan rows at each step. Removal is ledger-driven and symmetric for every kind (`install.remove` → `removeInstalledPackage`), with a pre-ledger best-effort strip via `host_registrations`; `litespm restore` replays the recorded backups byte-for-byte and refuses on sibling changes or edited owned nodes (`cmd/litespm/restore_test.go`), incl. TOML and skill-directory paths | Crash-window journal between file write and commit; ledgered update routing (ARCH/33 §6) |

## 4. Provider & invocation runtime

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/provider` supervisor (start / stop / probe) | `WIRED` | `internal/provider/`; `provider.probe` returns the real supervisor view or an explicit not-found | Persist provider rows on install (`m4`) |
| IPC `capabilities.search` / `capabilities.describe` + Bridge `search_capabilities` / `describe_capability` | `WIRED` | Handlers at `cmd/litespm/main.go:2089` and `:2135` read `db.ListCapabilities` / `db.GetCapability` over rows `internal/discover` wrote; the Bridge forwards and returns real results | — (invocation registry below) |
| IPC `provider.invoke` + Bridge `invoke_capability` | `WIRED` | `cmd/litespm/main.go` calls `discover.Invoke` **with `discover.WithPolicy(policyEngine)`** (the gate existed but was unreachable until 2026-10-07: without the option every effectful invocation failed `no policy engine configured`). Ungranted tools now fail closed with the approval-required ask, granted ones pass Tier 3 with schema-drift re-verification on every call, and the user's deny rules are consulted. The CLI invoke path attaches the engine too. Grant creation is `litespm grant <capabilityId>` (interactive terminal required, same gate as `litespm approve`); `TestProviderInvokeGateOpensWithAGrant` pins the daemon-side loop | Persist provider rows on install so a fresh machine can invoke without a prior `capabilities refresh`; session-scoped leases on top of grants (ARCH/35, needs ARCH/34 registry) |
| IPC `invocation.get` / `invocation.cancel` + Bridge `get_invocation` / `cancel_invocation` | `IMPLEMENTED` | Registered handlers return explicit `-32601` (`cmd/litespm/main.go:2216`, `:2225`) with the concrete reason: `provider.invoke` is synchronous and the asynchronous registry is `ARCH/34`, still `DESIGNED`. **These two are the only `-32601` handlers left** | Invocation registry + receipt schema (`ARCH/34`) |
| State writers for `providers` / `capabilities` / `host_registrations` | `WIRED` | Production callers exist: `SaveProvider` + `SaveCapability` from `internal/discover`; `SaveHostRegistration` from `cmd/litespm/install_mcp.go` | — |
| State writers for `capability_grants` / `audit_events` | `capability_grants` **`WIRED`**; `audit_events` still `IMPLEMENTED` (no non-test caller) | `SaveCapabilityGrant` is called by `cmd/litespm/grant.go` (one deterministic row per capability, immutable `granted_by`/`granted_at` on re-save, rebind on schema re-approval) and read by `internal/policy` Tier 3; `ListCapabilityGrants` / `SetCapabilityGrantStatus` back `grant list` / `grant revoke` | Call `RecordAuditEvent` from the grant/invoke path |
| `internal/discover` (probe installed servers → capability rows, invoke a tool) | `WIRED` | `internal/discover/discover.go`; spawns a configured host's registered server, `mcpclient.ProbeProvider` → `providers` + `capabilities` rows, and invokes one capability after re-checking its schema fingerprint. First production caller of `SaveProvider`, `SaveCapability` and `mcpclient`; `GetProvider`/`GetCapability`/`ListCapabilities`/`GetInstallComponent` readers added. Wired to `capabilities.search`, `capabilities.describe`, `provider.invoke` and the `litespm capabilities` / `litespm invoke` commands. Verified end to end against a locally published release and a real MCP server (sync → install → probe → list → describe → invoke), including refusal on schema drift | `invocation.get`/`cancel` (ARCH/34 `DESIGNED`); env metadata from the catalog (the user supplies variable names via `--env`, which the probe resolves) |
| `internal/mcpclient` (dual-profile MCP client) | `WIRED` | `internal/mcpclient/`; its first production importer is `internal/discover`, which dials an installed server, probes its tools and calls one. `ConnectStdio` now completes the handshake with `notifications/initialized`, without which a real server connects and then hangs on the first `tools/list` | Remote transports (`ConnectStreamableHTTP`, `ConnectLegacy`) still have no caller — the catalog publishes no URL |
| `internal/auth` (OAuth PKCE loopback broker) | `IMPLEMENTED` | `internal/auth/`; **zero production importers** | Wire into provider launch |
| Connector executor | `DESIGNED` | `internal/connector` **deleted** under decision `D1`; `ARCH/29` is a design record only | Re-decide `D1` before any implementation |
| Invocation engine + tamper-evident receipts | `DESIGNED` | Blocked on the registry above | Registry + receipt schema |
| Runtime profiles | `DESIGNED` (library `IMPLEMENTED`, unwired) | `internal/profiles` parses/validates/diffs profiles (tested); no `profile` verbs, no OCI distribution, nothing consumes the library | Verb set + activation flow (ARCH/35 §2) |
| Capability leases | `DESIGNED` (library `IMPLEMENTED`, unwired) | `internal/profiles` issues/checks/revokes leases with mandatory TTL, scope binding and schema pinning (tested); no caller consults a lease at the invoke gate | Registry first (ARCH/34), then gate invocation on leases |
| Token / context budgets | `DESIGNED` | — | — |

## 5. Governance & evidence

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| Project manifest (`litespm.yml`) + lockfile (`litespm.lock`) | `DESIGNED` | Neither file nor package exists | Schema + frozen install |
| Deployment ledger + three-way reconciliation | `IMPLEMENTED` (install/reconcile/uninstall) | Migration 002 creates `deployment_mutations`, 003 adds `backup_path`; installs write ledger rows in the same SQLite transaction as install/component/registration rows with node-level pre/post images and `PriorEntry` (`internal/lifecycle`, `cmd/litespm/install_mcp.go`); `internal/deployment.Reconcile` drives `install.remove` (strip-what-still-matches, keep-what-the-user-edited) and `litespm restore` replays backups byte-for-byte with two refusal guards. Failure injection tests prove configs byte-identical and no orphan rows | §6 update routing through the ledger; crash-window journal (ARCH/33 §2) |
| Canonical identity + alias graph | `DESIGNED` | `Listing` ID grammar is normative (ARCH/10); no cross-source edge set | Identity graph over `internal/domain` |
| Interop (OpenAPM, `skills-lock.json`) | `DESIGNED` | No importer/exporter exists | `internal/interop` |
| Policy inheritance (tighten-only) + `policy explain` | `DESIGNED` | `policy.Engine.Evaluate` is flat with injected `[]DenyRule`; no hierarchy or provenance | `PolicyDecision.provenance` + layered rules |
| `audit --ci` + SARIF + scratch replay | `DESIGNED` | No audit command; no replay | Ledger to replay, then a replay command |
| SBOM + signed releases + catalog pointer signing | releases + SBOM **`IMPLEMENTED`** (workflow; first signed release on the next `v*` tag); catalog pointer signing **`WIRED`**; TUF envelope still `DESIGNED` | `release.yml` runs `cosign sign-blob --bundle` over every artifact (keyless, identity = this repository's release workflow on a tag) plus a CycloneDX SBOM; `deploy-pages.sh` signs `/v1/current.json` when an OIDC identity exists and the catalog client verifies the bundle before consuming the pointer; `litespm self-update` verifies the binary's bundle (`LITESPM_REQUIRE_SIGNED_UPDATE=1` refuses unsigned). npm installer still checksum-only (stated gap in `SECURITY.md`) | TUF-style root/timestamp/snapshot for the catalog; npm bundle verification |
| Static content scan (labelled *scan*) | `DESIGNED` | `internal/doctor` has a canary mechanism to build on; no scanner | `internal/scan` emitting verdicts with reasons |
| Compatibility evidence records | `DESIGNED` | Host lists are publisher-declared, not test results (`ARCH/26` §4.2) | Host × OS × version × date × test records |
| Sandbox / isolation tier declaration | `DESIGNED` | No tier is declared; `ARCH/08` refuses OS-sandbox claims and `doctor` reports process supervision only | Declare the true tier in docs and report it from `doctor` |
| `adopt` migration flow | `DESIGNED` | No `adopt_tool` handler/tool exists; host detection is read-only | Ownership records (needs the ledger) |

## 6. Interfaces & packaging

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| TUI / local dashboard | `DESIGNED` | CLI only; the web site is public and separate | Daemon-call-only TUI |
| Shell completion (bash/zsh/fish/PowerShell) | `DESIGNED` | None | `cmd/litespm` generators |
| Air-gapped signed bundle | `DESIGNED` | — | Signed offline bundle (needs §5 signing) |
| `scripts/build_full_catalog.py` (Python builder) | `WIRED` **(dataset producer, snapshot-recording)** | Every upstream fetch is recorded by `scripts/snapshot_store.py` (raw bytes + ETag + digest, immutable, gitignored `source-snapshots/`) and replayed from the recording by default — two runs with no network are byte-identical, `--refresh` uses conditional requests, a digest mismatch refuses the build, and a failed refresh prints `STALE` instead of falling back silently (`internal/source` mirrors the format with test-only callers). The builder publishes `web/data/catalog.json` and the dataset stats in `web/data/release.json`; it no longer touches `web/public/v1/*` — the served pointer and release tree belong to `litespm catalog build` (single writer per key). Listing ids are funnelled through fail-closed `canonical_id()` (an earlier revision shipped six ids the domain grammar rejects). CI never runs it (`ARCH/31` §4.3) | Close `D4` (the ingestion/publication split above is implemented, not adjudicated); add row assertions |

**Companion guides for this section.** [`scripts/README.md`](scripts/README.md) inventories every
script in `scripts/` and records which workflow (if any) runs it — `ci.yml` syntax-checks them all
**and executes `deploy-pages.sh`** in its `build-web` job, and `release.yml` executes
`build-release.sh`. [`web/README.md`](web/README.md) documents the static
marketplace: verified commands, layout, deploy path, and why the site and the binary can read
different catalog files.

---

## What this file supersedes

Where `README.md`, `AGENTS.md`, `TEST.md`, `npm/README.md`, or `ARCH/12`,
`ARCH/13`, `ARCH/20`, `ARCH/21`, `ARCH/24` implied a higher state than a row
above, **this file wins**. The specific false claims removed in this pass are
listed in the commit that introduced this file; the durable statement is the
table.

## Not yet true, and not claimed

Provider **autostart** (the `providers` table is written by `internal/discover` on
`capabilities refresh`, but nothing persists a provider row **at install time**, so a fresh
machine has nothing to autostart); a project manifest + lockfile; signed releases or packages;
runtime policy enforcement at install time; `invocation.get` / `invocation.cancel` (an
invocation registry); plugin installation; and any isolation level beyond process supervision.
(An agent-driven install **does** complete for skill and MCP-server listings — plan + human
approval → skills ledger / host-config registration. The catalog release tree **is** published
and the live origin serves it — `catalog sync` was verified against it on 2026-10-05.)
Signed releases remain the gap that blocks the most downstream work.
