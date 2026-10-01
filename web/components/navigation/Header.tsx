"use client";

import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Github, SquareTerminal } from "lucide-react";
import { formatCount } from "../../lib/format";

export interface HeaderCounts {
  all: number;
  mcp: number;
  skill: number;
  plugin: number;
}

interface HeaderProps {
  activeTab?: string;
  setActiveTab?: (tab: string) => void;
  counts?: HeaderCounts;
  /** Total distinct host names, so kind-tab coverage reads as "n of N". */
  onNavigate?: () => void;
}

const PRIMARY_NAV = [
  { href: "/explore/", label: "Explore" },
  { href: "/agents/", label: "Agents" },
  { href: "/categories/", label: "Categories" },
  { href: "/trending/", label: "Leaderboards" },
];

const KIND_TABS: Array<{ id: string; label: string; key: keyof HeaderCounts }> = [
  { id: "all", label: "All", key: "all" },
  { id: "mcp", label: "MCP servers", key: "mcp" },
  { id: "skill", label: "Agent skills", key: "skill" },
  { id: "plugin", label: "Plugins", key: "plugin" },
];

export function Header({ activeTab = "all", setActiveTab, counts, onNavigate }: HeaderProps) {
  const pathname = usePathname();
  const showKindTabs = Boolean(setActiveTab && counts);

  return (
    <header className="sticky top-0 z-40 border-b border-rule bg-surface">
      {/* The header is opaque, not glassy. A blur layer over a ruled table
          costs paint on every scroll frame and buys nothing here. */}
      <div className="shell flex h-12 items-center gap-4">
        <Link href="/" className="flex shrink-0 items-center gap-2" aria-label="LitePSM Market home">
          <span className="flex h-6 w-6 items-center justify-center rounded-chip bg-ink">
            <SquareTerminal className="h-3.5 w-3.5 text-surface" strokeWidth={2.25} aria-hidden="true" />
          </span>
          <span className="t-cond hidden text-[15px] font-semibold tracking-tight text-ink sm:inline">
            LitePSM Market
          </span>
        </Link>

        {/* The rail scrolls on narrow viewports. Linked labels need the room
            more than the wordmark does, so the wordmark yields first. */}
        <nav className="no-scrollbar -mb-px flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto" aria-label="Primary">
          {PRIMARY_NAV.map((item) => {
            const active = pathname?.startsWith(item.href.replace(/\/$/, ""));
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? "page" : undefined}
                onClick={onNavigate}
                className={`shrink-0 border-b-2 px-2.5 py-1 text-[13px] font-medium leading-5 transition-colors ${
                  active
                    ? "border-ink text-ink"
                    : "border-transparent text-ink-2 hover:border-rule-2 hover:text-ink"
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>

        <a
          href="https://github.com/sarv-projects/LitePSM"
          target="_blank"
          rel="noopener noreferrer"
          className="btn hidden shrink-0 lg:inline-flex"
        >
          <Github className="h-3.5 w-3.5" aria-hidden="true" />
          Repository
        </a>
      </div>

      {showKindTabs && (
        <div className="border-t border-rule bg-sunken">
          <nav className="no-scrollbar shell flex items-center gap-1.5 overflow-x-auto py-1.5" aria-label="Capability kind">
            {KIND_TABS.map((tab) => {
              const selected = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => setActiveTab?.(tab.id)}
                  className="chip"
                  data-selected={selected || undefined}
                >
                  {tab.id !== "all" && (
                    <span
                      aria-hidden="true"
                      className="h-2.5 w-[3px] rounded-[1px]"
                      style={{ backgroundColor: `var(--${tab.id})` }}
                    />
                  )}
                  <span>{tab.label}</span>
                  <span className="chip-count t-tabular">{formatCount(counts![tab.key] ?? 0)}</span>
                </button>
              );
            })}
          </nav>
        </div>
      )}
    </header>
  );
}
