# Current Run Handover

## Active Goal

Resume the guarded `feed:skills-sh` source-only replay and rebuild only from reviewed, complete producer output; separately review/fix website UI defects. Preserve producer-owned data, source snapshots, the pre-session stash, and the CLI sandbox. Keep all changes unstaged, uncommitted, and unpublished.

## Repository State at Audit Close

- Repository: `/home/sarvesh/business_Dev/liteSPM`.
- HEAD and `origin/main`: `57a5ff950d2c8fb5a8fa9583a4edb5624cfa0811` (rechecked after the end-to-end pass; aligned).
- Preserve producer-owned dirty paths; do not stage, overwrite, or dump the generated catalog diff:
  - Modified: `internal/catalogbuild/dataset_test.go`, `scripts/build_full_catalog.py`, `web/data/catalog.json`, `web/data/release.json`, `web/lib/telemetry.ts`.
  - Untracked: `scripts/test_snapshot_lifecycle.py`.
- Before remediation, `CURRENT_RUN.md` was the only audit-authored repository file; no product code was changed during the audit. Remediation orchestration has since added `.gitignore`/`.ignore` entries and `.slim/deepwork/fix-audit-findings.md`. Product changes from the active lanes are not yet reconciled.
- **Audit side effect:** the npm wrapper test built into pre-existing, gitignored `dist/litespm-linux-amd64`, overwriting that local binary. The directory is not tracked by HEAD. Its prior bytes were not backed up; the local checksum manifest currently does not match that rebuilt binary. This is not evidence of a published-artifact problem.
- Root `AGENTS.md`, `ARCH/00-INDEX.md`, `TODO.md`, and `STATUS.md` were read. `STATUS.md` is the subsystem-state authority; distinguish implemented behavior, documented design, and dirty-tree-only behavior.

## Reviewers (All Completed; Read-Only)

- Architecture/contracts: `ses_ee7fb1d99ffeRgCGegmH0hl7eI`.
- CLI/core/security: `ses_ee7fb1d6fffesx9krGMmzza8oE`.
- Website/UI: `ses_ee7fb1d55ffejwDdY3XorpQGcx`.
- Ingestion/release/supply chain: `ses_ee7fb1d3affexGAMxe4JLdjLNq`.

Their findings were reconciled with local source where noted below. Reports were static unless explicitly described. Do not describe unverified risks as runtime-reproduced.

## Highest-Priority Findings to Report

1. **High — release materialization reuses an immutable release identity for changed inputs.** `cmd/litespm/main.go:1219-1319` reuses the previous pointer's release ID, sequence, and time in `--materialize` without comparing the newly compiled manifest digest to `prev.ManifestDigest`. Reproduced: the clean HEAD dataset materialized byte-for-byte to the committed `rel-2026-10-07-01` tree; the dirty 46,166-row dataset also materialized under that same ID/sequence/time with a different manifest digest, and `--verify` accepted the newly generated tree because it verifies that tree against its own pointer. The code path can therefore emit changed bytes under a CDN-immutable identity; the committed-input case itself reproduces correctly.
2. **High — explicit policy deny is bypassed by MCP install.** Reproduced end-to-end with a local synthetic catalog and isolated home/data root: adding `{"effect":"package.install"}` to `policy-deny.json` did not stop `litespm install mcp:acme:demo-server --host opencode`; the command returned success and wrote the server entry. A focused test in `/tmp/opencode/head-check` confirmed the same policy engine returns `deny` for that operation, while `installMCPFromListing` still succeeds. The existing skills policy test denies the same effect, confirming an MCP/skills enforcement asymmetry. `ARCH/15-POLICY-APPROVALS.md:73-84` makes explicit deny non-overridable.
3. **High — MCP install approval does not bind the exact host target set (and may not bind the command/runtime descriptor).** Plan creation captures registered hosts (`cmd/litespm/main.go:2596-2628`), but execution calls `installMCPFromListing(..., nil, ...)` (`main.go:2023-2028`); `cmd/litespm/install_mcp.go:117-124` re-resolves current registered hosts when empty. `install_authz.go:100-169` checks persisted plan identity/hash/expiry and listing/scope/version but does not revalidate hosts. Architecture review also found empty `HostChange.ValueJSON` / unpopulated provider launch records and approval prompt omits command/targets (`install_authz.go:310-316`). A target or descriptor change between approval and execution may exceed approved effects. Static finding; not reproduced.
4. **High — catalog ingestion fetches are insufficiently constrained.** `scripts/build_full_catalog.py:318-340` `_http_get` reads response bodies without a byte cap and follows default redirects; sitemap-derived URLs have substring checks rather than strict host/IP validation (dirty/current script paths around `:1240-1247,1334-1342`). `scripts/snapshot_store.py:106-120` requires HTTPS but does not host/IP allowlist. Subagent noted `internal/catalog/client.go:107-154` similarly accepts arbitrary HTTPS hosts and blocks downgrade/hop excess but not private destinations. Threat includes SSRF to private services and unbounded response consumption; source-derived, not dynamically tested.
5. **High/Medium — dirty producer dataset is incoherent with the checked-in release tree.** HEAD and the live origin contain 5,825 rows; dirty `web/data/catalog.json` has 46,166, and dirty `web/data/release.json` describes the larger dataset while checked-in `web/public/v1/current.json` still names the 5,825-row release `rel-2026-10-07-01`, sequence 145. Building from the dirty dataset under that identity was reproduced. This is local working-tree evidence, not a claim that the live origin is incoherent. Do not publish the dirty data until the release is deliberately cut and all artifacts agree.

## Other Material Findings

