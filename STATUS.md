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
| `internal/domain` (IDs, models, RFC 8785 JCS, `LPSM-*` errors) | `TESTED` | Imported by every subsystem; `internal/domain/domain_test.go`. Note: `CanonicalizeJSON` takes `[]byte`, not `any` | Independent falsification pass |
| `internal/config` (platform paths, env, legacy adoption) | `WIRED` | `internal/config/paths.go`; legacy `LITEPSM_*` fallback `internal/config/config.go:132`; `internal/config/legacy_test.go` | — |
| `internal/state` (SQLite WAL, 22 tables, safe CAS rollback journal) | `TESTED` | `internal/state/operations.go`; recovery tests; the daemon **halts** on recovery failure with `Fatal: startup recovery failed` → **exit 1** (`cmd/litespm/main.go:1173-1175`); `70` is a *doctor* category code only (`ARCH/20` §2) | Independent falsification pass |
| `internal/ipc` (Named Pipe DACL / Unix socket 0600, dispatcher) | `TESTED` | `internal/ipc/`; the `Serve`/`Stop` race is fixed and reproduced under `-race -count=50` | — |
| `internal/bridge` (12-tool stateless stdio shim) | `WIRED` | `internal/bridge/shim.go`; launched by `litespm bridge stdio --host`. Read tools resolve; see §4 for the `-32601` tools | Implement the `-32601` handlers |
| Daemon (`litespm daemon serve`) | `WIRED` | `cmd/litespm/main.go:92-96` (dispatch) and `:1121` (`runDaemonServe`); recovery runs before the listener binds | — |
| `internal/policy` (effect taxonomy, install/skills gate) | `WIRED` | Evaluated on install and skills paths | Policy hierarchy + provenance (§5) |
| `internal/secrets` (OS vault + memory/file stores) | `WIRED` for vault open; launch injection **`IMPLEMENTED`, not `WIRED`** | The daemon opens the store before serving (`cmd/litespm/main.go:1135`) and `doctor` round-trips a canary. **Secrets are *not* injected at provider launch:** `ResolveLaunchSecrets` has test-only callers (`internal/secrets/secrets_test.go`, `test/canary_test.go`) and nothing populates `LaunchSpec.SecretEnv` (`ARCH/19` §1.1) | A production caller that injects resolved secrets at provider launch |
| `internal/doctor` (10 checks, `--repair`, category exit codes) | `WIRED` | `internal/doctor/`; exit codes 10/20/30/40/50/60/70 at `cmd/litespm/main.go:935-982` | Timing instrumentation (ARCH/31 §9) |
| Self-update (`litespm self-update`) | `WIRED` | `internal/update/`; real manifest fetch, SHA-256 fail-closed, downgrade guard, atomic replace. **No signature check** | Signed releases (§6) |
| Skills lifecycle (`add` / `list` / `update` / `remove`) | `WIRED` | `cmd/litespm/skills_*.go`; atomic swap, rollback, dry-run, provenance ledger in `internal/skills` | `skills-lock.json` import/export, skill diff |
| `internal/agent` (ACP registry, `agent list` / `agent resolve`) | `WIRED` | `cmd/litespm/main.go:562` (`runAgentCommand`), `:615` (`list`), `:641` (`resolve`); `internal/agent` is the one package whose documented inventory matches the tree (ARCH/24 §21) | — |
| Host adapters (`litespm host list\|detect\|setup\|remove`) | `WIRED` | 6 bespoke + 44 generic `BridgeTarget`s (`internal/host/targets_data.go`). **MCP-entry merge only** — no instruction/skill/agent projection and no `compile` | Projection of instruction/skill/command components |
| Interactive setup wizard (`litespm`, `setup`, `init`) | `WIRED` | A bare `litespm` invocation launches the wizard (`cmd/litespm/main.go:51-54`), as do `setup`/`init` (`:62-63`); advisories are **compiled into the binary** (`cmd/litespm/wizard.go:138-143` — no HTTP fetch at setup time); agent auto-detection, backup and single-entry merge only | A non-interactive `setup <agent>` form; parsing of the published `advisories` array |
| Web static marketplace | `WIRED` | `web/` builds a Next.js static export; the live origin answers `GET /v1/current.json` 200 | A published site artifact whose release tree resolves (see below) |
| npm wrapper + `postinstall` installer | `IMPLEMENTED` | `npm/package.json`, `npm/scripts/install-binary.js`; on a **missing** checksum entry it warns and proceeds unverified. `files` now lists `LICENSE`/`NOTICE`, and `release.yml` stages them (`cp LICENSE NOTICE npm/`) before `npm publish --provenance` | Resolve the missing-checksum fail-open; publish through `release.yml` |
| `LICENSE` / `NOTICE` | `IMPLEMENTED` | Root `LICENSE` (Apache-2.0) and `NOTICE` exist and are included in the npm `files` allowlist (`npm/package.json`), staged by `.github/workflows/release.yml` | Publish, then verify the tarball listing |

