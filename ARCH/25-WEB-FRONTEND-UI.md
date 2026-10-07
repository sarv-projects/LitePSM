# Web Frontend & UI Architecture (LiteSPM Market)

Inspired by the design of `mcpmarket.com`, LiteSPM Market is a static web application that serves as the visual discovery interface for AI agent plugins, MCP servers, and skills.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                   LiteSPM Market Web Architecture                      │
│                                                                        │
│   Next.js 15 / React 19 Static Export (Tailwind CSS, local primitives)│
│                               │                                        │
│   Static Client Search ◄──────┴──────► Immutable Static Releases       │
│   (MiniSearch in Web Worker)           (Cloudflare Pages /v1/releases/)│
└────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Design Vision & Aesthetic Standards

LiteSPM Market adheres to an engineering-focused, high-performance aesthetic:
*   **Typography:** IBM Plex Sans for UI, Plex Condensed for dense table names, Plex Mono for machine-readable content (commands, counts, slugs). No dark mode: single paper/surface theme.
*   **Header:** Opaque sticky bar (no blur layer — blur over ruled tables costs paint on scroll). Skip link for keyboard users.
*   **Instant Responsiveness:** Client-side filtering with deferred query values; MiniSearch index owned by a Web Worker (`web/lib/search.worker.ts`) with in-thread fallback (`web/lib/useCatalogSearch.ts`).
*   **Keyboard-First Navigation:** `Cmd+K`/`Ctrl+K` or `/` focuses search (`web/components/navigation/SearchBar.tsx`); cards are single focusable links with visible focus rings.

---

## 2. Page Sections & Component Hierarchy

```text
web/
  ├── components/
  │   ├── navigation/
  │   │   ├── Header.tsx              # Sticky opaque nav, Explore/Agents/Categories/Coverage, kind tabs
  │   │   └── SearchBar.tsx           # Omni-search with Cmd+K and `/` shortcuts, clear button
  │   ├── hero/
  │   │   ├── HeroSection.tsx         # Telemetry badge, headline, quickstart copy
  │   │   ├── CategoryRail.tsx        # Wrapped category chips with inline counts (dynamic facets)
  │   │   └── ProportionBar.tsx       # Kind share bar (mcp/skill/plugin)
  │   ├── catalog/
  │   │   ├── ExtensionGrid.tsx       # Responsive grid (1 to 3 columns)
  │   │   ├── ExtensionCard.tsx       # Card with type+category tags, host chips, no stars
  │   │   ├── PublisherMark.tsx       # Registry-flag verified mark with not-an-audit disclaimer
  │   │   └── SectionRow.tsx          # Curated home rows with view-all links
  │   ├── home/
  │   │   ├── ClientGrid.tsx          # Agent client grid
  │   │   └── FaqSection.tsx          # Honesty FAQ (stars not published, verified meaning)
  │   ├── layout/
  │   │   └── SiteFooter.tsx          # Footer with honesty notes + adapter evidence links
  │   └── ui/
  │       ├── CountUp.tsx             # Animated count-up telemetry
  │       └── Toast.tsx               # Copy-feedback toasts
  └── app/ (App Router, `output: 'export'`, `trailingSlash: true`)
      ├── layout.tsx                  # IBM Plex Sans/Condensed/Mono, site JSON-LD, skip link
      ├── page.tsx                    # Server Component: build-time home slice → HomeRoute → HomeView
      ├── HomeRoute.tsx / HomeView.tsx # client `dynamic()` half / Search-first home with curated rows + catalog grid
      ├── explore/page.tsx            # Server Component: facets + one page of rows → ExploreRoute → ExploreView.tsx (power-filter surface: kind/category/agent/verified/sort)
      ├── package/page.tsx            # Server Component → PackageRoute → PackageView.tsx (detail via `/package/?slug=<key>`, slug or id)
      ├── agents/page.tsx             # Server Component → AgentsRoute → AgentsView.tsx (supported consumer hosts with readiness)
      ├── categories/page.tsx         # Server Component → CategoriesRoute → CategoriesView.tsx (category index with per-kind breakdown)
      ├── trending/page.tsx           # Server Component → TrendingRoute → TrendingView.tsx (coverage/count ranking — counts, not stars)
      └── data/catalog.json/route.ts  # static export of the site-shaped dataset (byte-identical to web/data/catalog.json)

Each `page.tsx` is a **Server Component** that reads `data/catalog.json`
(≈3.8 MB) at build time and derives exactly the slice its route shows —
counts, facets, one page of rows — so the dataset itself never enters a
client chunk; only that slice is serialized into the HTML as props. The view
loads through a thin client `*Route.tsx` wrapper whose only job is route-level
code splitting (`next/dynamic`, `ssr: true`). Placing `dynamic()` in the
Server Component does **not** split — the view is inlined into the route chunk
(measured: 129 kB First Load) — while the client wrapper keeps every route at
the 107 kB shared baseline. Rows beyond the prerendered page are fetched at
runtime from `/data/catalog.json` with `?v=<datasetDigest>` (from
`web/data/release.json`, so a cached copy cannot disagree with the HTML beside
it), and only when an interaction needs them: search, filter, sort,
"Show more", or a `/package/?slug=` deep link. Categories, Agents, Trending
and a bare `/package/` never fetch it — a first visit downloads the page, not
rows it has not asked for.
```

