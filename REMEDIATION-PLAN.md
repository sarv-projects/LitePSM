# LitePSM Remediation Plan

Source: full-repo audit (159 findings) re-verified at `510df41` → **10 FIXED, 42 PARTIAL, 107 NOT FIXED**.
This file is the execution tracker. Status values: `TODO` / `IN_PROGRESS` / `DONE` / `VERIFIED`
(`VERIFIED` = independent crosscheck agent confirmed with evidence + full toolchain green).

> **Wave 1 status: VERIFIED.** Phases 0 + 2 implemented, crosschecked by 2 independent reviewers
> (17/17 claims confirmed), 5 crosscheck defects X1–X5 fixed and re-verified by a third independent
> agent (mutation tests in /tmp). Known non-regression: `GOOS=solaris|illumos go build ./internal/doctor/`
> fails only via the pre-existing `modernc.org/sqlite` issue, byte-identical at HEAD (D3 exclusion).
> Cosmetic nit logged: bridge_test daemon-backed `list_installed` assertions don't detect canned panel
> text (conformance_test.go:304 does). Phase 3 agent: update the status cells you complete (TODO → DONE)
> and add a DONE for each finding you close.

## Decisions (locked)

- **D1** — Wire `auth` + `mcpclient` + provider supervision; wire `source`/`connector` minimally;
  delete rather than ship unreachable code.
- **D2** — Fabricated success is replaced by explicit `not_implemented` RPC errors / non-zero CLI
  exits (fail-closed). Tests asserting fabrication are rewritten in the same change.
- **D3** — Supported platforms = windows/darwin/linux. Build tags become `unix`; plan9/js failures
  outside `internal/provider` (e.g. modernc.org/sqlite) are out of scope.
- **D4** — Go `catalogbuild` is the single builder; canonical listing-ID scheme = 4-seg.

## Wave 1 crosscheck defects (found by independent verifiers, must fix before VERIFIED)

| ID | Defect | Status |
|---|---|---|
| X1 | `internal/doctor/diskspace_unix.go` tagged `unix` but `syscall.Statfs` undefined on netbsd/openbsd/solaris/illumos → regresses builds that worked at HEAD | TODO |
| X2 | `cmd/litepsm/main.go` `skills.read_resource` fabricates `"Resource %s for skill %s."` on every failure path (D2 violation) | TODO |
| X3 | D2 not fully executed: `internal/bridge/shim.go` standalone fallbacks still fabricate success (`Capability %s invoked successfully.` etc.) and `bridge_test.go`/`conformance_test.go` assert them | TODO |
| X4 | `scripts/check_ci.py`: queries latest run of ANY workflow (not just CI) → false-green window; `jobs` non-dict elements raise unguarded AttributeError | TODO |
| X5 | doctor canary: read-back/value-check failure paths never attempt Delete → leftover canary in keyring, message doesn't say so | TODO |
| X6 | Tracker statuses stale; residual fabrications must be logged: `install.remove` returns `removed:true` on DB error (→ Phase 3), `repair.go` `Applied:true` on ReadDir error (finding 46, Phase 3), shim "○ Stopped" for unknown status (cosmetic, Phase 1/AGENTS UI) | PARTIAL — `install.remove` and `repair.go` now fail closed (Phase 3); shim "○ Stopped" cosmetic item still TODO |

## Phase 0 — Gates  (status: DONE pending X1/X4)

| Finding | Task | Status |
|---|---|---|
| gofmt | `gofmt -w` the 10 unformatted files; `gofmt -l .` must be empty | DONE |
| 158 | `internal/provider` build tags → `unix` (+ no-op fallbacks); `GOOS=plan9`/`GOOS=js` build of that package green | DONE |
| 124 | ci.yml: add gofmt, `go vet`, `tsc --noEmit`, `node --check`, `bash -n`, `py_compile`, `permissions:`, `concurrency:`; release.yml runs tests before build | DONE |
| 93 | `scripts/check_ci.py`: auth from `GITHUB_TOKEN`, guard empty runs, nonzero exit on failure | DONE (X4 pending) |

## Phase 2 — Honesty layer (status: DONE pending X2/X3/X5)