## 2. Catalog & source plane

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/source` (8 upstream adapters) | `IMPLEMENTED` | `internal/source/`; **test-only callers**; no `catalog build` CLI command | A non-test caller that ingests into a published release |
| `internal/catalogbuild` `CompileRelease` | `IMPLEMENTED` | `internal/catalogbuild/compiler.go`; emits `v1/current.json` + `v1/releases/<id>/{listings,versions,manifest}.json`; `CompileRelease` / `BuildOutput.WriteToDirectory` have **no non-test caller** (the package *is* imported by `internal/catalog/client.go:14`, but only for its result types, `CurrentPointer` / `ReleaseManifest`) | A non-test caller + CI publish |
| `litespm catalog sync` | `WIRED` **(broken at origin)** | `internal/catalog/client.go`; `cmd/litespm/main.go:83-85` (`runCatalogSync`). Live (re-probed 2026-10-05): `/v1/current.json` 200 but `/v1/releases/rel-2026-09-30-01/manifest.json` **404** | Publish a release tree whose fields match `CurrentPointer`; add a live-sync test |
| Deployed web catalog data | `SHIPPED` | `web/data/catalog.json` is built by `scripts/build_full_catalog.py` (Python) and bundled/served by the web app | Unify with the Go builder (`D4` is contested) |
| Catalog release tree `/v1/releases/<id>/` | `DESIGNED` | Never published; live 404. ARCH/18 §1–§2 also specify `index.json` / `shards/` / `items/` the compiler does not emit | Publish the tree from committed snapshots |
| Catalog deltas / shards / FTS index | `DESIGNED` | One ~3.4 MB `catalog.json` blob; no shard or FTS output | A measured trigger, then shard + delta output |

## 3. Install plane

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/resolver` (pure DFS + semver) | `WIRED` | `internal/resolver/resolver.go`; called by the `resolver.prepare_plan` handler (`cmd/litespm/main.go:1359`, `resolver.NewResolver` at `:1849`, `SavePlan` at `:1391`) — `internal/install` does **not** import it; `planHash` persisted | Lockfile-driven frozen resolve |
| `internal/artifact` (bounded spool, safe archive extraction, canonical tree digest) | `TESTED` | `internal/artifact/artifact_test.go` (`TestSpoolDownloadBounded`, `TestExtractArchiveSafely_Zip`, `TestExtractArchiveSafely_TarGz`, `TestComputeCanonicalTreeDigestDeterminism`) and `test/fuzz_hostile_archive_test.go` (zip-slip, Windows drive-letter escape, case collision, tar symlink); reached from production code at `internal/install/engine.go:322,345` and `internal/doctor/engine.go:312` | The 100:1 compression-ratio (zip-bomb) check is not implemented (`ARCH/05` §2.1); an `ArtifactFetcher` seam (`ARCH/17` §4) |
| `internal/install` engine (CAS staging + commit + rollback) | `IMPLEMENTED` | `internal/install/engine.go:204` rejects an `Execute` with neither `TreeSource` nor `ArchiveSource`; unit-tested | Supply a real artifact source |
| `litespm install <id>` (CLI) | `IMPLEMENTED` | `cmd/litespm/main.go:855,858-872` fabricates an **in-memory synthetic zip**; label `local synthetic package; remote resolve/verify not yet wired` | Real resolve → verify → fetch |
| `install.execute` handler + Bridge `request_install` | `IMPLEMENTED` **(cannot complete)** | `cmd/litespm/main.go:1420-1426` supplies no source; `internal/bridge/shim.go:341` calls it. An agent using `/marketplace` cannot install anything | Same artifact-source wiring (`M3`) |