---

## 3. Key UI Elements

### 3.1 Header & Dynamic Telemetry Badge
*   **Sticky Header:** Displays the LiteSPM logo, primary navigation links (**Explore**, **Agents**, **Categories**, **Coverage** → `/trending/`), kind tabs (All/MCP servers/Agent skills/Plugins with live counts), and a GitHub link.
*   **Release Stamp (`web/components/hero/HeroSection.tsx` → `ReleaseStamp`):** a status pill in the hero whose text is the *observed* state of a real `GET /v1/current.json`, never a hard-coded liveness claim:
    *   **`loading`** (the first render, prerendered HTML included): `Checking release…` — the bundled manifest proves what this build shipped, not that the origin still serves it.
    *   **`ready`** (the fetch answered 2xx): `Live catalog · Updated {relativeAge(createdAt)}`, with `releaseId · seq · manifestDigest` in the `title`. `relativeAge` reads the client clock, so it is computed after mount rather than during render (a `Date.now()` in render would hydrate to a different string than the server printed).
    *   **`offline`** (the fetch failed): `Bundled catalog` — the counts on screen are the build's own snapshot.
*   **Dynamic counts:** `itemCount` and the per-kind counts come from `current.json` once it answers, falling back to counts derived from the bundled listings; the hero never prints a zero placeholder while the data is still loading.

### 3.2 Dynamic Omni-Search & Category Rail
*   **Search Form:** Centered input with glassmorphism blur, leading search icon, trailing keyboard shortcut pill (`⌘K`), and clear button.
*   **Category Rail:** A wrapped chip group (not a fixed list) rendered from the live facets. `categoryFacets(listings, limit = 16)` (`web/lib/telemetry.ts`) returns the **top 16 categories by frequency**, each chip carrying its inline count, plus a leading *All categories* chip. The label header reports `{facets.length} most common of {total} entries`. There is no hard-coded category list: the chips are data-driven, so the set changes with the dataset. (Earlier drafts listed a fixed set of labels; those were illustrative, not a contract.)

### 3.3 Extension Cards
Each capability is rendered as a clean card:
*   **Header:** Upstream publisher icon, name, author link, and official verification badge.
*   **Description:** Concise 2-line summary.
*   **Metadata Badges:**
    *   Exactly two tags: type + category.
    *   Host-compatibility chips derived from registries (MCP → all 50 bridge adapters; skill → all 77 skill targets; plugin → publisher-declared).
    *   No stars: the catalog publishes no popularity figures. `publisher.verified` is a registry flag with a not-a-security-audit disclaimer (`PublisherMark.tsx`, `SiteFooter.tsx`).
*   **One-Click Copy Command:** Button that copies `litespm install <id>` to clipboard with toast feedback. Package names and commands render in monospace.

