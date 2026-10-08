# Current Run Handover

## Active Goal

Remediate the actionable bugs reproduced in the completed LiteSPM end-to-end audit, verify each change, and preserve producer-owned dirty work. The user explicitly authorized fixes and parallel subagents.

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

## Final Next Steps

1. **Producer rebuild is running** (started 2026-10-08, background shell `sh_11c4687cc001CFtR3zpfnfGs1P`): replay recorded snapshots, fetch missing mcpmarket pages (bounded ≤2,000/run, 1 s crawl-delay, robots-permitted paths only), skill `SKILL.md` verification probes, officialskills.sh page adoption. When it finishes: inspect the run ledger (stale/incomplete counts + `extra_endpoint_rows`), confirm promotion/`url` row counts.
2. **Release cut**: `go run ./cmd/litespm catalog build` (defaults: `web/data/catalog.json` → new `rel-2026-10-08-01`, sequence 146, `current.json` + `web/data/release.json` identity sync), then `go run ./cmd/litespm catalog build --verify web/public`, then full `go test ./...` (dirty-dataset E2E), web `tsc` + `npm run build`, and a browser spot-check (a promoted skill page must now show an install command; a remote MCP page likewise).
3. User reviews the combined uncommitted diff; nothing is staged, committed, or published (producer-owned files and prior dirty hunks preserved throughout).
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
