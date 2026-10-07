# `scripts/` — repository scripts

Twelve scripts live in this directory. **Two of them are executed on a push or pull request.**
`.github/workflows/ci.yml` *checks the syntax* of every script (`bash -n scripts/*.sh`,
`python3 -m py_compile scripts/*.py`) and **executes** `gen_hosts_ts.go` (regeneration gate),
`deploy-pages.sh` (in its `build-web` job, so the staged bundle is packaged and leak-audited on
every push/PR), and the npm wrapper tests; `.github/workflows/release.yml`
executes `build-release.sh`. The rest are manual — including **`check_ci_local.sh`**, which runs
the CI stages on your machine in CI order.

Capability states for the subsystems these scripts feed are in [STATUS.md](../STATUS.md); defect
history is in [REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md).

## At a glance

| Script | Kind | Who runs it | Fails on bad input? |
|---|---|---|---|
| `build-release.sh` | bash | **Release** (`.github/workflows/release.yml`, on `v*` tags) | Yes — `set -euo pipefail`, `go build` aborts |
| `deploy-pages.sh` | bash | **CI** (`.github/workflows/ci.yml`, `build-web` job, every push/PR) and **manual** (site packaging) | Yes — `set -euo pipefail` + explicit leak audit `exit 1` |
| `check_ci_local.sh` | bash | **Manual** — mirrors `ci.yml` stage-for-stage (`--web`, `--quick` flags) | Yes — `set -euo pipefail`, every stage names itself on failure |
| `build_full_catalog.py` | Python | **Manual** (catalog dataset producer) | **No content assertions** — aborts only if a fetch raises; validation is fail-closed at release build time in Go |
| `gen_hosts_ts.go` | Go (`//go:build ignore`) | **CI** (regeneration gate in `static-checks`) and **manual** (before committing host-adapter edits) | Yes — `os.Exit(1)` on write failure |
| `check_ci.py` | Python | **Manual** (status probe) | Yes by contract — exit `0` / `1` / `2` |
| `check_headers.py` | Python | **Manual** (upstream research) | No — prints, never asserts |
| `check_voltagent.py` | Python | **Manual** (upstream research) | No — prints, never asserts (network errors raise) |
| `fetch_test.py` | Python | **Manual** (upstream research) | No — catches every error and prints `ERR`, exits 0 |
| `parse_catalogs.py` | Python | **Manual** (upstream research) | No — prints, never asserts |
| `parse_skills.py` | Python | **Manual** (upstream research) | No — prints, never asserts |
| `sample_voltagent.py` | Python | **Manual** (upstream research) | No — prints, never asserts |

`scripts/__pycache__/` is a build artifact of `python3 -m py_compile` and is gitignored — it is not
a script.

---

## `build-release.sh` — cross-platform release binaries

- **Purpose.** Compile the CLI for six targets and write a checksum file.
- **Invocation.** `VERSION=0.3.0 ./scripts/build-release.sh` (from the repo root; `VERSION` defaults
  to `0.3.0` and must match `npm/package.json`, which the release workflow asserts separately).
- **Inputs.** `./cmd/litespm` source, the Go toolchain.
- **Outputs.** `dist/litespm-windows-{amd64,arm64}.exe`, `dist/litespm-linux-{amd64,arm64}`,
  `dist/litespm-darwin-{amd64,arm64}`, `dist/SHA256SUMS.txt` (from `sha256sum` or `shasum -a 256`),
  built with `-trimpath -ldflags "-s -w -X main.Version=…"`, `CGO_ENABLED=0`.
- **Who runs it.** The release workflow only (`Compile Release Binaries`, with `VERSION` derived
  from the tag). CI never executes it — only `bash -n` syntax-checks it, so a broken target surfaces
  at tag time, not on a pull request.
- **Truthfulness gaps.** `SHA256SUMS.txt` is a **hash-only** integrity record: releases are unsigned
  (no signature/SBOM, [STATUS.md](../STATUS.md) §5). The checksum step is silently skipped if
  neither `sha256sum` nor `shasum` exists.

## `deploy-pages.sh` — Cloudflare Pages packaging + leak audit

- **Purpose.** Build the Next.js static export, stage a `pages-dist/` directory, **materialize the
  released catalog `/v1` tree with the Go builder**, write the `_headers` CDN cache policy
  (`ARCH/18` §4), and audit the staged tree for source/secret leaks (`ARCH/18` §5 strict allowlist).
- **Invocation.** `./scripts/deploy-pages.sh` from the repo root; optionally
  `NEXT_PUBLIC_SITE_URL=https://<origin> ./scripts/deploy-pages.sh`.
