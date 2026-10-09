"use client";

import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Search, SquareTerminal } from "lucide-react";
import { ThemeToggle } from "./ThemeToggle";

/**
 * One header for every route: logo, the primary rail, a search affordance, the
 * theme toggle and the install CTA.
 *
 * The rail only lists routes that exist (`/explore`, `/agents`, `/categories`,
 * `/trending`) — Compare, Docs and Tour are not links until they are pages.
 * The kind tabs that used to sit on a second row are gone: filtering by kind
 * is what `/explore` is for, and the discover surface traded that row for a
 * spacious hero. On narrow screens, navigation gets its own row rather than
 * being clipped between the wordmark and utility controls. `--stack-top`
 * follows that responsive height so sticky filter bars remain below the header.
 *
 * The search affordance deep-links to Explore with `?focus=search`, which
 * focuses the search field there without asking for the dataset — the fetch
 * belongs to the first keystroke, not to the click.
 */
interface HeaderProps {
  /** Fired when a rail link is followed; the home route uses it to reset. */
  onNavigate?: () => void;
  /** Lets the current page focus its search field before the deep-link updates. */
  onSearchClick?: () => void;
}

const PRIMARY_NAV = [
  { href: "/explore/", label: "Explore" },
  { href: "/agents/", label: "Agents" },
  { href: "/categories/", label: "Categories" },
  { href: "/trending/", label: "Coverage" },
];

export function Header({ onNavigate, onSearchClick }: HeaderProps) {
  const pathname = usePathname();

  return (
    <header className="sticky top-0 z-40 border-b border-rule bg-surface">
      {/* The header is opaque, not glassy. A blur layer over a ruled table
          costs paint on every scroll frame and buys nothing here. */}
      <div className="shell grid h-[92px] grid-cols-[auto_minmax(0,1fr)] grid-rows-[48px_44px] items-center gap-x-3 md:flex md:h-12 md:gap-4">
        <Link href="/" className="col-start-1 row-start-1 flex min-h-[44px] shrink-0 items-center gap-2" aria-label="LiteSPM Market home">
          {/* The logo tile is one of the six places the brand indigo is allowed. */}
          <span className="flex h-7 w-7 items-center justify-center rounded-chip bg-accent">
            <SquareTerminal className="h-4 w-4 text-white" strokeWidth={2.25} aria-hidden="true" />
          </span>
          <span className="hidden text-[15px] font-semibold tracking-tight text-ink sm:inline">
            LiteSPM Market
          </span>
        </Link>

        {/* The rail gets a full-width mobile row, then returns to the header's
            single-line layout at desktop widths. Links keep 44px touch targets. */}
        <nav
          className="no-scrollbar col-span-2 row-start-2 -mb-px flex w-full min-w-0 items-stretch self-stretch overflow-x-auto md:col-auto md:row-auto md:w-auto md:flex-1"
          aria-label="Primary"
        >
          {PRIMARY_NAV.map((item) => {
            const active = pathname?.startsWith(item.href.replace(/\/$/, ""));
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? "page" : undefined}
                onClick={onNavigate}
                className={`flex shrink-0 items-center border-b-2 px-2 text-[13px] font-medium transition-colors duration-state ease-out md:px-2.5 ${
                  active
                    ? "border-accent text-ink"
                    : "border-transparent text-ink-2 hover:border-rule-2 hover:text-ink"
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>

        <div className="col-start-2 row-start-1 flex shrink-0 items-center gap-2">
          <Link
            href="/explore/?focus=search"
            onClick={onSearchClick}
            className="btn btn-icon"
            aria-label="Search the catalog"
            title="Search the catalog"
          >
            <Search className="h-4 w-4" aria-hidden="true" />
          </Link>
          <ThemeToggle />
          <Link href="/#install" className="btn btn-solid btn-lg whitespace-nowrap">
            <span className="hidden sm:inline">Install LiteSPM</span>
            <span className="sm:hidden">Install</span>
          </Link>
        </div>
      </div>
    </header>
  );
}