- **Source provenance is lost across Go release compilation.** Producer adds explicit row `source` IDs, but `internal/catalogbuild/dataset.go:31-48` has no `Source`; `convertRow` (`:172-227`) infers `SourceID` from the second colon-separated listing-ID component. `DatasetSnapshotID` (`:131-137`) is synthetic from whole-dataset bytes, not the actual fetch snapshot ID. Source row cannot reliably trace to its upstream feed/fetch snapshot.
- **Stale/partial ingestion lacks structured completeness state.** `obtain()` (`scripts/build_full_catalog.py:356-424`) returns prior bytes after refresh failure with a STALE log; snapshot store may preserve healthy state when prior content/count remains. Output may not expose stale/completeness. Current dirty script supports source skipping; treat that as dirty-only unless comparing against HEAD.
- **Snapshot store/history/schema concerns.** `scripts/snapshot_store.py` keys by URL and replaces old content for a URL (`:172-230`) rather than immutable historical snapshots; docs say source schema matches snapshot record, but actual record includes `sourceId` while `schemas/source.schema.json` disallows extras and omits it. Verify schema version/consumer before treating compatibility impact as proven.
- **Schema drift:** release ID schema pattern differs from Go-generated `rel-YYYY-MM-DD-NN`; listing schema requires `sourceType`/`manifestFormat` while `domain.SourceReference` exposes `sourceId`/`upstreamId`/`url`. No actual JSON-schema validation in the release path was found by reviewers.
- **The current live release is searchable but not installable:** `catalog sync` from the live origin succeeded and indexed 5,825 rows. All 4,079 MCP and 1,108 skill rows are `discovery_only`; the 638 plugins are `metadata_verified`, but the synced `versions.json` has zero `runtime.command` entries. Search works; `prepare_install` fails with no versions, and both MCP and skill CLI installs fail closed as not installable. The live package page nevertheless shows an `litespm install skill:anthropics:docx` command, which the CLI rejects with `LPSM-NOT-INSTALLABLE`.
- **CLI/core:** import uses a manifest snapshot loaded before interactive approval and writes via direct `os.WriteFile` (`cmd/litespm/import.go:202-260,313-325,418-436`), risking lost concurrent edits/truncation; MCP host config install has read/merge/write without compare-and-swap (`internal/host/entry_install.go:401-483`); install compensation does not survive process death (documented in `internal/lifecycle/txn.go:19-26`); `install --workspace` is parsed but not consumed (`main.go:925-999,1115-1148`); capabilities `--host`/flag ordering mismatch docs (`capabilities.go:22-33,72-87,123-124,241-265`); self-update binary download lacks deadline (`main.go:261,311-316`, unlike updater manifest 30s timeout); SemVer parser accepts malformed forms (`internal/resolver/semver.go:35-90`).
- **Dependency metadata claim is not a confirmed defect.** Production sync does load `versions.json` as `[]*domain.VersionRecord` and calls `SearchIndex.Replace` (`internal/catalog/client.go:229-243`); however `internal/catalogbuild/dataset.go:244-250` constructs current producer VersionRecords with empty dependencies by design, while CLI resolver adapter only copies version strings (`main.go:2543-2565`). Classify as current product/data-model limitation or latent capability gap only after checking real release version records and resolver contract; do not overstate.
- **UI:** Live browser search for `postgres` rendered 82/5,825 rows. Broad query `a` rendered **5,659 result cards** with no window/pagination (DOM text was ~1.05 MB); `SearchResults` places the entire list inside `aria-live="polite"` (`HomeView.tsx:97-100,134-145`; `SearchResults.tsx:51,90-97`; `search.worker.ts:123-137`). On a live package page, five of six inactive tabs referenced nonexistent `aria-controls` targets; clicking a tab replaces the one `tabpanel`, so all remaining inactive references stay dangling (`PackageTabs.tsx:554-584`). Other candidates: loading/deferred-query mismatch; search keyboard shortcuts only on Home/Explore; mobile Categories reserves hidden 130px third grid track. Dirty registry rows are labeled vendor-listed despite registry source being distinct from publisher marketplace provenance (dirty producer output only).
- **Docs drift:** `ARCH/25` theme/kind-tab contract inconsistencies; architecture docs misstate `catalog build`/artifact fetcher, MCP install needs, HTTPS redirect controls, self-update verification, IPC peer authentication. Reconcile with STATUS and preserve documented open gaps.
- **Web dependency audit:** `npm audit --json` in the clean HEAD copy reported 10 affected packages (2 moderate, 7 high, 1 critical). Direct `next@15.1.7` is in npm's critical range with many advisories; npm reports `15.5.27` as a non-major fix. Direct `tailwindcss@3.4.17` is also flagged high, with the suggested fix requiring a major update to 4.x. The site is exported as static files, so do not imply Next server-only advisories are directly exploitable on the deployed static origin; the build dependency is nevertheless stale/vulnerable, and CI has no `npm audit` gate. SBOM is uploaded without detached signature/checksum entry; installer checksum behavior remains as previously reviewed.

## Website/UI Positive Evidence and Limits

- Static export wired: Next 15.1.7 `output:"export"`, trailing slashes, static slices plus full `/data/catalog.json`, lazy full-data client fetch, worker fallback. Theme/reduced-motion, skip link, focus styles, labeled search/mobile controls exist.
- `npx tsc --noEmit` passed in the current checkout. In the isolated clean HEAD copy, `npm ci && npm run build` and full `scripts/deploy-pages.sh` passed, including materialization, digest verification, and strict Pages bundle allowlist/leak audit. The build warned about the 10 npm audit findings above. Live browser navigation/search/package detail were exercised; this proves the current live origin behavior, not every browser/OS combination. No dedicated web test script exists.

## Tooling, Verification, and Limitations

- Git/GitHub MCP confirmed local/remote HEAD alignment and remote STATUS; DeepWiki has no repository index. `agent-browser` CLI unavailable; Chrome DevTools browser MCP was used for the live site.
- Optional codebase indexer dependencies (`tree-sitter-language-pack`, `pathspec`) were absent; no package install and no `.code-intelligence/` index was generated.
- Verification executed: current checkout `gofmt -l .`, `go vet ./...`, `bash -n scripts/*.sh`, `python3 -m py_compile scripts/*.py`, Node syntax checks, `cd npm && npm test`, `python3 scripts/build_full_catalog.py --check`, `python3 scripts/snapshot_store.py` (26 checks), untracked producer `python3 scripts/test_snapshot_lifecycle.py` (15 checks), `npx tsc --noEmit`, and `git diff --check` passed. Current dirty-tree `go test -race -count=1 ./...` fails only `TestCatalogRelease_EndToEnd`: dirty release listings are 40,939,053 bytes and versions 21,978,015 bytes, exceeding `internal/catalog`'s 16 MiB response limit. In a clean archive of HEAD, `go test -race -count=1 ./...` passed all packages. The clean copy's web production build and deploy bundle audit also passed.
- Live origin `/v1/current.json`, manifest, listings, and versions all returned HTTP 200 and the pointer matched checked-in HEAD. Live `catalog sync`, search, browser rendering, and isolated daemon/Bridge JSON-RPC calls were exercised. Bridge initialized and exposed 12 tools; search worked; `get_invocation`/`cancel_invocation` returned their documented `-32601` not-implemented errors. Daemon testing used a temporary `secret-tool` shim under `/tmp` because the environment has no native Secret Service; no OS/user configuration was changed.
- Dynamic tests reproduced MCP deny bypass and changed-input materialization as above. The approval/host-target binding gap remains source-derived only; no host-target mutation test, SSRF probe, crash injection, Windows/macOS runtime acceptance, or real live-catalog install success was performed (the live release has no installable rows).
- All findings are candidates prioritized by severity and evidence, not an assertion that every risk was reproduced. Separate committed HEAD issues from dirty producer observations and from docs-only drift.

