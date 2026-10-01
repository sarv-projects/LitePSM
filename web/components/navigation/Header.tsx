"use client";

import React from "react";
import Link from "next/link";
import { Terminal, Github, Sparkles, Box, Code } from "lucide-react";

export interface HeaderCounts {
  all: number;
  mcp: number;
  skill: number;
  plugin: number;
}

interface HeaderProps {
  activeTab: string;
  setActiveTab: (tab: string) => void;
  counts: HeaderCounts;
}

const TABS: Array<{ id: string; label: string; icon?: React.ElementType; tone: string; countKey: keyof HeaderCounts }> = [
  { id: "all", label: "All", tone: "", countKey: "all" },
  { id: "mcp", label: "MCP Servers", icon: Code, tone: "text-blue-600", countKey: "mcp" },
  { id: "skill", label: "Agent Skills", icon: Sparkles, tone: "text-purple-600", countKey: "skill" },
  { id: "plugin", label: "Plugins", icon: Box, tone: "text-amber-600", countKey: "plugin" },
];

export function Header({ activeTab, setActiveTab, counts }: HeaderProps) {
  return (
    <header className="sticky top-0 z-40 w-full border-b border-slate-200/80 bg-white/80 px-4 py-3 backdrop-blur-md transition-colors lg:px-8">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-3">
        {/* Brand */}
        <Link
          href="/"
          onClick={() => setActiveTab("all")}
          className="group flex shrink-0 items-center gap-2.5"
          aria-label="LitePSM Market home"
        >
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-slate-900 shadow-md shadow-slate-900/10 transition-transform group-hover:scale-105">
            <Terminal className="h-4 w-4 text-emerald-400 stroke-[2.5]" aria-hidden="true" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="hidden text-lg font-black tracking-tight text-slate-900 sm:inline">LitePSM</span>
            <span className="hidden rounded-full border border-emerald-200/80 bg-emerald-50 px-2 py-0.5 font-mono text-[11px] font-semibold text-emerald-700 sm:inline">
              v0.1.0
            </span>
          </div>
        </Link>

        {/* Kind filter tabs */}
        <nav
          className="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto rounded-2xl border border-slate-200/80 bg-slate-100/80 p-1 sm:justify-center"
          aria-label="Capability kind"
        >
          {TABS.map((tab) => {
            const Icon = tab.icon;
            const selected = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                aria-pressed={selected}
                onClick={() => setActiveTab(tab.id)}
                className={`flex shrink-0 items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-semibold transition-all ${
                  selected ? "bg-white text-slate-900 shadow-sm" : "text-slate-600 hover:text-slate-900"
                }`}
              >
                {Icon && <Icon className={`h-3.5 w-3.5 ${tab.tone}`} aria-hidden="true" />}
                <span>{tab.label}</span>
                <span className="hidden font-mono text-[10px] text-slate-400 sm:inline">
                  ({(counts[tab.countKey] ?? 0).toLocaleString()})
                </span>
              </button>
            );
          })}
        </nav>

        {/* Actions */}
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
    </header>
  );
}