### 3.4 Package Detail Page (`/package/?slug=<key>`)
Clicking any card opens the canonical detail page (query route; `slug` when globally unique, else full `id` — see `listingHref()` in `web/lib/catalog.ts`). Sections (single scrolling page, not a modal):
1.  **Identity block:** Icon, canonical name (monospace), publisher, type + category tags, summary, `Available from` source, version.
2.  **Install with LiteSPM + Install with your agent:** The correct `litespm install <id>` command plus the one host bridge snippet for the selected host/OS (never a per-package native config as the primary path).
3.  **Agent compatibility:** Matrix with per-host status and the disclaimer that compatibility is publisher-declared or technically derivable — not a test result.
4.  **Specification:** Declared configuration surface only when known; otherwise explicit `Not published`/`Unknown` empty states (never fabricated).
5.  **Also in category:** Related entries by shared category.

---

## 4. Technical Stack & Deployment

*   **Framework (Frozen):** **Next.js 15.1.7 (App Router with `output: 'export'`, `trailingSlash: true`, `images.unoptimized`)** and React 19.
*   **Static Generation:** Query-route detail pages (`/package/?slug=`); no per-item `generateStaticParams` (5,814 items vs 20k Pages file limit — see ARCH/26 §6.3).
*   **Styling:** Tailwind CSS 3.4 with CSS variables, single theme; `tailwind-merge` + `clsx` for class composition.
*   **Component Primitives:** Local primitives only (`CountUp`, `Toast`); no Radix dependency (`web/package.json`).
*   **Icons:** Lucide React.
*   **Search Engine:** MiniSearch 7.2 in a dedicated Web Worker over the static catalog, with in-thread fallback; `date-fns` for relative recency.
*   **Zero Server Infrastructure:** The web application is 100% static. Telemetry binds to `/v1/current.json` with offline/fallback state (never a fake live indicator). Deployed alongside the static catalog on Cloudflare Pages.

---

## 5. Design System — Three Densities (J001, `DESIGNED`)

> **Status:** the tokens, type roles, motion scale, theme system and component
> splits in §5–§8 are the approved Phase-J spec (TODO `LPSM-J001`–`J009`);
> implementation is in flight. §1–§4 above keep describing the shipped tree
> until each row's acceptance evidence lands. Where §5 and §1 disagree during
> the transition, this section is the target and STATUS/TODO record the state.

The site currently speaks one visual language everywhere — the "friendly
index" register §1 set for Explore. The mistake is making every page use it.
One rule fixes it:

| Density | Used by | Language |
|---|---|---|
| **DISCOVER** — visual, spacious | home, ecosystem cards, Agent Atlas, categories | product cards, diagrams, generous type; Condensed essentially absent |
| **EVALUATE** — rich, evidence-first | package pages, agent profiles, compare, quick preview | tabs, matrices, trust/provenance blocks; plain language first, exact terms one level deeper |
| **OPERATE** — dense, precise | explore/search results, CLI surfaces, docs tables | rows, filters, mono metadata, keyboard-first |

Explore is the reference implementation of OPERATE and is **not** to be
re-styled beyond tokens, sizes and motion.

### 5.1 Tokens

| Token | Value | Where it may appear |
|---|---|---|
| `--brand` (primary) | `#3157F6` (indigo) | logo, hero graphic, primary CTAs, search focus ring, active nav, one subtle blue→violet glow behind the hero — **marketing surfaces only** |
| kind hues | MCP = electric blue, Skill = teal, Plugin = violet (the current `--mcp`/`--skill`/`--plugin` hues, unchanged) | kind badges, cards, diagram nodes — the semantic layer, never replaced by brand |
| neutrals | slightly warmer slate than today's blue-gray; rules/hairlines preserved | everything else; the catalog body stays calm — the contrast with the brand surfaces is what reads premium |

Dark theme (system / light / dark, persisted in `localStorage`, default
system; applied as `data-theme` with `prefers-color-scheme` when `system`):

| Token | Dark value |
|---|---|
| background | `#0B0E14` (never pure black) |
| surface | `#111620` |
| surface-raised | `#171D2A` |
| text | `#EDF1F7` |
| muted | `#97A3B6` |
| rules | `#283144` |
| accent | same brand indigo |

All text/control pairs — light and dark — must state their measured
contrast ratio in the implementing PR; AA is the floor (§1 already documents
a 6.6:1 check for the old accent).

### 5.2 Type roles (IBM Plex retained)

*   **IBM Plex Sans** — headings, hero and product copy (today Condensed is
    over-used here, which is a large part of the "database terminal" feel).
*   **IBM Plex Sans Condensed** — dense result rows and numeric tables only.
*   **IBM Plex Mono** — ids, versions, paths, commands, code — never prose.