- **Inputs.** `web/` (runs `npm ci || npm install`, then `npm run build`), and the released pointer
  `web/public/v1/current.json` (must exist — it is the release the bundle reproduces).
- **Outputs.** `pages-dist/` = copy of `web/out/` **with its exported `v1/` replaced** by
  `go run ./cmd/litespm catalog build --materialize --out pages-dist`, which reproduces the
  released pointer's id/sequence/createdAt and manifest **byte for byte** (release ids are
  immutable in the CDN cache, so the deployed bytes must be exactly the bytes the pointer digests;
  the command fails closed if the pointer is missing or not a Go-built pointer) +
  `_headers` (immutable `/v1/releases/*`, no-cache `/v1/current.json`, CORS, `nosniff`).
  `pages-dist/` is gitignored.
- **Who runs it.** `.github/workflows/ci.yml` runs it in the `build-web` job (`Package and Audit
  the Deploy Bundle` step) on every push/PR, and maintainers run it by hand. It only packages —
  the `wrangler.toml` (`name = "litespm"`, `[assets] directory = "./pages-dist"`) upload is a
  separate step. The origin was published and verified 2026-10-05 (release `rel-2026-10-05-01`);
  whether subsequent uploads are triggered by an external Cloudflare build or by hand is outside
  this repository's control.
- **Truthfulness gaps.** It packages, it does not deploy. It reproduces the released pointer but
  never cuts a new release: refreshing the catalog is `python3 scripts/build_full_catalog.py`
  (dataset) then `go run ./cmd/litespm catalog build --out web/public` (pointer + tree), and only
  then this script. The `npm ci || npm install` fallback hides a lockfile mismatch. Its
  forbidden-file `find` expression rejects `.go`, `.env*`, `.pem`, `.key`, `.db`, `.sqlite*` and any
  `.ts` file, but cannot catch a secret pasted into `.json`/`.html`. Because CI runs the script,
  the audit **is** enforced on every push/PR.

## `build_full_catalog.py` — **the catalog dataset producer**

- **Purpose.** Fetch upstream markdown registries, normalize them into catalog rows, and publish
  the dataset the site and `litespm catalog build` consume (`STATUS.md` §2, `ARCH/31` §4.3).
- **Invocation.** `python3 scripts/build_full_catalog.py` from the repo root (Python 3, stdlib only,
  network required).
- **Inputs.** Upstream raw GitHub markdown: `punkpeye/awesome-mcp-servers`,
  `VoltAgent/awesome-agent-skills`, `modelcontextprotocol/servers`, plus vendor plugin marketplaces;
  and the two registries `web/data/hosts.json` + `web/data/skill-targets.json` (run
  `gen_hosts_ts.go` first — if they are unreadable the script warns and publishes **empty** host
  lists).
- **Outputs.** `web/data/catalog.json` (**5,814 rows** as committed; every listing id passes through
  fail-closed `canonical_id()`, which strips query junk/apostrophes and maps everything outside
  `[A-Za-z0-9_.~%+-]` to `-` — an earlier revision shipped six ids the domain grammar rejects) and
  the dataset stats in `web/data/release.json` (`itemCount`, per-kind counts, `hostCompatibility`,
  `datasetDigest`; the `releaseId`/`sequence`/`manifestDigest`/`createdAt` keys belong to
  `litespm catalog build` and are preserved on merge). **It no longer writes
  `web/public/v1/current.json`** — the served pointer and release tree have a single owner, the Go
  builder.
- **Who runs it.** Manual, by a maintainer, whenever the catalog should be refreshed. **CI never
  runs it** — so a green build says nothing about catalog freshness or correctness.
- **Truthfulness gaps.** It shares the publication pipeline with `litespm catalog build`: Python
  owns ingestion (dataset rows), Go owns publication (pointer, release id/sequence, digests). That
  split is what the code does today, but locked decision **D4 is not yet adjudicated**
  (`REMEDIATION-PLAN.md` is history; `STATUS.md` §2 tracks closing it). It has **no content
  assertions** — no row-count floor, no schema/ID validation of its own (the fail-closed validation
  lives in `catalogbuild.ParseDataset` and runs at release build time), no non-zero exit when output
  is degraded; vendor-source fetch failures are printed as `skipped` and the build continues. By
  documented policy it publishes `stars: null` on every row (no popularity figures are invented).

## `gen_hosts_ts.go` — host registries for the web data files

- **Purpose.** Generate the site's host data *from the Go source* so the public site cannot drift
  from the shipped binary.
