# `web/` — LiteSPM public marketplace site

A **Next.js 15.1.7 static export** (`output: "export"` in `next.config.ts`) of React 19 + Tailwind
3.4 that publishes the catalog index: omni-search, category rail, cards, detail pages, host
coverage. It is a *read-only public site* — it never talks to the local daemon, the bridge, or the
vault.

Capability state: **`WIRED`** for the static marketplace, and the deployed catalog data is
Capability state: **`SHIPPED`** for the static marketplace and the published catalog — see
[STATUS.md](../STATUS.md) §1–§2. The site and the binary now read the same origin:
`https://litespm.sarveshbh-2022.workers.dev` serves both, and `litespm catalog sync` was verified
against it from a clean data root on 2026-10-05 (release `rel-2026-10-05-01`, sequence 143, 5,814
indexed). The earlier "the sync path is dead at the origin" defect
([ARCH/31 §4.2](../ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#42-the-published-catalog-and-the-client-read-different-files--the-sync-path-is-dead-at-the-origin))
is closed.

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
                  Each route is a Server Component (`page.tsx`) that reads `data/catalog.json`
                  at build time, a thin client wrapper (`*Route.tsx`) that owns the `next/dynamic`
                  import, and the view (`*View.tsx`) that hydrates from props.
                  `app/data/catalog.json/route.ts` exports the rows as `/data/catalog.json`
                  (`force-static`), byte-for-byte from `data/catalog.json`.
  components/     catalog/  hero/  home/  layout/  navigation/  ui/
  lib/            catalog.ts  catalogData.ts  hosts.ts  telemetry.ts  search.worker.ts
                  useCatalogSearch.ts  site.ts  clipboard.ts  format.ts
  data/           catalog.json · hosts.json · skill-targets.json · release.json   (build inputs)
  public/v1/      current.json                                                     (runtime fetch)
  out/            static export produced by `npm run build` (gitignored)
```

`next.config.ts` sets `output: "export"`, `images.unoptimized: true`, `trailingSlash: true`.

## 3. Data inputs

| File | Rows / shape | Read by | Produced by |
|---|---|---|---|
| `data/catalog.json` | **5,814** rows (`id`, `name`, `slug`, `kind`, `summary`, `category`, `publisher`, `transport`, `runtime`, `stars`, `version`, `command`, `args`, …) | **Build time only:** the `page.tsx` Server Components derive counts, facets, section rows and one page of grid rows from it and serialize *those* into the HTML. The rows themselves are never imported by client code — that is what keeps ~3.8 MB out of every JS chunk. | `scripts/build_full_catalog.py` |
| `/data/catalog.json` | the same bytes, exported by `app/data/catalog.json/route.ts` | fetched at **runtime**, lazily — on the first search, filter or entry lookup (`lib/catalogData.ts`), never on a passive visit. Versioned by `data/release.json`'s `datasetDigest` (`?v=…`) so a CDN cannot answer a newer build with stale rows. | `next build` (route handler reads `data/catalog.json` and writes `out/data/catalog.json`) |
| `data/hosts.json` | **50** bridge adapters | `lib/hosts.ts` | `scripts/gen_hosts_ts.go` from `internal/host` |
| `data/skill-targets.json` | **77** skill install targets | `lib/hosts.ts` | `scripts/gen_hosts_ts.go` from `internal/skills` |
| `data/release.json` | merged stats bundle: dataset stats (`itemCount`, per-kind counts, `hostCompatibility`, `datasetDigest`) + release identity (`releaseId`, `sequence`, `manifestDigest`, `createdAt`) | imported at **build time** (`lib/telemetry.ts`) | **Single writer per key:** `scripts/build_full_catalog.py` merges the dataset stats, `litespm catalog build` stamps the release identity |
| `public/v1/current.json` | released pointer, `releaseId rel-2026-10-05-01`, `sequence 143`, `itemCount 5814`, `manifestDigest` over `manifest.json` | fetched at **runtime**, `cache: "no-store"` (`lib/telemetry.ts`) — the release-liveness check the header reports on | `go run ./cmd/litespm catalog build --out web/public` (copied through by `scripts/deploy-pages.sh`, which re-materializes it byte-for-byte) |

`stars` is `null` on every row by policy: no upstream source exposes a machine-readable popularity
figure, so none is published (`ARCH/26` §12.4).

## 4. How catalog data is produced

1. `go run scripts/gen_hosts_ts.go` → regenerates `data/hosts.json` + `data/skill-targets.json`
   from the Go registries (so host counts cannot drift from the binary).
2. `python3 scripts/build_full_catalog.py` → fetches upstream registries and writes
   `data/catalog.json` + the dataset stats in `data/release.json` (ids funnelled through fail-closed
   `canonical_id()`).
3. `go run ./cmd/litespm catalog build --out web/public` → converts the dataset into the release
   tree (`public/v1/releases/<id>/{manifest,listings,versions}.json`), writes the released pointer
   `public/v1/current.json`, and stamps the release identity into `data/release.json`. Cuts a new
   release id/sequence from the previous pointer (releases are immutable in the CDN cache).
4. `./scripts/deploy-pages.sh` → runs `npm ci` + `npm run build`, stages `pages-dist/`, replaces the
   exported `v1/` with a byte-for-byte materialization of the released tree, writes the
   `_headers` cache policy, and runs the leak allowlist audit.
5. Publication to the live origin is a **manual** step — no workflow uploads `pages-dist/`
   (`wrangler.toml` points at it).

**All of this is manual.** CI never regenerates catalog data. The builders are split by role —
Python owns dataset ingestion, `litespm catalog build` (Go) owns pointer/release publication —
which is what the code does today; locked decision **D4 is implemented-as-split but not yet
adjudicated** ([STATUS.md](../STATUS.md) §2).

## 5. Output & deployment

- `npm run build` → **`web/out/`** (`index.html` per route, `_next/`, `404.html`, `data/catalog.json`,
  and `v1/` copied from `public/v1` — ≈28 MB today, almost all of it the release tree). Gitignored.
  The exported `v1/`
  carries whatever the last local `catalog build` left in `public/v1/` — the deploy script drops it
  and materializes the released tree instead.
- `scripts/deploy-pages.sh` → **`pages-dist/`** = `out/` + `_headers` + the byte-for-byte
  materialized release tree under `v1/`. Gitignored.
- `wrangler.toml` (`name = "litespm"`, `[assets] directory = "./pages-dist"`,
  `not_found_handling = "404-page"`) — the deployment name is the hostname and now matches the
  rebrand; the legacy `litepsm` Worker still serves the same frozen release until retired
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
3. The app performs **two** same-origin requests, both public static files and no cookies: `/v1/current.json` on load (release liveness), and `/data/catalog.json` **only when a reader searches, filters, sorts, pages or opens an entry** — a passive first visit fetches neither the rows nor any script containing them. Search, the MiniSearch worker and the index all run in the browser; there is no analytics beacon, no `localStorage`, no third-party script.
4. `scripts/deploy-pages.sh` enforces a file-type allowlist on the staged tree and exits non-zero
   on `.env*`, `.pem`, `.key`, `.db`, `.sqlite*`, `.go` or `.ts` files — but it is manual, so do not
   treat that as CI enforcement.

## 7. Truthfulness gaps (as of this pass)

- `npm run start` and `npm run lint` are unusable (§1); there is no `type-check` script despite a
  comment in `lib/hosts.ts` naming one.
- Catalog freshness is not verified anywhere: no CI step runs the Python builder, so
  `data/catalog.json` can go stale while every build stays green (row counts are printed by the
  builders, not asserted against each other).
- UI stacks described elsewhere in prose do not all exist here: the dependency list contains no
  Radix packages — the components use plain React, Tailwind and `lucide-react` icons.
- `next build` in CI proves the export compiles; it does not prove the deployed origin serves it
  (the live check used elsewhere is `GET /v1/current.json` → 200).

See [STATUS.md](../STATUS.md) for authoritative capability states and
[scripts/README.md](../scripts/README.md) for the builders referenced above.