### 5.3 Size & motion

| Control | Size |
|---|---|
| desktop compact (inputs, filters, chips) | 32–36 px |
| primary buttons | 40 px |
| mobile touch targets | ≥ 44 px |
| metadata text | may stay small — **actions** may not |

Motion: **120 ms** state · **160 ms** hover · **200 ms** panel/drawer ·
**240 ms** entrance, mostly ease-out (today's 90 ms linear is mechanical).
`prefers-reduced-motion` disables all of it except opacity instant cuts.
Forbidden outright: logo marquees, parallax, particles, floating blobs,
per-reload typing animations.

### 5.4 Honesty overlay (binding on every surface)

*   No invented popularity: no `popular`, `trending`, `top`, `best`,
    `🔥`, star ratings, install counts. A curated row states its selection
    rule on screen.
*   Compatibility wording never exceeds the catalog's semantics
    (publisher-declared vs LiteSPM-tested stay distinguishable).
*   Where install/runtime provenance is not trustworthy the UI says
    `Discovery only`, `Runtime not yet verified`, `Publisher-declared` —
    honesty reads as trustworthiness, not weakness.

---

## 6. Landing Page (J002, `DESIGNED`)

Section order (the wireframe, top to bottom):

1. **Header** — logo · Explore · Agents · theme toggle · search
   (⌘K palette when J009 lands; until then a route into Explore) · primary
   **Install LiteSPM** button (40 px). No nav item may link a route that
   does not exist.
2. **Hero** — headline **"Give your AI more abilities."**; sub: *Find tools,
   skills, integrations and AI agents — and see exactly what works with
   what.*; intent-first search ("What do you want your AI to do?" with
   try-chips GitHub / Browser / Databases / Files / Design / Research that
   deep-link into Explore's real query params); CTAs `[Explore
   capabilities]` `[Install LiteSPM]` + the `$ npm install -g litespm` copy
   line; then the smaller positioning line *The package manager for AI
   agents.*; then the quiet proof line (global kind counts — **data as
   proof, not headline**; the row count never leads the page again).
3. **One-bridge diagram** — inline SVG: agent names → LiteSPM box
   ("discover · install · manage") → MCP/Skills/Plugins nodes in their kind
   hues. One subtle connector-path animation on first paint (desktop),
   static simplified on mobile and under reduced motion. Not stock art, not
   3D spheres: it is the product's architecture, and it is the site's
   memorable visual.
4. **"What do you use?"** — Claude Code / Codex / OpenCode / Cursor / Cline
   / Roo Code / Other / I'm not sure; persisted in `localStorage`. State
   flow in §8.2.
5. **"What do you want your AI to do?"** — normalized user taxonomy (Code &
   Git, Browser, Files & Documents, Databases, Cloud & DevOps, Design,
   Research & Search, Communication, Productivity…) mapped onto **real**
   dataset category slugs Explore already accepts; hand-written friendly
   label + one-line description + three real example capability names + a
   count per tile. Never expose the raw 63 upstream categories here.
6. **Explore the ecosystem** — big kind cards (MCP servers / Agent skills /
   Plugins) with counts and the two-layer content of §7.1, plus the Agents
   entry point.
7. **Good places to start** — selection rule captioned on screen (e.g.
   "Selected because the publisher is official — not a popularity
   ranking"). No `popular/trending/top` anywhere.
8. **How LiteSPM works** — Find → Check → Add → Manage; one plain sentence
   each, with an inline "How this works technically →" disclosure (no new
   routes).
9. **Agent Atlas teaser** — 4–5 agent cards from `web/lib/hosts.ts` real
   data + `[Explore all agents]`.
10. **"Know what you're adding."** — trust in plain language (Where it came
    from · Who publishes it · What it can access · Whether LiteSPM tested it
    · What version), with a "Technical provenance →" expander revealing the
    real field names (sha256, manifest digest, signature).
11. **Built for developers too.** — real CLI snippets (only shipped
    commands), feature bullets (lockfiles, provenance, local-first state,
    rollback, config preservation), links to docs/architecture/GitHub.
12. **Footer.**

Positioning line (site-wide, D-026): **"The package manager for AI
agents."** with supporting *Discover and manage MCP servers, skills,
plugins, integrations and agents from one place.* The historical
"Lightweight Skill & Package Manager" expansion remains the acronym's
origin (ARCH/01), not the headline.

---

## 7. Component & Page Specs (J003–J009, `DESIGNED`)

### 7.1 Two card components, two layers (J003)

*   **`CatalogRow`** — dense search result (what Explore keeps): name, kind
    badge, one metadata line, counts; hover affordance only.
*   **`CapabilityCard`** — discovery (home, ecosystem, collections):
    logo/publisher, name, plain-language summary, `✓ Works with <selected
    agent>` badge (declared semantics), kind badge + technical label line,
    works-with chip row, `View details →`. Hover: border strengthens, card
    rises 2 px, logo scales subtly, arrow shifts 2 px (160 ms).
*   **Two layers everywhere:** nontechnical text is the default; the
    technical layer (runtime, transport, version, source, digest) sits
    behind disclosure/expand — same page, same component, different depth.

### 7.2 Explore additions (J004)

Desktop filter panel (Type · Works with · Runtime · Transport), mobile
filter bottom-sheet (≥44 px), Compact ↔ Cards toggle, and a **quick
preview**: click/select a row → right drawer (desktop) / sheet (mobile)
with summary, works-with, source, trust state, install command and `View
full page` — without navigating away. Esc closes; keyboard behavior (`/`,
arrows) preserved.

### 7.3 Package page restructure (J005)

Answer, in order: **What is this? What can it do? Will it work with what I
use? What access does it need? How do I add it?** — then tabs (Overview ·
Capabilities · Compatibility · Security · Versions · Source). The generic
host-setup block (OS/agent/config path/snippet) that currently repeats on
all 5,814 pages moves behind a single **Manual setup** disclosure. Install
panel reflects the selected agent: `✓ LiteSPM is configured for Codex`
→ `[Add GitHub]`, or `LiteSPM isn't connected yet → [Set up Codex]` (static
site: show the three-step guided flow, never fake local detection).

