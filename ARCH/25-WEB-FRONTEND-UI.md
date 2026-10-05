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
      ├── page.tsx                    # Search-first home with curated rows + catalog grid
      ├── explore/page.tsx            # Power-filter surface (kind/category/agent/verified/sort)
      ├── package/page.tsx            # Detail via `/package/?slug=<key>` (slug or id)
      ├── agents/page.tsx             # Supported consumer hosts with readiness
      ├── categories/page.tsx         # Category index with per-kind breakdown
      └── trending/page.tsx           # Coverage/count ranking (counts, not stars)
```

---

## 3. Key UI Elements

### 3.1 Header & Dynamic Telemetry Badge
*   **Sticky Header:** Displays the LiteSPM logo, primary navigation links (**Explore**, **Agents**, **Categories**, **Coverage** → `/trending/`), kind tabs (All/MCP servers/Agent skills/Plugins with live counts), and a GitHub link.
*   **Dynamic Telemetry Eyebrow Badge:** An animated status pill dynamically bound to `/v1/current.json` (never hard-coded):
    *   **Live Capability Count:** Dynamically formatted from `current.json.itemCount` (e.g., `itemCount.toLocaleString() + " Capabilities"`).
    *   **Relative Recency:** Dynamically calculated from `current.json.createdAt` against the client clock (e.g., `formatDistanceToNow(new Date(createdAt)) + " ago"`).
    ```text
    ● {itemCount.toLocaleString()} Capabilities · Updated {formatDistanceToNow(createdAt)} ago
    ```

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
