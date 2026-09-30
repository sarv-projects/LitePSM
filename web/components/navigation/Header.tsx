"use client";

import React from "react";
import { Terminal, Shield, Sparkles, Github, Layers, BookOpen } from "lucide-react";

interface HeaderProps {
  activeTab: string;
  setActiveTab: (tab: string) => void;
}

export function Header({ activeTab, setActiveTab }: HeaderProps) {
  return (
    <header className="sticky top-0 z-40 w-full glass-panel border-b border-[#232734] px-4 lg:px-8 py-3.5">
      <div className="max-w-7xl mx-auto flex items-center justify-between">
        {/* Brand */}
        <div className="flex items-center gap-3 cursor-pointer" onClick={() => setActiveTab("all")}>
          <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-emerald-400 to-teal-600 flex items-center justify-center shadow-lg shadow-emerald-500/20">
            <Terminal className="w-4 h-4 text-black stroke-[2.5]" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-lg font-bold tracking-tight text-white font-mono">LitePSM</span>
            <span className="text-xs px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-400 font-mono border border-emerald-500/20">
              v0.1.0
            </span>
          </div>
        </div>

        {/* Center Nav Links */}
        <nav className="hidden md:flex items-center gap-1 bg-[#11131a]/80 p-1 rounded-lg border border-[#232734]">
          <button
            onClick={() => setActiveTab("mcp")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-all ${
              activeTab === "mcp"
                ? "bg-emerald-500 text-black shadow font-semibold"
                : "text-gray-400 hover:text-white"
            }`}
          >
            MCP Servers
          </button>
          <button
            onClick={() => setActiveTab("skill")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-all ${
              activeTab === "skill"
                ? "bg-emerald-500 text-black shadow font-semibold"
                : "text-gray-400 hover:text-white"
            }`}
          >
            Agent Skills
          </button>
          <button
            onClick={() => setActiveTab("plugin")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-all ${
              activeTab === "plugin"
                ? "bg-emerald-500 text-black shadow font-semibold"
                : "text-gray-400 hover:text-white"
            }`}
          >
            Plugins
          </button>
        </nav>

        {/* Right Actions */}
        <div className="flex items-center gap-3">
          <a
            href="https://github.com/sarv-projects/LitePSM"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#171a23] hover:bg-[#232734] text-xs font-medium text-gray-300 hover:text-white border border-[#232734] transition-all"
          >
            <Github className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">GitHub</span>
            <span className="px-1.5 py-0.2 rounded bg-black/40 text-[10px] text-emerald-400 font-mono">
              ★ 1.4k
            </span>
          </a>
        </div>
      </div>
    </header>
  );
}