## Website Link-Breakage Fix (2026-10-08 evening)

User reported: "check the website, the links not working."

### Root cause (found via two-browser reproduction + dev-mode `pageerror` capture)

`web/app/HomeView.tsx:97` / `web/app/explore/ExploreView.tsx:158` passed `full ?? []` into
`useCatalogSearch` — before the catalog loads, `full` is `null`, so **`[]` was a fresh array every
render**. That churned the hook's drive-effect deps (`[listings, ...]`) every render, and its
`!worker` branch (`web/lib/useCatalogSearch.ts`) called `setResults(new array)` **without the
`lastKeyRef` guard the fallback branch has** → infinite `setState`-in-effect loop → React logged
`Maximum update depth exceeded` forever, which **starved the router's transition so every
client-side `<Link>` navigation silently never committed** (no fetch when prefetched, no
`pushState`, no error — dead links). Typing in search loaded the catalog (`full` became stable),
the loop stopped, and navigation flushed — matching every observed behavior (links "worked" only
after typing or from `/package/`).

### Fix (uncommitted, on top of `29643c3`)

`web/lib/useCatalogSearch.ts` only:
1. Normalizes empty row sets to one shared `EMPTY_LISTINGS` identity (kills dep churn from any caller).
2. Key-gates the `!worker` branch with `lastKeyRef` exactly like the fallback branch (kills the unguarded `setResults`).

Call sites unchanged; `npx tsc --noEmit` passes.

### Evidence