## 4. Provider & invocation runtime

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| `internal/provider` supervisor (start / stop / probe) | `WIRED` | `internal/provider/`; `provider.probe` returns the real supervisor view or an explicit not-found | Persist provider rows on install (`m4`) |
| IPC `provider.invoke` / `invocation.get` / `invocation.cancel` + Bridge `invoke_capability` / `get_invocation` / `cancel_invocation` | `IMPLEMENTED` | Registered handlers return explicit `-32601` with concrete reasons (`cmd/litespm/main.go:1676-1699`; codes at `:1678`, `:1687`, `:1696`); no invocation registry exists | Invocation registry + persisted provider/capability rows |
| IPC `capabilities.search` / `capabilities.describe` + Bridge `search_capabilities` / `describe_capability` | `IMPLEMENTED` | `cmd/litespm/main.go:1626-1642` (codes at `:1630`, `:1639`); explicit `-32601`; Bridge tools exist but resolve nowhere | A capability index |
| State writers for `providers` / `capabilities` / `capability_grants` / `audit_events` / `host_registrations` | `IMPLEMENTED` | Writers exist in `internal/state`; **no non-test caller** per the wiring audit | Call them from the install/runtime path |
| `internal/mcpclient` (dual-profile MCP client) | `IMPLEMENTED` | `internal/mcpclient/`; **zero production importers** | Wire into provider dispatch |
| `internal/auth` (OAuth PKCE loopback broker) | `IMPLEMENTED` | `internal/auth/`; **zero production importers** | Wire into provider launch |
| Connector executor | `DESIGNED` | `internal/connector` **deleted** under decision `D1`; `ARCH/29` is a design record only | Re-decide `D1` before any implementation |
| Invocation engine + tamper-evident receipts | `DESIGNED` | Blocked on the registry above | Registry + receipt schema |
| Runtime profiles | `DESIGNED` | No profile model exists | — |
| Capability leases | `DESIGNED` | — | — |
| Token / context budgets | `DESIGNED` | — | — |

## 5. Governance & evidence

| Subsystem | State | Evidence | To reach the next state |
|---|---|---|---|
| Project manifest (`litespm.yml`) + lockfile (`litespm.lock`) | `DESIGNED` | Neither file nor package exists | Schema + frozen install |
| Deployment ledger + three-way reconciliation | `DESIGNED` | `host_backups` is a pre-edit snapshot, not an ownership model | `deployment_mutations` ledger + merge semantics |
| Canonical identity + alias graph | `DESIGNED` | `Listing` ID grammar is normative (ARCH/10); no cross-source edge set | Identity graph over `internal/domain` |
| Interop (OpenAPM, `skills-lock.json`) | `DESIGNED` | No importer/exporter exists | `internal/interop` |
| Policy inheritance (tighten-only) + `policy explain` | `DESIGNED` | `policy.Engine.Evaluate` is flat with injected `[]DenyRule`; no hierarchy or provenance | `PolicyDecision.provenance` + layered rules |
| `audit --ci` + SARIF + scratch replay | `DESIGNED` | No audit command; no replay | Ledger to replay, then a replay command |
| SBOM + signed releases + TUF-style catalog signing | `DESIGNED` | `SECURITY.md`: `SHA256SUMS.txt` only; releases unsigned | cosign-signed releases, then SBOM, then catalog trust root |
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
| `scripts/build_full_catalog.py` (Python builder) | `WIRED` **(deployed producer)** | It is what publishes `web/public/v1/current.json` and `web/data/*`; CI never runs it (`ARCH/31` §4.3) | Honour or amend `D4` (one builder) |

**Companion guides for this section.** [`scripts/README.md`](scripts/README.md) inventories every
script in `scripts/` and records which workflow (if any) runs it — `ci.yml` only syntax-checks them
and `release.yml` runs exactly one. [`web/README.md`](web/README.md) documents the static
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

A published catalog release tree with a succeeding `catalog sync`; an
end-to-end agent-driven install; provider autostart (the `providers` table is
never populated by non-test code); a project manifest + lockfile; signed
releases or packages; runtime policy enforcement; `invoke` / `get` / `cancel`;
an invocation registry; and any isolation level beyond process supervision.
Signed releases and the catalog release tree are the two gaps that block the
most downstream work.
