# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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

### Fixed

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

### Known limitations

- The install path cannot execute end-to-end until the daemon supplies an
  artifact source; `invoke_capability` and related tools return JSON-RPC
  `-32601`. See `STATUS.md` and `REMEDIATION-PLAN.md` (finding M3).
- The catalog release tree and pointer are published, and `catalog sync`
  succeeds against the live origin (2026-10-05); releases remain unsigned.