| Finding | Task | Status |
|---|---|---|
| 40 | IPC handlers: truthful `tools.list` (no hardcoded Verified/StatusReady), exact `catalog.get_item` via `GetListing`, Kind filter honored, provider.probe/get/cancel/invoke → `not_implemented` RPC error, `_ = json.Unmarshal` → `-32602` | DONE |
| 42 | `capabilities.search` / `capabilities.describe` → explicit `not_implemented` (no hard-coded canned lists) | DONE |
| 43 | `skills.list` progressive disclosure (no rawContent/instructions); `skills.load_body` errors when not installed | DONE (X2 pending: read_resource) |
| 41 | `catalog sync` failure = error; remove all hard-coded seed listings on failure paths | DONE |
| 45 | doctor: journal/disk/ReadDir/backups/CAS checks real or truthful `skipped`/`warn`; no fabricated PASS messages; canary Delete error surfaced | DONE (X5 pending) |

## Phase 3 — Wiring (status: IN_PROGRESS — 106, 105, 102, 103, 99, 39, 104 and finding 46 DONE; 97, 98/119, 118 and finding 47 still TODO)

| Finding | Task | Status |
|---|---|---|
| 106 | `prepare_plan` → real `resolver.Resolve` → persisted `InstallPlan` (planHash); `GetListing` in path | DONE |
| 105 | install: plan load + `planID` non-nil, state machine incl. `rolling_back`, tx around save+commit, per-op staging recovery | DONE |
| 102 | daemon startup calls `RecoverIncompleteOperations` | DONE |
| 103 | daemon `StartProvider`/`StopProvider` lifecycle; replace Phase-2 `not_implemented` provider handlers with real supervisor calls | DONE (probe real; invoke/get/cancel keep `-32601` with the concrete missing-subsystem reasons — no capability rows, no MCP dispatch, no invocation registry) |
| 99 | daemon constructs `auth.AuthBroker`; no `secretStore, _ :=` | DONE (secret store now fail-closed in daemon/doctor; AuthBroker wiring deferred — grep shows zero consumers and no `auth.*` IPC method in ARCH/06) |
| 97 | `catalog sync` → `source` adapters → `CompileRelease` → `Client.Sync` end-to-end (D1: wire minimally or delete) | TODO |
| 98/119 | connector: wire one real path + persistence/grants/audit, **or delete package** (D1) | TODO |
| 118 | skills: `update`, `--dry-run`, policy consult, source pin/digest in ledger, scope-checked `--all` | TODO |
| 46/47 | doctor clean_staging skips in-flight ops + truthful `Applied`; updater real HTTP/checksums/`DownloadURLs[binName]`/O_EXCL/atomic replace | 46 DONE; 47 TODO |
| 39 | install uses real path (106) or prints honest "synthetic preview" label; `--help`/unknown flags/`--scope` validation | DONE |
| 104 | wire callers for the 6 orphan IPC handlers or remove registration; add handler tests | DONE |

## Later phases (not started)

- **P1** Security fail-open: 12, 11, 8, 100/16/15, 17-21, 120, 60-63, 78, 50, 49, 2, 7, 10, 13
- **P4** Data/state: 25, 68-74, 107-109, 138-139, 52-57, 59, 75, 129, 130, 147
- **P5** Host/config: 32-38, 64-67, 116, 117, 141, 148
- **P6** CLI: 79-81, 44, 121, 153, 76, 77
- **P7** Packaging/CI/web: 51, 91, 92, 94, 95, 125, 127, 82-90, 145, 146, 156, 87, 90
- **P8** Docs/dead code: 132-135, 140, 143, 144, 149-152, 155, 159, 122, 123
- **P9** Full re-audit: 0 ❌, ≤5 🟡, toolchain green

## Verification protocol (every wave)

1. Implementer runs: `gofmt -l`, `go build ./...`, `go vet ./...`, `go test ./...`, package-scoped `-race`.
2. Independent verifier (did NOT implement) attempts to **falsify** each claim: greps for
   residual fabrication strings, re-reads cited lines, re-runs toolchain.
3. Diff review for regressions/security.
4. Status flipped to `VERIFIED` only by the verifier.
