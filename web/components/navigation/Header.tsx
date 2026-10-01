"use client";

import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Terminal, Github, Sparkles, Box, Code } from "lucide-react";

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
}

const PRIMARY_NAV = [
  { href: "/explore/", label: "Explore" },
  { href: "/agents/", label: "Agents" },
  { href: "/categories/", label: "Categories" },
  { href: "/trending/", label: "Trending" },
];

const KIND_TABS: Array<{ id: string; label: string; icon?: React.ElementType; tone: string; key: keyof HeaderCounts }> = [
  { id: "all", label: "All", tone: "", key: "all" },
  { id: "mcp", label: "MCP Servers", icon: Code, tone: "text-blue-600", key: "mcp" },
  { id: "skill", label: "Agent Skills", icon: Sparkles, tone: "text-purple-600", key: "skill" },
  { id: "plugin", label: "Plugins", icon: Box, tone: "text-amber-600", key: "plugin" },
];

export function Header({ activeTab = "all", setActiveTab, counts }: HeaderProps) {
  const pathname = usePathname();
  const showKindTabs = Boolean(setActiveTab && counts);

  return (
    <header className="sticky top-0 z-40 w-full border-b border-slate-200/80 bg-white/85 backdrop-blur-md">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 py-3 lg:px-8">
        <Link href="/" className="group flex shrink-0 items-center gap-2.5" aria-label="LitePSM Market home">
          <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-slate-900 shadow-md shadow-slate-900/10 transition-transform group-hover:scale-105">
            <Terminal className="h-4 w-4 text-emerald-400 stroke-[2.5]" aria-hidden="true" />
          </span>
          <span className="hidden text-lg font-black tracking-tight text-slate-900 sm:inline">LitePSM</span>
        </Link>

        <nav className="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto sm:justify-center" aria-label="Primary">
          {PRIMARY_NAV.map((item) => {
            const active = pathname?.startsWith(item.href.replace(/\/$/, "")) && item.href !== "/";
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`shrink-0 rounded-xl px-3 py-1.5 text-sm font-medium transition-colors ${
                  active ? "bg-slate-100 text-slate-900" : "text-slate-600 hover:bg-slate-100 hover:text-slate-900"
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
          className="flex shrink-0 items-center gap-2 rounded-xl border border-slate-200 bg-white px-3 py-1.5 text-xs font-semibold text-slate-700 shadow-sm transition-all hover:bg-slate-50 hover:text-slate-900"
        >
          <Github className="h-4 w-4 text-slate-700" aria-hidden="true" />
          <span className="hidden sm:inline">GitHub</span>
        </a>
      </div>

      {showKindTabs && (
        <div className="border-t border-slate-100 bg-white/60">
          <nav
            className="no-scrollbar mx-auto flex max-w-7xl items-center gap-1 overflow-x-auto px-4 py-1.5 lg:px-8"
            aria-label="Capability kind"
          >
            {KIND_TABS.map((tab) => {
              const Icon = tab.icon;
              const selected = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => setActiveTab?.(tab.id)}
                  className={`flex shrink-0 items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-semibold transition-all ${
                    selected ? "bg-slate-900 text-white shadow-sm" : "text-slate-600 hover:text-slate-900"
                  }`}
                >
                  {Icon && <Icon className={`h-3.5 w-3.5 ${selected ? "text-emerald-400" : tab.tone}`} aria-hidden="true" />}
                  <span>{tab.label}</span>
                  <span className={`font-mono text-[10px] ${selected ? "text-slate-300" : "text-slate-400"}`}>
                    ({(counts![tab.key] ?? 0).toLocaleString()})
                  </span>
                </button>
              );
            })}
          </nav>
        </div>
      )}
    </header>
  );
}
