# LiteSPM Remediation Plan

Source: full-repo audit (159 findings) re-verified at `510df41` → **10 FIXED, 42 PARTIAL, 107 NOT FIXED**.
This file is the execution tracker. Status: `TODO` / `IN_PROGRESS` / `DONE` / `VERIFIED`
(`VERIFIED` = independent falsifier confirmed with evidence + mutation tests + toolchain green).

## Where things stand (Oct 5, post-rebrand)

- Product renamed **LiteSPM**; `litePSM` is now a symlink to `liteSPM`; module `github.com/sarv-projects/litespm`; CLI `cmd/litespm`; npm `litespm`.
- Committed: `fe42c28` (Phases 0+2 + Phase 3 core), `4332f30` (web cleanup). **Uncommitted working tree**: the rename plus Phase 3 completion and review fixes (verified, awaiting commit).
- Toolchain: `gofmt` clean · `go build ./...` · `go vet ./...` · `go test -count=1 ./...` all packages ok · web `tsc --noEmit` ok.
- Open items: pre-existing `internal/bridge` `-race` flake (reproducible at HEAD); doctor exit-code classification; deferred Phase 4 wiring (source pipeline, provider lifecycle, artifact-source install).

## Decisions (locked)

- **D1** — Wire `auth`+`mcpclient`+provider supervision; wire `source`/`connector` minimally **or delete** — delete rather than ship unreachable code.
- **D2** — Fabricated success → explicit `not_implemented` RPC errors / non-zero CLI exits (fail-closed); tests asserting fabrication rewritten.
- **D3** — Platforms windows/darwin/linux; `unix` build tags; plan9/js failures outside `internal/provider` out of scope.
- **D4** — Go `catalogbuild` is the single builder; canonical listing-ID = 4-seg.

## Wave 1 — Phases 0 + 2 (+ crosscheck defects X1–X5) — VERIFIED

Falsified then fixed and re-verified: **X1** diskspace build tags narrowed (netbsd/openbsd green; solaris/illumos fail only via pre-existing `modernc.org/sqlite`, D3); **X2** `skills.read_resource` fail-closed; **X3** bridge standalone fail-closed + tests inverted to the strict contract; **X4** `check_ci.py` workflow-scoped URL + guards + 0/1/2 exit contract; **X5** doctor canary Delete on all paths. Phase 0/2 findings (gofmt, 158, 124, 93, 40, 41, 42, 43, 45) DONE.

## Wave 2 — Phase 3 completion — VERIFIED

| Finding | Outcome |
|---|---|
| 106 / 105 | DONE — real resolver→plan (planHash persisted), install validates plan/expiry/digest, tx + `rolling_back` + per-op staging. **Caveat M3:** the daemon `install.execute` still has no `ArchiveSource`/`TreeSource`, so `request_install` cannot complete end-to-end — artifact-source wiring is **Phase 4**. |
| 102 | DONE — daemon calls `RecoverIncompleteOperations` before serving; now **halts** (exit 70) when an operation fails to recover. |
| 103 | DONE (partial) — provider start/stop wired, `provider.probe` real; `invoke`/`get`/`cancel` keep explicit `-32601` with concrete reasons. **Caveat m4:** `providers` table is never populated by non-test code → autostart is inert (Phase 4: persist provider rows on install). |
| 99 | DONE — vault open fail-closed; AuthBroker wiring deferred (no consumers). |
| 39 | DONE — real flag validation, honest synthetic-package label. |
| 104 | DONE — all six orphan handlers covered by real-IPC tests (`host.apply_setup` added). |
| 46 | DONE — repair skips in-flight staging; `Applied` truthful. |
| 98/119 connector | DONE (D1 **delete**) — zero importers proven; package + seeds removed (9 files, 2072 lines); docs corrected (`ARCH/29`, `README` counts, `ARCH/24` tombstone, `SECURITY.md`, `ARCH/00` = 21 modules). |
| 118 skills | DONE — `Installer`/`Update` (atomic swap + rollback + dry-run), ledger provenance (digest/ref/inventory), fixed `ParseSkillSource` pin parsing, policy hook, scope-checked `RemoveScoped`, removal guard; CLI wired (`skills update`, `--dry-run`, `--force`, scope). |
| 47 updater | DONE — real HTTPS release-manifest fetch, SHA-256 fail-closed, downgrade guard, O_EXCL private-dir atomic replace + Windows backup/swap, `VerifySelfBoot` wired, rollback; CLI wired (bounded download of `DownloadURLs[binName]`). Self-update completes once a release publishes `litespm-*` assets (packaging P7). |
| 97 source | **DEFERRED → Phase 4** — analysis: honest wiring needs an upstream fetcher + `catalog build` command + CI publish + one ID scheme/web projection. Do NOT delete (D4 tension). Source docs still to correct. |