- **Invocation.** `go run scripts/gen_hosts_ts.go` from the repo root (build tag `ignore`, so it is
  excluded from `go build ./...`).
- **Inputs.** `host.ListAdapters()` (bridge adapters) and `skills.AgentTargets()` (skill install
  targets). It fakes `HOME` to `/tmp/__litespm_home__` so no maintainer path leaks into the output.
- **Outputs.** `web/data/hosts.json` (**50** bridge hosts) and `web/data/skill-targets.json`
  (**77** skill targets).
- **Who runs it.** Manual, whenever an adapter or skill target is added/renamed, and **before**
  `build_full_catalog.py`.
- **Truthfulness gaps.** No workflow runs it, so the committed JSON can lag the registry until
  someone re-runs it. Windows user paths are deliberately not synthesised (registry policy,
  `ARCH/30`) — `userPath` is the Unix resolution with `~` substitution, or `<project>`.

## `check_ci.py` — latest CI run status

- **Purpose.** Report whether the most recent `ci.yml` run (and its jobs) passed.
- **Invocation.** `python3 scripts/check_ci.py`; set `GITHUB_TOKEN` to avoid anonymous rate limits
  and to fetch failed-job logs.
- **Inputs.** GitHub REST API for `sarv-projects/LiteSPM` (`/actions/workflows/ci.yml/runs`, scoped
  so a Release run can never mask a red CI run).
- **Outputs.** stdout report; exit **0** = latest run succeeded, **1** = failed/errored,
  **2** = could not determine (network/API/parse, or no verdict yet).
- **Who runs it.** Manual. No workflow invokes it (CI only byte-compiles it).
- **Truthfulness gaps.** The repository path is hard-coded, so a fork or renamed remote needs an
  edit. It reports a run's status; it is not itself a gate for anything.

## Upstream research scripts (never wired, never asserting)

These six were written while identifying catalog sources. They fetch public markdown and **print**
counts/samples; none of them validates anything, writes repo files, or is referenced by CI,
`go:generate`, or another script. Expect network failures rather than assertion failures.

| Script | What it does | Invocation |
|---|---|---|
| `check_headers.py` | Fetches `punkpeye/awesome-mcp-servers` README and prints its `##`/`###` section headers | `python3 scripts/check_headers.py` |
| `check_voltagent.py` | Fetches `VoltAgent/awesome-agent-skills` README; prints line count, headers, first list items | `python3 scripts/check_voltagent.py` |
| `fetch_test.py` | Probes four upstream READMEs, prints byte size and matched list-item count per source; swallows errors as `ERR` | `python3 scripts/fetch_test.py` |
| `parse_catalogs.py` | Parses the four upstream READMEs for `- [Title](github URL)` rows; prints totals and two samples | `python3 scripts/parse_catalogs.py` |
| `parse_skills.py` | Parses the VoltAgent skills list into `(name, url, description)` tuples; prints 15 | `python3 scripts/parse_skills.py` |
| `sample_voltagent.py` | Dumps lines 80–150 of the VoltAgent README for eyeballing | `python3 scripts/sample_voltagent.py` |

**Naming traps.** `check_headers.py` does **not** check source-file headers or license headers — no
such gate exists in the repository. `check_voltagent.py`/`parse_*.py`/`fetch_test.py` are probes,
not ingestion: the ingestion path they informed is `build_full_catalog.py`.

---

## Coverage summary (what is actually enforced)

- **Executed by CI on every push/PR:** `deploy-pages.sh` (the `build-web` job's
  `Package and Audit the Deploy Bundle` step, so the export, the byte-for-byte release
  materialization and the leak audit run on each change) — in addition to the syntax steps
  `bash -n scripts/*.sh` (syntax) and `python3 -m py_compile scripts/*.py` (byte-compile).
- **Executed by the release workflow:** `build-release.sh`.
- **Manual-only:** `build_full_catalog.py`, `gen_hosts_ts.go`, `check_ci.py`, and the six research
  probes. (Not a script, but the same workflow: the release/publication step
  `go run ./cmd/litespm catalog build --out web/public` is also manual, and `deploy-pages.sh` runs
  its `-materialize` form on every packaging run.)
- **Assertion-less (never fail on bad content):** `build_full_catalog.py` (no row/schema checks of
  its own — validation happens fail-closed later in `catalogbuild.ParseDataset` at release build
  time), `fetch_test.py` (always exits 0), and the six research probes.
- **The deployed catalog dataset producer:** `build_full_catalog.py`, manually run. Publication of
  the pointer/release tree is `litespm catalog build`; see the D4 note above and `ARCH/31` §4.3.
