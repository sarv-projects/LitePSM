# Web Frontend & UI Architecture (LitePSM Market)

Inspired by the design of `mcpmarket.com`, LitePSM Market is a static web application that serves as the visual discovery interface for AI agent plugins, MCP servers, and skills.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                   LitePSM Market Web Architecture                      │
│                                                                        │
│   Next.js 15 / React 19 Static Export (Tailwind CSS + Radix UI)       │
│                               │                                        │
│   Static Client Search ◄──────┴──────► Immutable Static Releases       │
│   (MiniSearch in Web Worker)           (Cloudflare Pages /v1/releases/)│
└────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Design Vision & Aesthetic Standards

LitePSM Market adheres to an engineering-focused, high-performance aesthetic:
*   **Color Palette & Theme:** Dark and light modes with high-contrast neutral scales (Geist Mono and Inter typography), subtle CSS dither masks, and glassmorphic borders (`border-[var(--design-line)]`).
*   **Instant Responsiveness:** Zero layout shifts, instant client-side filtering, and sub-100ms fuzzy search powered by an in-memory index loaded in a Web Worker.
*   **Keyboard-First Navigation:** Pressing `Cmd+K` or `/` instantly focuses the global omni-search palette.

---

## 2. Page Sections & Component Hierarchy

```text
web/
  ├── components/
  │   ├── navigation/
  │   │   ├── Header.tsx              # Sticky navigation, logo, links, theme switch
  │   │   └── SearchBar.tsx           # Global omni-search input with shortcut hint
  │   ├── hero/
  │   │   ├── HeroSection.tsx         # Live count badge, animated headline, search
  │   │   └── CategoryRail.tsx        # Horizontal scrolling category filter pills
  │   ├── catalog/
  │   │   ├── ExtensionGrid.tsx       # Responsive responsive grid (1 to 3 columns)
  │   │   ├── ExtensionCard.tsx       # Card with tags, stars, verified badges, copy
  │   │   └── DetailDrawer.tsx        # Slide-over panel with README, tools, schemas
  │   └── ui/                         # Radix UI primitives (Dialog, Tabs, Tooltip, Sheet)
  └── pages/ / app/
      ├── layout.tsx
      ├── page.tsx                    # Main marketplace home
      └── server/[id]/page.tsx        # Deep-linkable item detail page
```

---

## 3. Key UI Elements

### 3.1 Header & Dynamic Telemetry Badge
*   **Sticky Header:** Displays the LitePSM logo, primary navigation links (**MCP Servers**, **Agent Skills**, **Plugins**, **Docs**), and a GitHub link.
*   **Dynamic Telemetry Eyebrow Badge:** An animated status pill dynamically bound to `/v1/current.json` (never hard-coded):
    *   **Live Capability Count:** Dynamically formatted from `current.json.itemCount` (e.g., `itemCount.toLocaleString() + " Capabilities"`).
    *   **Relative Recency:** Dynamically calculated from `current.json.createdAt` against the client clock (e.g., `formatDistanceToNow(new Date(createdAt)) + " ago"`).
    ```text
    ● {itemCount.toLocaleString()} Capabilities · Updated {formatDistanceToNow(createdAt)} ago
    ```

### 3.2 Dynamic Omni-Search & Category Rail
*   **Search Form:** Centered input with glassmorphism blur, leading search icon, trailing keyboard shortcut pill (`⌘K`), and clear button.
*   **Category Rail:** Horizontally scrollable pill rail featuring categories:
    *   *All*, *Developer Tools*, *Data Science & ML*, *API Development*, *Database Management*, *Browser Automation*, *Productivity*, *Security & Testing*, *DevOps*, *Official*.

### 3.3 Extension Cards
Each capability is rendered as a clean card:
*   **Header:** Upstream publisher icon, name, author link, and official verification badge.
*   **Description:** Concise 2-line summary.
*   **Metadata Badges:**
    *   `[MCP: stdio]` or `[MCP: Streamable HTTP]` or `[Agent Skill]`.
    *   `★ 1.2k` (Upstream GitHub stars).
    *   `[Tested: Cline, Pi, Grok]` (Host compatibility indicators).
*   **One-Click Copy Command:** Button that copies `litepsm install <id>` to clipboard with visual tooltip feedback.

### 3.4 Deep Inspection Detail Drawer (Modal / Sheet)
Clicking any card opens an accessible slide-over sheet or dedicated page with 4 interactive tabs:
1.  **Overview / README:** Sanitized, syntax-highlighted Markdown description from the upstream source.
2.  **Tools & Schema:** Interactive tree showing all exposed MCP tools, parameter types, required flags, and the canonical `schemaFingerprint`.
3.  **Permissions & Security:** Transparent disclosure of declared filesystem effects, outbound network domains, and required secrets.
4.  **Host Connect:** Dropdown allowing user to select **Cline**, **Pi Agent**, **Grok Build**, or **Claude Code**, which instantly displays the exact setup command or config snippet.

---

## 4. Technical Stack & Deployment

*   **Framework (Frozen):** **Next.js 15 (App Router with `output: 'export'`)** and React 19.
*   **Static Generation:** Item pages use `generateStaticParams()` reading build-time catalog shards, with client-side fallback fetching from `/v1/releases/<release-id>/items/<id>.json`.
*   **Styling:** Tailwind CSS v4 with CSS variables for dark/light themes.
*   **Component Primitives:** Radix UI primitives (Dialog, Tabs, Tooltip, Sheet).
*   **Icons:** Lucide React.
*   **Search Engine:** Client-side Web Worker running MiniSearch over `index.json`.
*   **Zero Server Infrastructure:** The web application is 100% static. It loads `/v1/current.json` and fetches shards on demand. Deployed alongside the static catalog on Cloudflare Pages.