### Wave 2 review defects

| ID | Defect | Status |
|---|---|---|
| M1 | `wrangler.toml` worker name vs live origin | FIXED — name stays `litepsm` (determines the `*.workers.dev` host) + explanatory comment; origins unchanged |
| M2 | Rename dropped legacy state/config/env/host-key (upgrade data loss, duplicate bridge) | FIXED — legacy root adoption (non-destructive), `LITEPSM_*` env fallback, legacy project config, host bridge-key adoption/removal; 17 hermetic tests |
| M3 | `install.execute` cannot complete (no artifact source) | OPEN → Phase 4 (marked PARTIAL) |
| m1 | dead `/explore/?sort=newest` link | FIXED |
| m2 | bridge rendered unknown status as "○ Stopped" | FIXED — `StatusUnknown` → "— Unknown" + test |
| m3 | startup recovery swallowed per-op failures | FIXED — returns error; daemon halts (exit 70) + test |
| m4 | provider lifecycle unreachable (`providers` never populated) | OPEN → Phase 4 |
| m5 | tracker statuses stale | FIXED — this rewrite |
| m6 | recovery can orphan a partial CAS tree (cross-device) | OPEN → Phase 4 (fail-closed today) |
| m7 | `doctor` exited 0 on failures | FIXED — category-based exit codes (catalog 10 / resolve 20 / approval 30 / install 40 / provider 50 / host 60 / state 70, worst-wins, warnings 0) per ARCH/20 §2; tests bind |
| m8 | module path casing vs git remote (`LitePSM`) | NOTE — non-breaking; align remote or casing at release |
| new | pre-existing `internal/bridge` `-race` flake (`bridge_test.go:61` ↔ `ipc/server.go:96`) | FIXED — `Serve` registers the accept loop under `s.mu` with a `stopped` guard; `Stop` waits after unlocking; `-race -count=50` clean, mutation reproduced the original race. Guard gap: `TestServerStopDoesNotRaceServeStartup` alone doesn't catch it (only the bridge integration test does) → strengthen in P1 (finding 10) |

## Later phases (not started)

- **P1** Security fail-open: 12, 11, 8, 100/16/15, 17-21, 120, 60-63, 78, 50, 49, 2, 7, 10, 13
- **P4** Data/state + deferred wiring: 25, 68-74, 107-109, 138-139, 52-57, 59, 75, 129, 130, 147; plus 97 source pipeline, artifact-source install (M3), provider persistence (m4), partial-CAS recovery (m6)
- **P5** Host/config: 32-38, 64-67, 116, 117, 141, 148
- **P6** CLI: 79-81, 44, 121, 153, 76, 77
- **P7** Packaging/CI/web: 51, 91, 92, 94, 95, 125, 127, 82-90, 145, 146, 156, 87, 90
- **P8** Docs/dead code: 132-135, 140, 143, 144, 149-152, 155, 159, 122, 123
- **P9** Full re-audit: 0 ❌, ≤5 🟡, toolchain green

## Verification protocol (every wave)

1. Implementer runs `gofmt -l`, `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, package-scoped `-race`.
2. Independent verifier (did NOT implement) attempts to **falsify** each claim, with mutation tests in `/tmp`.
3. Diff review for regressions/security.
4. Status flips to `VERIFIED` only via the verifier.
