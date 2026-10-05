# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

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
- `catalog sync` returns 404 against the live origin until the catalog release
  tree is published.
