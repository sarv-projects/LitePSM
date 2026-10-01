"use client";

import React from "react";
import Link from "next/link";
import { Terminal, Github, Sparkles, Box, Layers, Code } from "lucide-react";

interface HeaderProps {
  activeTab: string;
  setActiveTab: (tab: string) => void;
  counts?: {
    all: number;
    mcp: number;
    skill: number;
    plugin: number;
  };
}

export function Header({
  activeTab,
  setActiveTab,
  counts = { all: 5185, mcp: 4079, skill: 1103, plugin: 3 },
}: HeaderProps) {
  return (
    <header className="sticky top-0 z-40 w-full bg-white/80 backdrop-blur-md border-b border-slate-200/80 px-4 lg:px-8 py-3 transition-colors">
      <div className="max-w-7xl mx-auto flex items-center justify-between gap-4">
        {/* Brand */}
        <Link
          href="/"
          onClick={() => setActiveTab("all")}
          className="flex items-center gap-3 cursor-pointer group"
        >
          <div className="w-9 h-9 rounded-xl bg-slate-900 flex items-center justify-center shadow-md shadow-slate-900/10 group-hover:scale-105 transition-transform">
            <Terminal className="w-4 h-4 text-emerald-400 stroke-[2.5]" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-lg font-black tracking-tight text-slate-900 font-sans">
              LitePSM
            </span>
            <span className="text-[11px] px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-700 font-mono font-semibold border border-emerald-200/80">
              v0.1.0
            </span>
          </div>
        </Link>

        {/* Center Kind Filter Tabs */}
        <nav className="flex items-center gap-1 bg-slate-100/80 p-1 rounded-2xl border border-slate-200/80 overflow-x-auto no-scrollbar max-w-[65%] sm:max-w-none">
          <button
            onClick={() => setActiveTab("all")}
            className={`px-3.5 py-1.5 text-xs font-semibold rounded-xl transition-all ${
              activeTab === "all"
                ? "bg-white text-slate-900 shadow-sm"
                : "text-slate-600 hover:text-slate-900"
            }`}
          >
            All ({counts.all.toLocaleString()})
          </button>

          <button
            onClick={() => setActiveTab("mcp")}
            className={`flex items-center gap-1.5 px-3.5 py-1.5 text-xs font-semibold rounded-xl transition-all ${
              activeTab === "mcp"
                ? "bg-white text-slate-900 shadow-sm"
                : "text-slate-600 hover:text-slate-900"
            }`}
          >
            <Code className="w-3.5 h-3.5 text-blue-600" />
            MCP Servers ({counts.mcp.toLocaleString()})
          </button>

          <button
            onClick={() => setActiveTab("skill")}
            className={`flex items-center gap-1.5 px-3.5 py-1.5 text-xs font-semibold rounded-xl transition-all ${
              activeTab === "skill"
                ? "bg-white text-slate-900 shadow-sm"
                : "text-slate-600 hover:text-slate-900"
            }`}
          >
            <Sparkles className="w-3.5 h-3.5 text-purple-600" />
            Agent Skills ({counts.skill.toLocaleString()})
          </button>

          <button
            onClick={() => setActiveTab("plugin")}
            className={`flex items-center gap-1.5 px-3.5 py-1.5 text-xs font-semibold rounded-xl transition-all ${
              activeTab === "plugin"
                ? "bg-white text-slate-900 shadow-sm"
                : "text-slate-600 hover:text-slate-900"
            }`}
          >
            <Box className="w-3.5 h-3.5 text-amber-600" />
            Plugins ({counts.plugin})
          </button>
        </nav>

        {/* Right Actions */}
        <div className="flex items-center gap-3">
          <a
            href="https://github.com/sarv-projects/LitePSM"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 px-3.5 py-1.5 rounded-xl bg-white hover:bg-slate-50 text-xs font-semibold text-slate-700 hover:text-slate-900 border border-slate-200 shadow-sm transition-all"
          >
            <Github className="w-4 h-4 text-slate-700" />
            <span className="hidden sm:inline">GitHub</span>
          </a>
        </div>
      </div>
    </header>
  );
}
