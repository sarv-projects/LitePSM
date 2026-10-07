# LiteSPM Remediation Plan

> **Not the status authority.** [STATUS.md](STATUS.md) is the single source of truth for what each
> subsystem actually does — its six states (`DESIGNED` / `IMPLEMENTED` / `WIRED` / `TESTED` /
> `VERIFIED` / `SHIPPED`) are binding repo-wide. This file is the **remediation history and
> open-findings tracker**: the audit snapshot, the locked decisions, the waves that were fixed and
> independently falsified, and what is still open. For a capability claim, read STATUS.md; for
> defect provenance and verification protocol, read this file.

**Source:** full-repo audit (159 findings) re-verified at `510df41` → snapshot **10 FIXED, 42
PARTIAL, 107 NOT FIXED**. That split is the *historical baseline of the audit*, not a live count —
it is reconciled in [Where things stand](#where-things-stand-oct-5-post-rebrand-and-post-doc-overhaul).
Tracker cell vocabulary: `TODO` / `IN_PROGRESS` / `DONE` / `VERIFIED` (`VERIFIED` = independent
falsifier confirmed with evidence + mutation tests + toolchain green).

## Where things stand (Oct 5, post-rebrand and post-doc-overhaul)

- Product renamed **LiteSPM**; `litePSM` is a symlink to `liteSPM`; module `github.com/sarv-projects/litespm`; CLI `cmd/litespm`; npm `litespm`. The git remote is still `git@github.com:sarv-projects/LiteSPM.git` (casing differs from the module path — finding `m8`, non-breaking).
- HEAD is `ff0a1db` ("refactor!: rebrand to LiteSPM and land verified remediation phases 0-3"), which superseded `fe42c28` and `4332f30`. The rename and the Phase 3 completion/review fixes are **committed**, not pending. The documentation overhaul below is **uncommitted working-tree** work.
- Toolchain: `gofmt` clean · `go build ./...` · `go vet ./...` · `go test -count=1 ./...` **all 23 packages ok** · web `npx tsc --noEmit` exit 0 — re-run on 2026-10-05 during this documentation pass. CI (`.github/workflows/ci.yml`) is the recurring check; this file does not assert the live run status (`scripts/check_ci.py` reports it with exit 0/1/2).
- **Snapshot reconciliation.** The 10/42/107 split predates `fe42c28` and `ff0a1db`. Everything recorded as `DONE` / `VERIFIED` in Wave 1 and Wave 2 below landed after that snapshot, so the totals are stale as counts. They are deliberately **not** re-numbered here: no re-audit has been run since. The current honest state of each capability is in [STATUS.md](STATUS.md); the re-audit gate is **P9** (0 ❌, ≤5 🟡).
- Open items today: the Phase-1 security batch from the 159-findings audit (auth broker https/state/verifier/mutex, IPC `SO_PEERCRED` + deadlines, policy fail-open DB paths, approval subject binding, bridge `-race` guard gap) and the deferred Phase-4 wiring (source pipeline, artifact-source install for plugins = `M3`, provider-row persistence on install = `m4`, partial-CAS recovery = `m6`). **Closed since this was written:** the npm fail-open checksums (`npm/scripts/install-binary.js` is now fail-closed on every path — [SECURITY.md](SECURITY.md) "Supply chain"), and `mcpclient`'s zero-importer state (`internal/discover` is its first production importer).
- **New, tracked in [ARCH/31 §4](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#4-new-defects-found-in-this-repository-during-the-comparison) rather than by the audit numbering below**, because they post-date the 159-finding audit:
  1. ~~the agent-facing install path completes for skills but not for MCP/plugin~~ — **closed 2026-10-06**: `install.execute` routes `kind=mcp` to host-config registration (`cmd/litespm/install_mcp.go`) with a plan + human approval (`install_authz.go`); only **plugins** still lack an artifact source (`M3`);
  2. ~~the catalog release tree is never published, so `catalog sync` 404s against the live origin~~ — **closed 2026-10-05**: the origin serves `/v1/current.json` and all of `/v1/releases/rel-2026-10-05-01/` with `200`, byte-identical to the committed release ([STATUS.md](STATUS.md) §2);
  3. `scripts/build_full_catalog.py` is a second catalog builder, contradicting **D4**;
  4. `ARCH/18` specifies `index.json`/`shards/`/`items/` outputs the compiler does not emit. (Its §4 `_headers` cache policy is implemented in `scripts/deploy-pages.sh:31-42`.)

## Decisions (locked)

- **D1** — Wire `auth`+`mcpclient`+provider supervision; wire `source`/`connector` minimally **or delete** — delete rather than ship unreachable code. (`connector` was deleted: see Wave 2, findings 98/119.)
- **D2** — Fabricated success → explicit `not_implemented` RPC errors / non-zero CLI exits (fail-closed); tests asserting fabrication rewritten.
- **D3** — Platforms windows/darwin/linux; `unix` build tags; plan9/js failures outside `internal/provider` out of scope.
- **D4** — Go `catalogbuild` is the single builder; canonical listing-ID = 4-seg. **CONTESTED (2026-10-05):** `scripts/build_full_catalog.py` is the actual producer of the deployed catalog, and `CompileRelease` has no non-test caller. D4 must either be honoured (delete the Python builder, wire and publish the Go one) or explicitly amended. See [ARCH/31 §4.3](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#43-decision-d4-is-contradicted-by-the-repository--there-are-two-catalog-builders).

## Wave 1 — Phases 0 + 2 (+ crosscheck defects X1–X5) — VERIFIED

Falsified then fixed and re-verified: **X1** diskspace build tags narrowed (netbsd/openbsd green; solaris/illumos fail only via pre-existing `modernc.org/sqlite`, D3); **X2** `skills.read_resource` fail-closed; **X3** bridge standalone fail-closed + tests inverted to the strict contract; **X4** `check_ci.py` workflow-scoped URL + guards + 0/1/2 exit contract; **X5** doctor canary Delete on all paths. Phase 0/2 findings (gofmt, 158, 124, 93, 40, 41, 42, 43, 45) DONE.

## Wave 2 — Phase 3 completion — VERIFIED

| Finding | Outcome |
|---|---|
| 106 / 105 | DONE — real resolver→plan (planHash persisted), install validates plan/expiry/digest, tx + `rolling_back` + per-op staging. **Caveat M3:** the daemon `install.execute` still has no `ArchiveSource`/`TreeSource`, so `request_install` cannot complete end-to-end — artifact-source wiring is **Phase 4**. |
| 102 | DONE — daemon calls `RecoverIncompleteOperations` before serving; now **halts** (exit `1`, `Fatal: startup recovery failed`) when an operation fails to recover (`cmd/litespm/main.go:1173-1175`) |
| 103 | DONE (partial) — provider start/stop wired, `provider.probe` real; `provider.invoke` resolves through `internal/discover`; `invocation.get`/`invocation.cancel` keep explicit `-32601` with concrete reasons. **Caveat m4:** `providers` rows are written by `internal/discover` on probe, but nothing persists them at install time → autostart on a fresh machine is still inert (Phase 4: persist provider rows on install). |
| 99 | DONE — vault open fail-closed; AuthBroker wiring deferred (no consumers → the broker stays `IMPLEMENTED`, `STATUS.md` §4). |
| 39 | DONE — real flag validation, honest synthetic-package label. |
| 104 | DONE — all six orphan handlers covered by real-IPC tests (`host.apply_setup` added). |
| 46 | DONE — repair skips in-flight staging; `Applied` truthful. |
| 98/119 connector | DONE (D1 **delete**) — zero importers proven; package + seeds removed (9 files, 2072 lines); docs corrected (`ARCH/29`, `README` counts, `ARCH/24` tombstone, `SECURITY.md`, `ARCH/00` = 21 modules). |
| 118 skills | DONE — `Installer`/`Update` (atomic swap + rollback + dry-run), ledger provenance (digest/ref/inventory), fixed `ParseSkillSource` pin parsing, policy hook, scope-checked `RemoveScoped`, removal guard; CLI wired (`skills update`, `--dry-run`, `--force`, scope). |
| 47 updater | DONE — real HTTPS release-manifest fetch, SHA-256 fail-closed, downgrade guard, O_EXCL private-dir atomic replace + Windows backup/swap, `VerifySelfBoot` wired, rollback; CLI wired (bounded download of `DownloadURLs[binName]`). Self-update completes once a release publishes `litespm-*` assets (packaging P7); **no signature check** (hash-only, `STATUS.md` §1). |
| 97 source | **DEFERRED → Phase 4** — analysis: honest wiring needs an upstream fetcher + `catalog build` command + CI publish + one ID scheme/web projection. Do NOT delete (D4 tension). Owner docs: [STATUS.md](STATUS.md) §2, `ARCH/03`, `ARCH/17`. |

### Wave 2 review defects

| ID | Defect | Status |
|---|---|---|
| M1 | `wrangler.toml` worker name vs live origin | SUPERSEDED (2026-10-05) — originally FIXED by keeping the **deployment** name `litepsm`; the repository later completed the coordinated rename to `litespm`: new Worker deployed and verified serving `rel-2026-10-05-01`, `wrangler.toml`/`DefaultRegistryURL`/`site.ts`/npm/docs updated in one change. The legacy `litepsm` Worker still serves the same frozen release until retired |
| M2 | Rename dropped legacy state/config/env/host-key (upgrade data loss, duplicate bridge) | FIXED — legacy root adoption (non-destructive), `LITEPSM_*` env fallback, legacy project config, host bridge-key adoption/removal; 17 hermetic tests |
| M3 | `install.execute` cannot complete (no artifact source) | **PARTIAL (updated 2026-10-06): skills and MCP servers now complete.** `install.execute` and the CLI route `kind=skill` to the skills ledger (`cmd/litespm/install_skill.go`) and `kind=mcp` to host-config registration (`cmd/litespm/install_mcp.go`), both behind the plan + human-approval gate in `install_authz.go`, verified against the live catalog; the synthetic package is removed. Only **plugin** listings still fail closed because the published catalog carries no artifact locator, and `internal/install/engine.go:204` still rejects a source-less `Execute`. The ARCH/17 §4 `ArtifactFetcher` is implemented and tested but has no production caller — wiring it to a catalog locator is the remaining piece. |
| m1 | dead `/explore/?sort=newest` link | FIXED |
| m2 | bridge rendered unknown status as "○ Stopped" | FIXED — `StatusUnknown` → "— Unknown" + test |
| m3 | startup recovery swallowed per-op failures | FIXED — returns error; daemon halts (exit `1`) + test |
| m4 | provider lifecycle unreachable (`providers` never populated) | OPEN → Phase 4 |
| m5 | tracker statuses stale | FIXED — pre-overhaul rewrite; re-checked line-by-line in Wave 3 below |
| m6 | recovery can orphan a partial CAS tree (cross-device) | OPEN → Phase 4 (fail-closed today) |
| m7 | `doctor` exited 0 on failures | FIXED — category-based exit codes (catalog 10 / resolve 20 / approval 30 / install 40 / provider 50 / host 60 / state 70, worst-wins, warnings 0) per ARCH/20 §2; tests bind |
| m8 | module path casing vs git remote (`LiteSPM`) | NOTE — non-breaking; remote is `git@github.com:sarv-projects/LiteSPM.git`, module is `github.com/sarv-projects/litespm`; align remote or casing at release |
| new | pre-existing `internal/bridge` `-race` flake (`bridge_test.go:61` ↔ `ipc/server.go:96`) | FIXED — `Serve` registers the accept loop under `s.mu` with a `stopped` guard; `Stop` waits after unlocking; `-race -count=50` clean, mutation reproduced the original race. Guard gap: `TestServerStopDoesNotRaceServeStartup` alone doesn't catch it (only the bridge integration test does) → strengthen in P1 (finding 10) |

## Wave 3 — Documentation overhaul (2026-10-05, uncommitted working tree) — `IN_PROGRESS`

| Item | Status |
|---|---|
| [STATUS.md](STATUS.md) created as the truth baseline; the six-state vocabulary from `ARCH/31` §2 is binding repo-wide | DONE (in tree) |
| `ARCH/31` (competitive landscape + ordered backlog) and `ARCH/32`–`ARCH/37` (the DESIGNED-only set: manifest/lock/interop, deployment ledger, invocation/receipts, profiles/leases, enterprise policy/audit/trust, TUI/completion) added | DONE (in tree, untracked) |
| Root documents re-read line by line and demoted to match STATUS.md — `README.md`, `AGENTS.md`, `TEST.md`, `SECURITY.md`, `TODO.md`, `npm/README.md`, this file | `IN_PROGRESS` — a five-part sweep is in flight; the per-file outcome is not asserted here (each file must either match STATUS.md or link to it) |
| `TODO.md`: blanket `Completed` retired for the six states, self-flagged overclaims corrected (C002/C004, C003, D003, E005, F004, G002/G003, H003), Phase I table added, Overhaul backlog maps `ARCH/32`–`ARCH/37` to next-phase work | DONE (this pass) |
| New `scripts/README.md` and `web/README.md` documenting every script and the site build, including which are CI-wired, manual-only, or assertion-less | DONE (this pass) |
| False legacy claims corrected wherever they appeared: wizard performs no HTTP fetch; no non-interactive `setup <agent>`; no crash-injection harness; no `adopt_tool` / `LPSM-HOST-READONLY-EXTERNAL`; no `--catalog-url` (use `LITESPM_REGISTRY_URL`); test package is `test/`, not `tests/e2e/`; catalog = 5,814 items; Go 1.26 | DONE for the files in this pass |
| Toolchain re-run after doc edits (`gofmt -l`, `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, web `npx tsc --noEmit`) | DONE for this pass (2026-10-05: gofmt clean, build/vet ok, 23/23 packages ok, `tsc` exit 0); re-run once more after the remaining doc parts land, before any commit |

## Later phases (open; STATUS.md is authoritative per capability)

The finding numbers below are the audit's own numbering. Some rows in these lists have already
landed as part of the Phase 0–3 remediation (CI static gates, `LICENSE`/`NOTICE`, `CHANGELOG`,
provider start/stop, skills lifecycle, self-update); they are not re-numbered here — **P9** is the
re-audit gate. Nothing in these phases is `WIRED` unless [STATUS.md](STATUS.md) says so.

- **P1** Security fail-open: 12, 11, 8, 100/16/15, 17-21, 120, 60-63, 78, 50, 49, 2, 7, 10, 13 — owner docs `ARCH/05`, and supply-chain signing items in [`ARCH/36`](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) (`ARCH/31` §12 phase 5)
- **P4** Data/state + deferred wiring: 25, 68-74, 107-109, 138-139, 52-57, 59, 75, 129, 130, 147; plus 97 source pipeline, artifact-source install (`M3`), provider persistence (`m4`), partial-CAS recovery (`m6`) — owner docs [`ARCH/33`](ARCH/33-DEPLOYMENT-LEDGER-RECONCILIATION.md) (ledger/reconcile), [`ARCH/34`](ARCH/34-RUNTIME-INVOCATION-RECEIPTS.md) (capability + provider rows, invocation), `ARCH/12` (recovery), `ARCH/03`/`ARCH/17` (source)
- **P5** Host/config: 32-38, 64-67, 116, 117, 141, 148 — owner docs `ARCH/16`, [`ARCH/33`](ARCH/33-DEPLOYMENT-LEDGER-RECONCILIATION.md) (ownership records + `adopt`)
- **P6** CLI: 79-81, 44, 121, 153, 76, 77 — owner doc [`ARCH/37`](ARCH/37-TUI-AND-COMPLETION.md) (TUI, completion, `why`)
- **P7** Packaging/CI/web: 51, 91, 92, 94, 95, 125, 127, 82-90, 145, 146, 156, 87, 90 — owner docs `ARCH/18` (one builder, D4), [`ARCH/36`](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) §4–§5 (signed releases, SBOM)
- **P8** Docs/dead code: 132-135, 140, 143, 144, 149-152, 155, 159, 122, 123 — owner docs `ARCH/00`, [`ARCH/31`](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) §13 (forbidden claims)
- **P9** Full re-audit: 0 ❌, ≤5 🟡, toolchain green

**Design-only work** (no code, `DESIGNED` everywhere) lives in `ARCH/32`–`ARCH/37` and is mapped
row by row in the **Overhaul backlog** section of [TODO.md](TODO.md): manifest + lockfile + frozen
install (`ARCH/32`), deployment ledger + three-way reconcile + `adopt` (`ARCH/33`), invocation
engine + receipts (`ARCH/34`), profiles + capability leases (`ARCH/35`), policy hierarchy +
`audit --ci`/SARIF + advisories/quarantine + TUF/SBOM/SLSA + air-gap bundles (`ARCH/36`), TUI +
dashboard + shell completion + `why` (`ARCH/37`). Identity graph and interop are `ARCH/32` §5–§6.

## Verification protocol (every wave)

1. Implementer runs `gofmt -l`, `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, package-scoped `-race`.
2. Independent verifier (did NOT implement) attempts to **falsify** each claim, with mutation tests in `/tmp`.
3. Diff review for regressions/security.
4. Status flips to `VERIFIED` only via the verifier.