### 7.4 Agent Atlas & compare (J006/J007)

`/agents` becomes a discovery grid (logo, vendor, kind, capability chips,
OS, compatible counts) + per-agent profile pages with a capability matrix.
`/compare` renders agent-vs-agent. **Both draw only from real signals**
(`web/lib/hosts.ts`, `SKILL_TARGETS`, catalog `compatibleHosts`, detection
data); unknown cells render `—`, never `✗`; no "best agent" editorializing.

### 7.5 Routes & palette (J008/J009)

`/trending` → `/insights` with redirect and link sweep (the page never had
trends); `/docs` hand-built sections (no new dependencies); `/tour` with
**real captured screenshots only**. Command palette (⌘K): capabilities ·
agents · categories · docs sections; opening it is an interaction and may
fetch the dataset under the §2/E1 contract.

---

## 8. User Flows (J002, `DESIGNED`)

### 8.1 Two journeys, one site

```text
Nontechnical                          Developer
────────────                          ─────────
Hero: "Give your AI                   Header: ⌘K (J009)
  more abilities."                    Search: postgres mcp stdio
→ intent search "GitHub"              → Explore (OPERATE: rows, filters,
→ results: GitHub                       keyboard, Compact/Cards)
  plain summary first                 → quick preview → full page
→ "Works with your Codex"             → tabs: Compatibility, Source,
→ "What it needs: GitHub sign-in"       Security (EVALUATE evidence)
→ [ Add to Codex ]                    → $ litespm install … ⧉
   (guided setup if LiteSPM            → Manifest / digest / config
    not connected yet)                   bindings one level deeper

Never required to know:               Never hidden from: anything.
MCP · stdio · JSON-RPC · config.toml
SHA-256 · bridge target
```

Both flows end at the same package page; the difference is which layer is
default and which is disclosed.

### 8.2 Agent-selector state flow

```text
"What do you use?" tap
   → write localStorage (agent id)
   → badges/copy recompute over rows ALREADY in memory
       (prerendered props + any useCatalog cache)
   → sections whose data is not loaded show qualitative copy,
     never a count that would require fetching
   → dataset fetch happens only where an interaction needs rows
       (search, filter, sort, Show more, ?slug= deep link) — unchanged
```

A selector change alone must never trigger a dataset fetch (E1 contract).