- **Dev** (`next dev`): full click-through — nav Explore/Agents/Categories/Coverage, footer, card→`/package/?slug=agent-rail`, logo→home — all navigate; 0 update-depth errors.
- **Prod** (fresh `npm run build` static export served on :8795): same 7/7 click-through navigates; **0 console errors**; search still returns "Showing 24 of 46,166 results".
- Reproduced BEFORE the fix in **both** chrome-devtools and Playwright Chromium; dev `pageerror` capture showed `Maximum update depth exceeded` ×40+ plus a `useId` hydration warning.
- The `useId` hydration warning (`_R_…` id mismatch on `SearchBar`, `/` and `/explore/`) appears in **dev only** (0 errors in prod) and matches the Next 15.5 known-issue class (radix-ui/primitives#3700); `SearchBar` is the app's only `useId`. Cosmetic in dev; no prod impact. Not fixed — documented.

### Working-tree note (IMPORTANT)

During the 18:45 server restart the tree was committed by an external actor: **`29643c3 "Commit"`
(sarvesh, 2026-10-08 17:55, 120 files, +914,387)** contains ALL session work (remediation + waves
1-5 + B1 + docs). Not an agent action — nothing was rewritten. Current dirty state is exactly:
`web/lib/useCatalogSearch.ts` (this fix) + `web/next-env.d.ts` (build artifact, restore after the
final web build) + this handover update. A **pre-session stash** `stash@{0} "WIP on main: 927e244
commit"` (yesterday 04:29, zero session markers) exists and was deliberately left untouched.

## Final Next Steps

1. **Producer rebuild is running** as detached PID `2456`; log: `/tmp/opencode/rebuild.log`. Last observed in replay mode at `skills.sh: 500/19,388 processed (384 added)`, after 764/1,114 skill rows had been verified/promoted. The process may have progressed since that observation. It will then enrich remaining feeds and crawl mcpmarket (≤2,000 pages/run, 1 s crawl delay, robots-permitted paths only). Wait for the completion notification from watcher `sh_11cf03444001V0ABq3JIDQxSHo`; then inspect final ingestion completeness/stale status, skill verification totals, endpoint-drop counts, and source rows before cutting a release.
2. **Release cut** (explicitly authorized): `go run ./cmd/litespm catalog build` from the completed producer dataset; verify the resulting release ID/sequence/digests and synced `web/data/release.json` + `web/public/v1/current.json`, then run `go run ./cmd/litespm catalog build --verify web/public`. Expected next sequence is 146 from the current 145 pointer, but confirm the pointer at execution time rather than assume it.
3. Run the final full Go/Python checks and web type-check/build after the dataset lands. Serve the final export on a fresh port and click-test routes plus promoted skill/remote-MCP pages. Restore only the generated `web/next-env.d.ts` route-types line after the last build. Leave the existing commit untouched; keep subsequent changes unstaged, uncommitted, and unpublished as requested.
4. If catalog sync must work behind an egress proxy, revisit the deliberate proxy bypass in `internal/catalog/client.go` (see Closeout decisions).
5. Open follow-ups recorded below: private-literal LAN endpoint opt-in (plan-time refusal has no consent flag), ingest-path `metadata_verified` alignment for remote-only records, `mcpmarket` accumulation across runs, residual shim.go line-cite drift in older docs.

## Limitations-Fixing Waves (2026-10-08, after closeout)

User selected ALL five open limitations (docs drift, release cut, installability metadata, mcpmarket source, snapshot history) and explicitly approved **implementing remote (URL) MCP install** when the bucket-(b) scope question was surfaced.

### Waves 1-2 (parallel lanes, all reconciled)

- **Docs drift**: `ARCH/00,01,03,04,05,06,07,08,09,10,11,17,22,25,36` + `STATUS.md` reconciled against source (catalog-client egress now enforced, self-update bounded+checksummed, `catalog build`/artifact-fetcher status, ARCH/25 internal contradictions).
- **Snapshot history**: `scripts/snapshot_store.py` now archives every superseded fetch under `history/<key>/<entry>/` (hard-linked, write-once, retention ≤8 entries/32 MiB per URL, prune-before-add fail-closed, lazy legacy migration). Self-test 50 checks, lifecycle 23 checks.
- **mcpmarket.com source**: integrated producer-side (`feed:mcpmarket-com` in `build_full_catalog.py` + `internal/source/sources.go`); robots.txt permits `/` (API disallowed, never used), sitemap patterns strict-fullmatch, exact-host allowlist (apex+www), per-run fetch budget `MCPMARKET_DEFAULT_MAX_PAGE_FETCHES=2000` @ 1 s delay. New suite: 105 checks.
- **Installability research** → `.slim/deepwork/installability-research.md` (681 lines): kind×source matrix, three buckets (wire-now / needs-consumer / fail-closed-forever).
- **A2 version-less resolution**: resolver owns the `discovery` implicit pin (`ImplicitVersion`); wildcards/exact-pin only, real ranges still fail closed; nothing synthesized into plan hash. `ARCH/13` §1.2 + `STATUS.md` note; real release row exercised in `plan_fields_test`.
- **A1+A3 skill promotion**: producer promotes a skill row to `metadata_verified` ONLY after a genuine build-time `SKILL.md` read (frontmatter name+description, sanitizer, mismatch gate, ≤3 probes depth ≤2); officialskills.sh pages adopt the published GitHub tree link as `skillSource`; askills slug self-collision fixed; enrichment instead of first-wins discard. New suite: 186 checks. **Promotion counts land with the rebuild.**
- **B2 registry schema**: `internal/source/mcp_registry.go` + `internal/interop` aligned to the live server.json schema (verbatim live fixtures), `litespm import` of real registry docs now produces a preview plan instead of `LPSM-IMPORT-004`; negative tests pin that no command is ever derived from a coordinate.

### B1 remote (URL) MCP install — Phases 0-5, all implemented

- **Phase 0** `internal/egress` extracted from `internal/catalog/client.go` (policy: https-only + loopback-http, no URL credentials, ≤3 redirects, same-origin for remote policy, checked-IP dial, proxy off, `DialTLSContext` cleared, link-local/metadata/CGNAT refused unconditionally, RFC1918 behind `AllowPrivate`); catalog delegates; `mcpclient` nil/caller clients wrapped fail-closed. 16 egress + 4 guard tests.
- **Phase 1** producer reads `remotes[]` (first `streamable-http` else first; drops logged; non-https sanitized → stays `discovery_only`); `DatasetRow.url` → `RuntimeDescriptor.Endpoint`; `ParseDataset` requires `Transport && (Command || URL)` + rejects `discovery_only`+`url`; `RuntimeForListing` accepts endpoint-only components. Suite: `test_remote_endpoints.py` 77 checks.
- **Phase 2** `RemoteEntrySpec` + `BridgeTarget.Remote`; `ServerEntry.Endpoint`; golden JSON/TOML entries for the 8 verified-capable hosts (claude-code, cline, opencode, codex, grok-build, pi-agent, cursor, zed); 8/42 matrix test (mass-enable fails); read-back transports both shapes; `--env` refused (`LPSM-REMOTE-ENV-REFUSED`); both-set refused; copy/porting wire endpoint + refuse incapable targets (`LPSM-COPY-002`); `hosts.json` gains `remote:true` ×8; `web/lib/hosts.ts` optional field.
- **Phase 3** plan-time `egress.CheckURL` + literal-IP classification (private literals refused, no opt-in flag yet); capability-filtered host sets with typed drop list; sealed-plan replay validates endpoint; `entryDigest` separates stdio vs remote; approval prompt prints a mandatory `endpoint : "https://…" (transport)` line; `install.execute` result carries `endpoint`+`transport`. Decision 6 honored (no targeted `network.outbound` policy input). E2E: plan→approve→execute→golden entry→ledger→remove→byte-identical restore on JSON + TOML hosts.
- **Phase 4** `discover` remote dial via `dialRemote` → `ConnectStreamableHTTP`/`ConnectLegacy` on guarded clients; typed `LPSM-PROVIDER-REMOTE-AUTH`/`-CONNECT`; zero rows on failure (Decision 8: unconnected health stays `— Unknown`); `state` `launchSpec.endpoint` (no migration, `mode=remote-http`/`legacy-sse`); capabilities prints the endpoint it dials. Fixtures: stateless 2026-07-28 wire shape (0 `initialize`) + legacy handshake (1 `initialize`), 401 → typed auth + zero rows, `169.254.169.254` refused pre-dial.
- **Phase 5** docs: `ARCH/05` §3.1/§3.2 corrected (egress enforced; genuinely-open bullets remain), `ARCH/07` D-030/D-031 recorded, `ARCH/16` §3.7.4 remote-entry model, `ARCH/01/03/08/09/14/20/00` re-cited, `STATUS.md` §3 rows updated, `TODO.md` LPSM-A008/F001, migrate-script docstring+print. (No `SPEC-CHANGELOG.md` exists here; ARCH/07 is the normative ADR record per `ARCH/00-INDEX`.)

### Verification after Waves 1-5

`go test ./... -count=1` **TEST_EXIT=0 (42 packages)** · `go vet`/`gofmt`/`git diff --check` clean · Python: `--check` OK + 77/31/186/105/23/50 checks · `cd web && npx tsc --noEmit` OK · combined re-run green after every wave.

### Decisions & gotchas (this phase)

- Bucket (c) unchanged: awesome-list MCP claims, registry coordinates without observed start, plugins without artifact locators, 7 non-git-host skills — all stay fail-closed forever.
- Remote rows classify `metadata_verified` from the publisher's own `remotes[]`; **no install-time probe** (Decision 7a); `runtime_verified` still means observed start.
- Plan-time policy: `Policy{AllowLoopbackHTTP:true, SameOriginRedirects:true, MaxRedirects:3}`, `AllowPrivate:false` → literal private-IP LAN endpoints refused at plan time (hostname-based LAN endpoints pass plan-time by design — no DNS at plan — and are checked at dial).
- Two interrupted lanes (server restart) were gap-audited and completed by a dedicated B1-GAP lane; all their required tests now exist.
- The engram memory MCP server dropped mid-session — handover lives entirely in this file.

## Active Remediation Lanes (all returned and reconciled)

- CLI/security and release identity: `ses_ee56fc947ffe5K5ipTieNPmCNb` — done, reviewed, tested.
- Registry/fetch safety: `ses_ee56fa6caffeliNSjnOfbFFacd` — done, reviewed, tested; producer hunks preserved.
- Website behavior: `ses_ee56fb9ebffey9J4D73iCd5438` — done, reviewed, browser-verified twice (pre- and post-Tailwind).
- Dependency security: `ses_ee56f988bffeVDVdsb0omxjSEN` — Next 15.5.27 + PostCSS override; superseded by the authorized Tailwind v4 migration from the website lane.
- Verification/coordination state: `.slim/deepwork/fix-audit-findings.md`. No Oracle reviewer is available; independent review and executable regression evidence are the substitute gate.

## Remediation Closeout (2026-10-08)

### Changed (all uncommitted; nothing staged)

- **Authorization/release identity:** `cmd/litespm/install_authz.go`, `install_mcp.go`, `main.go`, `catalog_build_test.go`, `plan_fields_test.go` and related tests. Explicit `package.install` denies now stop MCP installs; the approved plan seals the exact host set, config paths, runtime descriptor, env names and force flag in each `HostChange.ValueJSON`, execution reads only the sealed plan (`LPSM-PLAN-STALE` if a target moved or the catalog changed), and `--materialize` refuses a changed manifest digest instead of reusing the prior immutable release identity.
- **Config-write concurrency:** `internal/host/backup.go`, `entry_install.go`, `entry_state.go`; `cmd/litespm/import.go`. Cross-process config lock, compare-before-replace writes, rollback that refuses to clobber edits made after the install, and an import snapshot/lock so a manifest edited between preview and approval is never overwritten.
- **Fetch/ingestion boundary:** `scripts/build_full_catalog.py` (on top of the producer's own hunks), `internal/catalog/client.go`, `internal/catalogbuild/dataset.go`, plus new `scripts/test_catalog_ingestion_safety.py` and `internal/catalog/{client_body_test,client_egress_test}.go`. Per-source host allowlist, HTTPS/port/credential refusal, DNS→public-IP validation pinned to the dialled address, redirect re-validation, 16 MiB bounded source responses, structured stale/incomplete run status, registry pagination/status hardening, and public-IP-checked catalog dials. Provenance: a row's `sourceSnapshotId` is only copied when the producer supplied a real one; the whole-dataset fingerprint is no longer stamped onto rows as if it were an upstream snapshot.
- **Website:** `SearchResults.tsx` (24 cards + "Show more", count-only live region), `PackageTabs.tsx` (all six panels mounted, `aria-controls` always resolves), `AddSteps.tsx` (fail-closed messaging, no `litespm install <id>` for discovery-only/unclassified/plugin rows).
- **Dependencies:** `web/package.json`, `web/package-lock.json`, `web/postcss.config.js`, `web/app/globals.css` — Next 15.1.7→15.5.27, Tailwind v3→4.3.3 (`@tailwindcss/postcss`, legacy config kept via `@config`), autoprefixer dropped (v4 handles prefixes). `npm audit` 10 findings (1 critical) → 0.
- **Other CLI candidates:** `capabilities.go` flag/command validation, strict SemVer parsing (`internal/resolver/semver.go`), self-update download deadline, `--workspace` rejected as unimplemented (docs updated in `README.md`, `ARCH/01`, `ARCH/04`).
- **Test-only flake fix:** `internal/catalog/integrity_test.go` pins `compileSampleRelease`'s `createdAt`.

### Verification (run in this tree unless noted)

- `go test ./... -count=1` — pass, all packages. The dirty 46,166-row `TestCatalogRelease_EndToEnd` now passes because manifest-declared listings/versions are bounded at 64 MiB instead of the 16 MiB metadata cap (previously the audit's dirty-tree failure).
- `go test -race ./cmd/litespm ./internal/host ./internal/resolver ./internal/catalog -count=1` — pass; **full `go test -race ./... -count=1` — pass, all packages** (including `internal/catalogbuild` 288s, `test` 248s, `cmd/litespm` 170s).
- `go vet ./...`, `gofmt -l cmd internal test`, `git diff --check` — clean.
- Python: `py_compile`, `build_full_catalog.py --check` (CHECK OK), `test_catalog_ingestion_safety.py` (31 checks), producer `test_snapshot_lifecycle.py` (15), `snapshot_store.py` self-test (26) — pass.
- Isolated web copy (`/tmp/opencode/litespm-web-final.hKCZ33`, never shared `node_modules`): `npm ci` → 0 vulnerabilities, `npm audit` → 0 advisories, `npx tsc --noEmit`, `npm run build` static export — pass.
- Independent browser check of that build (Chrome DevTools MCP): query `a` → "Showing 24 of 39,643 results · 46,166 entries", "Show 24 more" → 48; package page → 6 tabs / 6 panels, all `aria-controls` resolve, exactly one visible panel; discovery-only entry shows `LPSM-NOT-INSTALLABLE` copy with zero item install commands; console 0 errors, 4 known font-preload warnings; screenshot `/tmp/opencode/pkg-page-tailwind4.png` shows the design intact.
- `cd npm && npm test` — all wrapper tests pass (no `dist/` binary was rebuilt this time).

### Decisions & gotchas

- **Catalog client bypasses environment proxies** (`clone.Proxy = nil`): a proxy would resolve destinations outside the new IP pinning. Deliberate fail-closed trade-off — proxy-dependent environments lose proxied catalog sync until a reviewed proxy-aware design exists.
- **Redirect cap is now 3 hops**: the code previously refused at 3 (`>= 3`) while `ARCH/03 §5` documents a cap of 3; the fix aligns code with the documented bound, so one extra redirect is permitted versus HEAD.
- **`internal/catalogbuild/dataset_test.go`**: only HEAD's synthetic-snapshot assertions (`snap-deadbeef`) were updated; the producer's K002 `source`-provenance hunk is intact.
- **The pre-existing `TestSyncUnavailableVerifierRespectsRequireFlag` flake**: `pointerFor` and `signedReleaseFiles` compile the same fixture independently, `CompileRelease` stamps `createdAt` at RFC3339 *second* precision, so a wall-clock second tick between the two compiles made the digest chain fail once during a full-suite run. Fixture time is now pinned; `-count=10` passes.
- Nothing was staged or committed; producer-owned `web/data/*`, `web/lib/telemetry.ts`, and their hunks in `scripts/build_full_catalog.py` are untouched by closeout.

### Not changed (open by decision, not by oversight)

- Dirty dataset vs checked-in release identity (finding 5): needs a deliberate release cut; `catalog build --materialize` now *refuses* to paper over it.
- No installable MCP/skill runtime metadata exists upstream; every path stays fail-closed and no launch facts were synthesized.
- `mcpmarket.com` was not added as a source (user informed; no explicit instruction to add it).
- `scripts/snapshot_store.py` untouched: it performs no network fetches, so the fetch policy belongs in the producer (now enforced there); its one-snapshot-per-URL replacement behavior and the broader docs drift remain open.

## Source Inventory Check

- Producer working tree integrates `mcpservers.org` and `skills.sh`; the dirty generated catalog has 255 rows with `source=feed:mcpservers-org` and zero rows with `source=feed:skills-sh` (all observed skills.sh candidates deduplicate against earlier rows). Neither integration is in the checked-in release yet.
- `mcpmarket.com` is absent from `scripts/build_full_catalog.py`, `internal/source/sources.go`, and both current dataset/release. User asked whether it was added; answered that it was not, and did not expand remediation to add a new source without explicit confirmation.

## Tailwind v4 Migration Handover (2026-10-08)

### Active Goal

- Complete the authorized LiteSPM website Tailwind v3-to-v4 migration while preserving the existing search, package-tab, and installability UI fixes and producer-owned data/telemetry work.

### Where We Stopped

- Updated `web/package.json`, `web/package-lock.json`, `web/postcss.config.js`, and `web/app/globals.css` for Tailwind CSS 4.3.3 and `@tailwindcss/postcss`.
- Kept `web/tailwind.config.js` and the existing `@layer components` CSS; `@config` compatibility loads the legacy design tokens. Added v3-compatible default border color and changed only utilities whose v4 equivalents differ.
- Updated migration-required utility classes in `web/app/explore/ExploreView.tsx`, `web/app/package/PackageView.tsx`, `web/components/catalog/CapabilityCard.tsx`, `web/components/catalog/ExtensionGrid.tsx`, `web/components/hero/HeroSection.tsx`, `web/components/home/LandingHero.tsx`, `web/components/layout/SiteFooter.tsx`, `web/components/navigation/SearchBar.tsx`, and `web/components/package/PackageTabs.tsx`.
- Verified `npm ci` (zero vulnerabilities), `npm audit --json` (zero advisories), `npx tsc --noEmit`, `npm run build` (static export), and `git diff --check`.
- Browser-tested the built export: query `a` showed 24 of 39,643 matches, then 48 after Show more; all six tabs referenced existing panels and only the selected panel was visible; discovery-only and plugin entries displayed their correct fail-closed errors without LiteSPM item-install commands. Desktop screenshot/DOM comparison preserved layout; 390px viewport had no horizontal overflow.
- `web/next-env.d.ts` was restored after Next's build generated a route-types reference; it is not part of the migration diff. Producer-owned catalog/release JSON and telemetry were not edited. No files were staged or committed.

### Next Exact Steps

1. No further Tailwind migration changes are required. During final reconciliation, review the full cross-lane diff without staging, committing, or overwriting producer-owned paths.

### Decisions & Gotchas

- Tailwind v4's official upgrader was run only in `/tmp/opencode`; its broad template/CSS rewrite was not copied wholesale. The focused migration keeps the old config via `@config` and preserves CSS component-layer ordering.
- Next 15.5.27 emitted a workspace-root warning because it sees `/home/sarvesh/business_Dev/package-lock.json`; this did not prevent static export. Browser console had zero errors and four font-preload warnings.
- Do not stage/commit this lane; the user explicitly requested that it remain uncommitted.

## Safe Catalog Retry Handover (2026-10-08)

### Active Goal

Resume the catalog rebuild after `skills.sh` HTTP 429 responses, honoring retry timing, avoiding duplicate rows, and only cutting a release from a complete build. Preserve current producer data and leave all follow-up changes unstaged, uncommitted, and unpublished.

### Where We Stopped

- The previous producer PID `2456` was SIGSTOP-paused with old code loaded. It was terminated via SIGTERM/SIGCONT; its durable snapshots under `source-snapshots/` were retained.
- `scripts/build_full_catalog.py` now honors `Retry-After` delay-seconds and HTTP-dates; absent/invalid headers use a 60-second wait. Automatic waits are bounded to one hour; an excessive delay fails closed. One retry is allowed after a source's first 429; a persistent/later 429 opens the source circuit, marks it incomplete, and stops that feed instead of hammering it.
- Skills.sh and mcpservers.org sitemap IDs are deduplicated before queuing fetches; a final unique-listing-ID invariant aborts before writing output if any duplicates remain.
- `scripts/test_skill_promotion.py` adds duplicate sitemap and snapshot replay checks plus 429 retry/circuit-breaker checks.
- Focused verification passed: `scripts/test_skill_promotion.py` (211), `scripts/test_catalog_ingestion_safety.py` (31), `scripts/test_snapshot_lifecycle.py` (23), `scripts/test_remote_endpoints.py` (77), `scripts/test_mcpmarket_source.py` (105), `build_full_catalog.py --check`, `py_compile`, and `git diff --check`.
- The replacement producer shell `sh_11d4dbbf1001SZnq9OtHhWiYvb` completed, writing only to `/tmp/opencode/litespm-catalog-rebuild-20261008-retry1`; log: `/tmp/opencode/rebuild-retry1.log`. It did not write production `web/data/`.
- After that run started, `obtain()` was also tightened so refresh-mode 429s propagate to `_obtain_bulk` instead of being mistaken for stale-cache fallback. The active run uses default replay mode (not `--refresh`), so this follow-up does not change its behavior; the refresh path has its own regression check.
- Completion gate failed: `CATALOG_INGESTION_STATUS` is `incomplete` for `feed:skills-sh` and the deliberately bounded `feed:mcpmarket-com`. `skills.sh` reported 19,239 not-yet-catalogued skills (612 already present, 586 to enrich), then returned HTTP 429 again after the single 60-second `Retry-After` recovery; the producer stopped that feed as designed. Do not promote this output or cut a release.
- Source-policy investigation found `https://skills.sh/robots.txt` disallows `/api/`; the old producer's `/api/download/...` route was therefore not retried. A permitted sample listing page exposes the full description as `SoftwareApplication` JSON-LD.
- `scripts/build_full_catalog.py` now reads the sitemap-listed public skill pages, extracts their JSON-LD description (description-meta fallback), refuses `/api/`, `/internal/`, `/debug-security/`, `/search`, foreign hosts, query-bearing page URLs, and traversal paths, and preserves discovery-only installability. `scripts/test_skill_promotion.py` and `scripts/test_snapshot_lifecycle.py` cover the new page/snapshot behavior; `ARCH/03-CATALOG-SOURCES.md` and `ARCH/27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md` document the source policy.
- Offline verification after the source change passed: `scripts/test_skill_promotion.py` (221), `scripts/test_snapshot_lifecycle.py` (23), `scripts/test_catalog_ingestion_safety.py` (31), `scripts/test_remote_endpoints.py` (77), `scripts/test_mcpmarket_source.py` (105), producer `--check`, and `py_compile`.
- An initial live `--only-source feed:skills-sh --refresh` attempt (`sh_11f13b8a5001hFd2lDikdsgOQO`) was terminated with exit 143 after review found it had started just before the complete robots path/redirect guard was added. Its log `/tmp/opencode/litespm-skills-sh-pages-20261009/producer.log` shows it reached the skill URL queue (19,792 URLs) and wrote no catalog; any source snapshots it recorded are retained. The refreshed sitemap snapshots can now be replayed under the complete guard.
- The first guarded replay-mode source-only pass (`sh_11f173757001toDXZ6sar6qXDu`) was canceled by a session/server restart. Its `/tmp/opencode` log directory was cleared by that restart; no producer process remains. Durable `source-snapshots/` data survived: 3,847 `feed:skills-sh` snapshots (3,599 partial from the interrupted pass, 248 healthy), latest fetch at `2026-10-09T06:11:32Z`. The production catalog/release/pointer timestamps remain unchanged (catalog and release from Oct 7; pointer from Oct 7).
- Resumed the same default replay-mode source-only command as shell `sh_11f4f686c0012J2GPKNiqld2x0`, logging to `/tmp/opencode/litespm-skills-sh-pages-20261009-resume/producer-resume.log`. Default replay reuses recorded snapshot bodies and fetches only missing URLs; this command writes/finalizes source snapshots only, not a dataset. It is active; do not poll its output. Post-guard regression checks passed (221 skill, 23 snapshot, 31 ingestion-safety, 77 remote-endpoint, 105 mcpmarket checks; producer `--check`, `py_compile`, diff check); no producer/test code contains the old `/api/download` route.
- The permitted mcpmarket partial crawl added 2,000 rows, deferred 414,727, and reported 0 fetch failures, 0 rows missing name/summary, and 19 non-listing URLs. This is the documented bounded-partial case, but it does not waive the separate skills.sh completeness gate.
- Isolated output has 48,417 rows (46,418 MCP, 1,361 skills, 638 plugins), SHA-256 `cc2ff8aaf701859ebf39f953ead554327bf7b3b2c010ced64c91b4f29da52043`; all 48,417 IDs are unique and temporary `release.json` count/digest reconcile. Remote MCP check: 25,401 URL+transport rows are `metadata_verified`, all pass the static publishability rule, and none invent command/args. Registry summary records 509 multi-endpoint rows with extra endpoints dropped by the one-endpoint schema and 14 rows whose selected endpoint was refused; no duplicate registry IDs.
- Existing production inputs remain unchanged: current pointer is still `rel-2026-10-07-01`, sequence 145, 5,825 items; `web/data/release.json` still describes the 46,166-row producer dataset. The temporary 48,417-row output was not copied into `web/data/` and no release was built.

### Next Exact Steps

1. Wait for shell `sh_11f4f686c0012J2GPKNiqld2x0`'s completion notification; inspect its log/status once. Require no incomplete/stale `feed:skills-sh` and no rate-limit circuit before rebuilding.
2. If the source-only replay completes cleanly, run the full producer in replay mode into a new isolated output directory; retain both earlier temp outputs. The mcpmarket source may remain bounded-partial only with the exact deferred count and zero fetch/layout failures.
3. Inspect completeness, unique IDs, endpoint safety, counts, and digest/stats of the new full output. Do not promote if any core source is incomplete or stale.
4. Only after the gate passes, confirm production `web/data/catalog.json` and `web/data/release.json` remain the existing 46,166-row data, transfer the accepted output, then cut and verify the release. Confirm the current pointer before assuming sequence 146; run final Go/Python checks, web build, and browser checks.
5. Leave the pre-session stash untouched; do not stage, commit, push, or publish.

### Decisions & Gotchas

- The temporary producer output protects the existing producer dataset from a run that finishes with an incomplete/rate-limited source. `--output` directs both `catalog.json` and producer stats into that directory; the producer still replays/fetches snapshots from the repository snapshot store.
- A complete HTTP fetch/build is not release evidence by itself: inspect the machine-readable completeness report before promoting output. The accepted mcpmarket crawl budget can produce an intentionally partial source; distinguish this from an accidental skills.sh rate-limit stop.
- Existing browser-only dev `useId` hydration warning remains documented above; it is not part of this rate-limit change.

## Terminal CLI Recheck (2026-10-08)

### Active Goal

Recheck LiteSPM's terminal CLI, local daemon/MCP bridge, stateful workflows, and repository checks in an isolated root sandbox. Do not touch the user's real config/keyring or publish a release.

### Where We Stopped

- Created and used the requested root sandbox `tmp-cli-sandbox-2026-10-08/` (currently untracked; intentionally retained for inspection). It contains a Go-built `litespm`, isolated HOME/XDG/LiteSPM roots, local fixture inputs, logs, and a sandbox catalog tree. No production `web/public` pointer or `web/data` file was written by the CLI acceptance run.
- Built `tmp-cli-sandbox-2026-10-08/litespm` and exercised all documented help pages and command families, aliases, version, setup/init wizard quit, host list/detect/setup/remove/uninstall, local and live agent list/resolve, catalog search/sync/build+verify, inventory/list, lock write/check/verify/SPDX, copy/apply/remove/restore, every import format (including server object/array), local and GitHub skill add/list/update/remove, and CLI installability refusals. Import traversal was refused (`LPSM-IMPORT-003`), a command-source marketplace was refused (`LPSM-IMPORT-005`), and server.json secret values were reduced to environment-variable names in the preview. The environment-mismatch during one install-remove probe was corrected; repeat with matching `$CODEX_HOME`/`$OPENCODE_CONFIG_DIR` passed and removed only the test entry.
- Live sandbox network checks passed: catalog sync fetched `rel-2026-10-07-01` (5,825 entries), ACP agent list returned 41 entries, local catalog search returned results, and both `self-update`/`update` reported v0.3.0 current. A real GitHub-backed skill was fetched, installed, listed, updated in dry-run mode, and removed. No production catalog path was written.
- Installability was checked against that fetched release: its MCP/skill entries were discovery-only; `install`/`add`/`i` refused the skill with `LPSM-NOT-INSTALLABLE`. A frozen plugin install passed lock verification but correctly failed `LPSM-ARTIFACT-UNAVAILABLE` because the catalog has no plugin artifact source. This is a current catalog/product limitation, not evidence that end-to-end package installation succeeds.
- The daemon started and shut down cleanly using isolated data/config/runtime roots and a sandbox-only `secret-tool` shim. MCP `initialize`, `tools/list`, and all 12 advertised tool calls were exercised. Catalog search/detail and installed/capability listing worked; unavailable/unsafe routes failed closed. `get_invocation` and `cancel_invocation` returned the documented JSON-RPC `-32601` not-implemented errors.
- Doctor/daemon vault behavior was tested two ways: a deliberately failing sandbox shim made `doctor` report `LPSM-AUTH-VAULT-UNAVAILABLE` and made daemon startup refuse; a separate successful sandbox shim allowed the live daemon test. The real desktop keyring was never reachable or touched.
- `doctor --repair` and `doctor --repair --yes` found no automated actions because staging was clean; they reported the same expected vault failure (exit 50) and made no repair changes.
- Sandbox catalog build used the current 46,166-row input and produced `rel-2026-10-08-01`, sequence 146, in the sandbox only. After the fix below, build and verify print the same actual pointer manifest digest.
- Found/fixed a CLI reporting bug: `catalog build` had labeled `manifest.ContentDigest` as `Manifest`, while verify reports `current.json.manifestDigest`. `cmd/litespm/main.go` now prints `output.ManifestDigest`; `cmd/litespm/catalog_build_test.go` adds `TestCatalogBuildPrintsActualManifestDigest` (first observed failing, then passing).
- Fixed the flaky IPC race test: it assumed that reading a handler response meant the handler's deferred semaphore release had already run, then waited for another handler without consuming a possible valid rate-limit response. `internal/ipc/ipc_test.go` now consumes transient `CodeRateLimited` replies and retries with a five-second client-side deadline; server concurrency/backpressure semantics are unchanged.
- Verification passed after the fix: `go test ./... -count=1`; default-parallel `go test -race ./... -count=1 -timeout=15m`; `go vet ./...`; `gofmt -l .` clean; `git diff --check`. The IPC race test also passed at `-count=100`.
- Broader checks passed before this IPC test-only adjustment: `bash -n scripts/*.sh`; `node --check` for npm entry points; all five Python suites (31/211/23/77/105 checks) plus producer `--check`/`py_compile`; `cd npm && npm test`; `cd web && npx tsc --noEmit && NEXT_TELEMETRY_DISABLED=1 npm run build`.
- The first default-parallel race run had hit the package's 10-minute test timeout in `internal/ipc/TestServerBoundsConcurrentHandlers`; after correcting the response/semaphore ordering assumption, the full default-parallel race suite passed. The manifest-digest display correction above was also reverified by the normal and race Go suites.
- The Next build regenerated the known route-types reference in `web/next-env.d.ts`; that generated line was restored per the prior handover. Build emitted only the existing workspace-root/multiple-lockfiles warning.

### Next Exact Steps

1. The earlier producer output failed the release gate and remains isolated. The `/api/` endpoint was found disallowed by robots.txt; a guarded replay-mode source-only pass is active on public pages (see preceding handover). No dataset was promoted and no release has been cut.
2. Continue release workflow only after every core feed is complete, then finish catalog identity/digest verification and browser smoke tests; keep every change unstaged, uncommitted, and unpublished.
3. No follow-up is needed for the observed CLI/test defects; the first parallel race timeout is resolved at the test seam. Do not change server semaphore behavior without a separate product requirement.

### Decisions & Gotchas

- The root sandbox is intentionally not deleted: user asked to create it, and its binary, generated catalog tree, fixture inputs, and logs are useful evidence. It is untracked and must not be staged.
- All live CLI network tests were pointed at sandbox state; the incomplete 48,417-row producer dataset remains isolated in `/tmp/opencode/litespm-catalog-rebuild-20261008-retry1` and was not promoted. A stopped source-only refresh wrote no dataset; retained source snapshots do not touch production catalog data.
- A successful daemon run with the test shim proves local daemon/IPC behavior only; it does not prove the real OS secret vault is configured. The sandbox doctor correctly leaves that as a failure.
- The race test now reflects the actual ordering contract: response delivery may precede semaphore release, and a request in that interval can be legitimately rate-limited. The test retries that response rather than deadlocking.

## Website UI Review (2026-10-09)

### Active Goal

Review the website UI while the guarded catalog replay runs; fix only reproduced website issues. Do not alter producer data or release artifacts.

### Where We Stopped

- `web/components/navigation/Header.tsx`: narrow screens now use a 92px content-height two-row header, giving primary navigation its own 44px touch row. Desktop returns to the original 48px content height. The Search affordance can focus the Explore search field when clicked while already on that route.
- `web/app/globals.css` defines responsive `--site-header-height` (48px desktop / 92px narrow). The `--stack-top` value in `HomeView.tsx`, `ExploreView.tsx`, `AgentsView.tsx`, `CategoriesView.tsx`, `TrendingView.tsx`, and all four route states in `PackageView.tsx` now follows that token.
- `CategoriesView.tsx` no longer reserves a hidden 130px mobile grid track. At 375px, the category-name track increased from 115px to 261px.
- Browser verification at 320/375/767px found all four primary links visible and hittable; desktop layout at 768/1280px retained the single-row header. A Categories → Coverage click navigated and marked Coverage active. On Explore, the header Search click navigated to `?focus=search` and focused the input, including the same-route case. The sticky Explore toolbar settled at 92px on mobile (header is 93px including its border) and 48px on desktop.
- Mobile screenshot: `.playwright-mcp/litespm-categories-mobile-fixed.png`.
- `./node_modules/.bin/tsc --noEmit --pretty false` passed after stopping the dev server; `git diff --check` passed. The Next dev server was stopped after browser verification. No production build was run for this focused UI pass.
- Existing Next dev `useId` hydration mismatch remains visible on Home/Explore in development, matching the prior recorded finding; it was not introduced or changed here. The prior clean production build/browser check had no hydration errors.

### Next Exact Steps

1. Continue the active source-only replay using the state above; do not reread/re-fetch completed snapshots or run a full producer build before its completeness gate passes.
2. On completion, inspect the new replay log and source status once. If complete and non-stale, run the full producer in a new isolated output directory and perform the existing completeness/uniqueness/endpoint/digest review before any promotion.
3. If resuming the website task later, test a resolved package-detail slug and optionally run a static production build; do not repeat the responsive checks already recorded above.

### Decisions & Gotchas

- Mobile header growth is intentional: keeping all four primary destinations visible at 320px is preferable to hiding links behind an unmarked, 99px horizontal scroll viewport. Sticky offsets track the responsive header token.
- The interrupted skills.sh replay left reusable partial snapshots, not a promotable catalog. The resumed command runs without `--refresh`, so completed page snapshots are replayed locally; only missing URLs can trigger network requests.
- All files remain unstaged, uncommitted, and unpublished. Producer-owned `web/data/*`, the pre-session stash, and `tmp-cli-sandbox-2026-10-08/` remain preserved.
