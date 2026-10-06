# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Host documentation audit (2026-10-06, `ARCH/30` §8.1).** Every bridge target and all six hand-written adapters were re-checked against live vendor
  documentation. The table header had claimed each row was checked against the agent's own documentation; for a number of rows that was not true, and the
  defects found share one failure mode — the bridge is written, `host verify` reports ready, and the host never reads it. Corrected: an invented OpenCode
  `mcp.servers` layout (the host has only a flat `mcp` map), Cline's settings file (it moved out of the VS Code `globalStorage` into
  `~/.cline/data/settings/`), Pi's undocumented fallback paths and its wrong container, Amp's container (a literal top-level `"amp.mcpServers"` key, not a
  nested object — its schema sets `additionalProperties:false`), Crush's Windows path and its schema-required `type`, `CODEX_HOME`/`GROK_HOME`/
  `AIDER_DESK_HOME_DIR`/`QWEN_HOME`/`PI_CODING_AGENT_DIR`/`OPENCODE_CONFIG_DIR` overrides, CodeBuddy's and Kilo's documented candidates, fx's project key,
  Kode's `KODE_CONFIG_DIR` filename, AstrBot's root, and three rows missing a documented scope. `RenderManualSetup` also emitted the entry object where the
  server name belongs, so the wizard's paste-this-snippet fallback printed invalid JSON for all 44 data rows. New: `FlatKey` and `ShapeStdioTyped` target
  metadata, and `internal/host/host_docs_audit_test.go`, which asserts the host-visible result (valid JSON, the server named, the entry under the key the
  vendor documents) for every registered adapter rather than only that our writer round-trips. Vendor self-contradictions on `type` (Cursor, Firebender, the
  two Snowflake pages) are recorded rather than guessed at.

- **`litespm catalog build`.** Converts the committed dataset into the static
  `/v1` release tree (`manifest.json`, `listings.json`, `versions.json`) and
  owns the served pointer `web/public/v1/current.json`: release ids derive per
  day with a `-NN` suffix, sequences advance from the released pointer, reused
  release ids and non-advancing sequences fail closed (release paths are
  CDN-immutable), and `-materialize` reproduces a released tree byte for byte —
  `scripts/deploy-pages.sh` uses it for every packaging run.
- **Catalog client integrity hardening.** `catalog sync` now verifies that the
  pointer's `manifestDigest` matches the served manifest bytes before trusting
  the release, validates `schemaVersion` and release-id path safety, caches
  served bytes verbatim (pointer last, as a commit marker), and re-verifies
  pointer/manifest/listings digests on every offline cache load.
- **Published catalog release tree.** The live origin serves release
  `rel-2026-10-05-01` (sequence 143, 5,814 capabilities) byte-identically to the
  committed release, with the pointer digest matching the served manifest;
  `litespm catalog sync` and `search` were verified against it from a clean data
  root with no registry override.
- **HTTPS artifact fetcher (ARCH/17 §4).** `internal/artifact/fetcher.go` adds
  the retrieval half the artifact layer was missing: HTTPS-only, no credentials
  in URLs, SSRF refusal of loopback/link-local/private/unspecified/multicast
  addresses at dial time (dialing the checked IP, so DNS rebinding cannot bypass
  it), redirect cap with no https→http downgrade, request timeout, bounded spool
  with streaming SHA-256, and fail-closed verification that refuses an unpinned
  artifact. Data-only; extraction is unchanged. Covered by
  `internal/artifact/fetcher_test.go` and `test/archive_install_e2e_test.go`
  (download → verify → extract → CAS install). It has no production caller yet:
  the catalog carries no artifact locators.
- **Marketplace skill installs.** `litespm install <skill-id>` and the Bridge
  `request_install` → `install.execute` path install real skills: the listing's
  source is fetched (GitHub `/tree/<ref>/<path>` browse URLs resolve to a clone
  target plus pinned ref and subpath), the skill is written into the detected
  agents' skill directories through the existing ledger and policy gate, and an
  install record is persisted. The previous in-memory synthetic package is
  removed; MCP/plugin listings fail closed with `LPSM-ARTIFACT-UNAVAILABLE`
  because the catalog carries no artifact locator for them.

