import type { Metadata } from "next";
import { IBM_Plex_Sans, IBM_Plex_Sans_Condensed, IBM_Plex_Mono } from "next/font/google";
import "./globals.css";
import { ToastViewport } from "../components/ui/Toast";
import { SITE_URL } from "../lib/site";

/**
 * IBM Plex: an engineered grotesque with squared terminals, which reads as
 * infrastructure software rather than startup. The condensed cut carries table
 * row names so 63-character capability names stay legible in a narrow column.
 * Mono is reserved for content that is genuinely machine-readable.
 */
const sans = IBM_Plex_Sans({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-sans",
  display: "swap",
});

const cond = IBM_Plex_Sans_Condensed({
  subsets: ["latin"],
  weight: ["600"],
  variable: "--font-cond",
  display: "swap",
});

const mono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-mono",
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: "LiteSPM Market — The Lightweight Skill & Package Manager for AI Agents",
    template: "%s · LiteSPM Market",
  },
  description:
    "The Lightweight Skill & Package Manager for AI Agents — a static index of MCP servers, portable agent skills, and plugins. Browse 5,000+ capabilities by kind, category, publisher, runtime and agent host, and install them through a single LiteSPM bridge entry.",
  applicationName: "LiteSPM Market",
  keywords: [
    "MCP",
    "Model Context Protocol",
    "agent skills",
    "SKILL.md",
    "plugins",
    "LiteSPM",
    "capability registry",
    "Claude Code",
    "Codex",
    "OpenCode",
  ],
  openGraph: {
    type: "website",
    siteName: "LiteSPM Market",
    title: "LiteSPM Market — The Lightweight Skill & Package Manager for AI Agents",
    description:
      "Browse MCP servers, agent skills, and plugins by kind, category, publisher, runtime and agent host.",
    url: SITE_URL,
  },
  twitter: {
    card: "summary",
    title: "LiteSPM Market",
    description: "A static capability index for coding agents.",
  },
  robots: { index: true, follow: true },
  // app/icon.svg supplies the icon; a separate /favicon.ico request 404s.
  icons: { icon: [{ url: "/icon.svg", type: "image/svg+xml" }] },
};

export const viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f5f7fb" },
    { media: "(prefers-color-scheme: dark)", color: "#0b0e14" },
  ],
};

/**
 * Theme resolution, run before first paint so the page never flashes the wrong
 * surface. It only ever writes `data-theme` on <html>: "system" (the default,
 * and what an unset attribute means) is resolved by CSS through
 * `prefers-color-scheme`, "light" and "dark" pin it. The choice itself lives
 * in `localStorage["litespm-theme"]` — no cookie, no server, no dependency.
 *
 * Kept as a plain string rather than an imported module so it is inlined in the
 * HTML: a deferred script would run after the first frame and defeat it.
 */
const THEME_INIT = `(function(){try{var t=localStorage.getItem("litespm-theme");document.documentElement.dataset.theme=(t==="light"||t==="dark")?t:"system";}catch(e){document.documentElement.dataset.theme="system";}})();`;

/**
 * Site-level structured data. This lives in the layout rather than on the
 * package page because `/package/` resolves its entry from `?slug=` in the
 * browser, so anything keyed to one entry cannot be prerendered. Stating the
 * site once, truthfully, is what a crawler can actually verify.
 */
const SITE_JSON_LD = {
  "@context": "https://schema.org",
  "@type": "WebSite",
  name: "LiteSPM Market",
  url: SITE_URL,
  description:
    "A static index of MCP servers, portable agent skills, and plugins for AI coding agents.",
  potentialAction: {
    "@type": "SearchAction",
    target: `${SITE_URL}/explore/?q={search_term_string}`,
    "query-input": "required name=search_term_string",
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    // `suppressHydrationWarning` because the theme script below writes
    // `data-theme` before React hydrates; that attribute is deliberately not
    // rendered on the server, which has no localStorage to read.
    <html
      lang="en"
      className={`${sans.variable} ${cond.variable} ${mono.variable}`}
      suppressHydrationWarning
    >
      <body className="min-h-screen bg-paper text-ink">
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT }} />
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(SITE_JSON_LD) }}
        />
        {/* Skip link: the header is 12rem of tab stops before the first
            content on every page. */}
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-50 focus:rounded-ctl focus:bg-ink focus:px-3 focus:py-3 focus:text-[13px] focus:text-surface"
        >
          Skip to content
        </a>
        {children}
        <ToastViewport />
      </body>
    </html>
  );
}
