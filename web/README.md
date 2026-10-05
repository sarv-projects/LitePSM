# `web/` — LiteSPM public marketplace site

A **Next.js 15.1.7 static export** (`output: "export"` in `next.config.ts`) of React 19 + Tailwind
3.4 that publishes the catalog index: omni-search, category rail, cards, detail pages, host
coverage. It is a *read-only public site* — it never talks to the local daemon, the bridge, or the
vault.

Capability state: **`WIRED`** for the static marketplace, and the deployed catalog data is
**`SHIPPED`** — see [STATUS.md](../STATUS.md) §1–§2. `litespm catalog sync` against the same origin
is **broken at the source** (the release tree 404s), which is why the site and the binary can show
different catalog files: [ARCH/31 §4.2](../ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#42-the-published-catalog-and-the-client-read-different-files--the-sync-path-is-dead-at-the-origin).

---

## 1. Commands (verified)

| Command | Script | Status |
|---|---|---|
| `npm ci` | — | Installs from `package-lock.json`. Works. |
| `npm run dev` | `next dev` | Works (local development server). |
| `npm run build` | `next build` | Works — writes the static export to **`web/out/`**. This is what CI and `scripts/deploy-pages.sh` run. |
| `npx tsc --noEmit` | *(no script exists)* | Works — **exits 0** as of this pass. CI runs it as a `npx tsc --noEmit` step; `package.json` has **no `type-check` script**, so `npm run type-check` fails with "Missing script" even though `lib/hosts.ts` refers to it in a comment. |
| `npm run start` | `next start` | **Broken by design.** Next.js refuses: `"next start" does not work with "output: export" configuration. Use "npx serve@latest out" instead.` Use a static file server on `out/`. |
| `npm run lint` | `next lint` | **Broken / non-hermetic.** There is no ESLint config and no `eslint` dependency, so `next lint` opens an interactive "How would you like to configure ESLint?" prompt and hangs when stdin is not a TTY. It cannot pass in CI and is not run there. |

Only `npm ci` + `npx tsc --noEmit` + `npm run build` are wired into CI
(`.github/workflows/ci.yml`, job `build-web`), which then asserts `test -f web/out/index.html`.

## 2. Layout

```text
web/
  app/            Next.js App Router pages: /  explore/  agents/  categories/  trending/  package/
  components/     catalog/  hero/  home/  layout/  navigation/  ui/
  lib/            catalog.ts  hosts.ts  telemetry.ts  search.worker.ts  useCatalogSearch.ts
                  site.ts  clipboard.ts  format.ts
  data/           catalog.json · hosts.json · skill-targets.json · release.json   (build inputs)
  public/v1/      current.json                                                     (runtime fetch)
  out/            static export produced by `npm run build` (gitignored)
```

`next.config.ts` sets `output: "export"`, `images.unoptimized: true`, `trailingSlash: true`.

## 3. Data inputs

| File | Rows / shape | Read by | Produced by |
|---|---|---|---|
| `data/catalog.json` | **5,814** rows (`id`, `name`, `slug`, `kind`, `summary`, `category`, `publisher`, `transport`, `runtime`, `stars`, `version`, `command`, `args`, …) | `lib/catalog.ts`, `lib/search.worker.ts` (MiniSearch index in a Web Worker) | `scripts/build_full_catalog.py` |
| `data/hosts.json` | **50** bridge adapters | `lib/hosts.ts` | `scripts/gen_hosts_ts.go` from `internal/host` |
| `data/skill-targets.json` | **77** skill install targets | `lib/hosts.ts` | `scripts/gen_hosts_ts.go` from `internal/skills` |
| `data/release.json` | release pointer (`releaseId`, `sequence`, `itemCount`, `manifestDigest`, `advisories`, per-kind counts, host compatibility) | imported at **build time** (`lib/telemetry.ts`) | `scripts/build_full_catalog.py` |
| `public/v1/current.json` | same pointer, `releaseId rel-2026-09-30-01`, `sequence 142`, `itemCount 5814` | fetched at **runtime**, `cache: "no-store"` (`lib/telemetry.ts:110`) — the app's **only** network request | `scripts/build_full_catalog.py` (copied through by `scripts/deploy-pages.sh`) |

`stars` is `null` on every row by policy: no upstream source exposes a machine-readable popularity
figure, so none is published (`ARCH/26` §12.4).

## 4. How catalog data is produced

1. `go run scripts/gen_hosts_ts.go` → regenerates `data/hosts.json` + `data/skill-targets.json`
   from the Go registries (so host counts cannot drift from the binary).
2. `python3 scripts/build_full_catalog.py` → fetches upstream registries and writes
   `data/catalog.json`, `public/v1/current.json`, `data/release.json`.
3. `./scripts/deploy-pages.sh` → runs `npm ci` + `npm run build`, stages `pages-dist/`, writes the
   `_headers` cache policy, and runs the leak allowlist audit.
4. Publication to the live origin is a **manual** step — no workflow uploads `pages-dist/`
   (`wrangler.toml` points at it).

**All of this is manual.** CI never regenerates catalog data, and the Go release compiler
(`internal/catalogbuild.CompileRelease`) has no non-test caller — two builders exist and locked
decision **D4 is contested** ([REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md), `ARCH/31` §4.3).

## 5. Output & deployment

- `npm run build` → **`web/out/`** (≈4.7 MB: `index.html`, `_next/`, `explore/`, `agents/`,
  `categories/`, `trending/`, `package/`, `404.html`, `v1/`). Gitignored.
- `scripts/deploy-pages.sh` → **`pages-dist/`** = `out/` + `_headers` + `v1/current.json`.
  Gitignored.
- `wrangler.toml` (`name = "litepsm"`, `pages_build_output_dir = "pages-dist"`,
  `[assets] directory = "./pages-dist"`) — the deployment name is the hostname and is deliberately
  **not** rebranded ([REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md) `M1`).
- Canonical origin comes from `NEXT_PUBLIC_SITE_URL` at build time, defaulting to the live
  deployment origin in `lib/site.ts`.
- The release workflow also archives the export as `litespm-web-static.tar.gz` in the GitHub
  release (`.github/workflows/release.yml`).

## 6. No-secrets rule

The site is public static content. Binding rules:

1. **No keystore, vault, token, or config-file data ever reaches `web/`.** Secrets live in
   `internal/secrets` (Go); the browser never receives them, and there is no server runtime that
   could read them.
2. The only data shipped is the JSON in §3 — public catalog metadata and host paths written as `~`
   or `<project>` placeholders by `gen_hosts_ts.go`.
3. The app performs **one** network request, same-origin `/v1/current.json`. There is no analytics
   beacon, no cookie, no `localStorage`, no third-party script.
4. `scripts/deploy-pages.sh` enforces a file-type allowlist on the staged tree and exits non-zero
   on `.env*`, `.pem`, `.key`, `.db`, `.sqlite*`, `.go` or `.ts` files — but it is manual, so do not
   treat that as CI enforcement.

## 7. Truthfulness gaps (as of this pass)

- `npm run start` and `npm run lint` are unusable (§1); there is no `type-check` script despite a
  comment in `lib/hosts.ts` naming one.
- Catalog freshness is not verified anywhere: no CI step runs the Python builder or checks that
  `data/catalog.json` matches `public/v1/current.json` (`sequence`/`itemCount` are printed by the
  builder, not asserted).
- The site renders catalog data that the Go client cannot sync against (broken release tree at the
  origin — [STATUS.md](../STATUS.md) §2).
- UI stacks described elsewhere in prose do not all exist here: the dependency list contains no
  Radix packages — the components use plain React, Tailwind and `lucide-react` icons.
- `next build` in CI proves the export compiles; it does not prove the deployed origin serves it
  (the live check used elsewhere is `GET /v1/current.json` → 200).

See [STATUS.md](../STATUS.md) for authoritative capability states and
[scripts/README.md](../scripts/README.md) for the builders referenced above.