- **Host config splice contract (`ARCH/16` §3.7.1).** Every JSON/JSONC host —
  the four bespoke adapters as well as the 44 data-driven targets — now edits a
  user config by byte offset (`renderBridgeEntryJSON`) instead of parsing into a
  map and re-serializing. Comments, key order, indentation, unknown keys and the
  trailing newline survive byte for byte; `cline`, `claude-code`, `opencode` and
  `pi-agent` previously reformatted, re-sorted and reflowed the whole document.
  Reads go through a comment-tolerant parse, so a JSONC config (Cline's lives in
  VS Code's globalStorage settings) can be set up and inspected instead of being
  refused; genuinely invalid JSON is still refused.
- **`host remove` layout tolerance.** Removal now tries every documented
  container path and rewrites only the one that actually holds the bridge entry.
  OpenCode's real v1 layout is `mcp.<name>`, while removal looked for a
  top-level `mcpServers`, so `host remove` could leave a registration behind; a
  JSONC config also defeated the old layout detection. Removal additionally
  repairs the residue of cutting a member (a comma stranded before a closing
  brace, a comment line that documented the removed member) and refuses rather
  than writing a document it has just proved is broken.

### Changed

- **Deployment renamed to `litespm`.** The live Cloudflare Worker moved from the
  pre-rebrand `litepsm` hostname to
  `https://litespm.sarveshbh-2022.workers.dev`; `DefaultRegistryURL`, the site
  default, npm docs and `wrangler.toml` were updated together, and the stale
  Pages-only `pages_build_output_dir` field was dropped (this is a Worker). The
  legacy `litepsm` Worker still serves the same frozen release and should be
  retired.
- **Rebranded to LiteSPM.** The product, CLI (`cmd/litespm`), Go module
  (`github.com/sarv-projects/litespm`), npm package (`litespm`), and host bridge
  key were renamed from the former identifiers. Upgrades adopt the previous data
  root, configuration, environment variables (with a legacy `LITEPSM_*`
  fallback), and host bridge entries non-destructively, so existing installs
  migrate without data loss or duplicate bridge registration.
- **Connectors removed.** The unreachable `internal/connector` package and its
  seeds were deleted (delete rather than ship dead wiring); documentation and
  module counts were corrected.

- Host config removal restores TOML configs and multi-line JSON objects byte for
  byte. A single-line JSON object can retain one line break, because inserting an
  entry into it necessarily added a line; content and validity are preserved.

### Fixed

- **CI now judges the deploy bundle with the deploy's own checks.** `scripts/deploy-pages.sh` was referenced by no workflow, so its packaging — and
  the leak audit and digest verification inside it — only ever ran on the machine doing the deploying. Both judgements moved into
  `scripts/audit-pages-dist.sh`, which the deploy script and CI now call, so a source map in the export or a release tree that no longer matches its
  pointer fails CI instead of shipping. `build-web` gained the Go toolchain the packaging step needs.
- **CI proves the committed dataset still reproduces the published release.** Nothing in CI touched `web/data/catalog.json`, so an edit that made it
  un-compilable (duplicate or unsafe listing ids), or that drifted from the released tree, surfaced only at deploy time. CI now runs
  `catalog build --materialize` against the committed pointer — a hermetic reproducibility check, no network — and verifies the result.
- **A stale local build can no longer shadow the pinned release.** The postinstall preferred `dist/<binary>` over the published release and stamped the
  install `"local"`, so a checkout with an old build reinstalled it forever and never fetched the version the package pins. `dist/` is now ignored
  unless `LITESPM_ALLOW_LOCAL_DIST=1` is set, and that path says plainly that its checksum is unverified.

- **Installer executes only verified bytes.** `npm/scripts/install-binary.js` verified checksums best-effort: a missing manifest entry, an unreadable
  manifest or a fetch failure each printed a warning and installed the binary anyway, and a digest mismatch aborted while still exiting 0, so npm
  reported success with no binary present. Verification is now fail-closed — anything unproven is deleted rather than installed — and a refusal is
  reported as an integrity failure rather than a network notice. Redirects are also HTTPS-only now: an `http://` Location previously downgraded both
  the binary and its checksum manifest to cleartext, which combined with the fail-open path to mean arbitrary code execution.
- **The npm wrapper no longer runs whatever `litespm` is on `PATH`.** `resolveBinary()` fell back to `which litespm` with no version or checksum check,
  so `npm i -g litespm` could silently execute an older release, a local build, or anything earlier on `PATH`. A package that pins a version now runs
  that version or exits non-zero.
- **Deploy leak audit missed source maps.** `scripts/deploy-pages.sh` forbade `*.go`, `*.env*`, keys and databases but not `*.map`, so a `.js.map` —
  which embeds the original module source — would have shipped to the CDN. Its `*.ts` rule also carried a chain of `-not` exceptions that could never
  apply (a `.ts` file never matches `*.js`), reading like an allowlist while forbidding every `.ts` file; both are fixed. `npm ci || npm install` is
  now a strict `npm ci`, so a release cannot ship a dependency tree no lockfile describes.
- **Packaging now verifies the release tree against its own pointer.** `/v1/releases/*` is CDN-cached immutably, so a tree that disagrees with
  `current.json` is unrecoverable once uploaded, and nothing checked it: `scripts/deploy-pages.sh` only asserted the files existed. `litespm catalog
  build --verify <dir>` re-runs the client digest chain (`pointer.manifestDigest` → `manifest.files[].digest`/`size`) over the written bundle and the
  deploy script calls it. Verified against a materialized tree and against a one-byte `listings.json` edit and a truncated `versions.json`, both of
  which now fail the deploy.

- **JSON canonicalization numbers.** `CanonicalizeJSON` formatted numbers with
  Go's `'g'` float style, turning every file size ≥ 10⁶ into scientific
  notation (`4.900491e06`) — valid JSON, but not the integer a release
  manifest declares, so the builder's own manifest could not be decoded back
  into its `int64` size fields. It now follows RFC 8785 (ECMAScript
  `Number::toString`) exactly, with range-boundary tests.
- **Catalog dataset ids.** `scripts/build_full_catalog.py` normalizes listing
  ids through fail-closed `canonical_id()` (query junk, apostrophes, and
  characters outside the domain grammar map to `-`); the committed dataset
  shipped six ids the domain grammar rejects and now has none.

Verified remediation phases 0–3:

- **CI gates.** Workflow-scoped URL checks, guards, and the 0/1/2 exit contract
  for the repository CI checker.
- **Fail-closed paths.** Vault open, the `skills.read_resource` handler, and the
  standalone bridge handler now fail closed; `doctor` returns category-based
  non-zero exit codes instead of reporting success on failure; the IPC
  accept/stop race was fixed and verified under `-race`.
- **Install pipeline.** A real resolver→plan path with a persisted plan hash;
  install validates the plan, expiry, and digest inside a transaction with
  `rolling_back` and per-operation staging; startup recovery of incomplete
  operations halts the daemon (exit **1**, `Fatal: startup recovery failed`) when
  an operation cannot be recovered.
- **Skills.** Installer and update support atomic swap, rollback, and dry-run;
  the ledger records provenance (digest, ref, inventory); source pin parsing,
  the policy hook, and scope-checked removal were fixed.
- **Self-update.** Real HTTPS release-manifest fetch with fail-closed SHA-256
  verification, a downgrade guard, atomic `O_EXCL` replacement in a private
  directory (with a Windows backup/swap path), self-boot verification, and
  rollback.
- **Release contract.** The release asset file-name contract was corrected so
  the self-update path and the published artifacts agree.

- **Multi-level container paths in the surgical merge.** The container-chain
  builder hardcoded the `litespm` member name at the leaf and dropped
  intermediate key names, so a two-level path such as OpenCode's `mcp.servers`
  produced a spurious extra `mcp` level (`mcp.mcp.litespm`). Every data-driven
  target uses a one-level path, so the defect was latent until the bespoke
  adapters were routed through the shared helper; it is now pinned by
  `TestMergeJSONEntrySurgicalTwoLevelKeyPath`.

### Known limitations

- The install path cannot execute end-to-end until the daemon supplies an
  artifact source; `invoke_capability` and related tools return JSON-RPC
  `-32601`. See `STATUS.md` and `REMEDIATION-PLAN.md` (finding M3).
- The catalog release tree and pointer are published, and `catalog sync`
  succeeds against the live origin (2026-10-05); releases remain unsigned.
