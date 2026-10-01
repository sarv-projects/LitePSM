"use client";

import React, { useState, useEffect, Suspense, useMemo } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import {
  ArrowLeft,
  Copy,
  Check,
  Star,
  ShieldCheck,
  ExternalLink,
  Terminal,
  Sparkles,
  Box,
  Code2,
  Shield,
  Layers,
  FileCode,
  FolderOpen,
  ChevronRight,
  Info
} from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { ExtensionItem } from "../../components/catalog/ExtensionCard";
import catalogData from "../../data/catalog.json";

function ItemDetailContent() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const itemId = searchParams.get("id");

  const items = catalogData as ExtensionItem[];
  const item = useMemo(() => {
    if (!itemId) return items[0];
    return items.find((i) => i.id === itemId) || items[0];
  }, [itemId, items]);

  const [activeHost, setActiveHost] = useState<string>("claude-code");
  const [platformOs, setPlatformOs] = useState<"win" | "mac" | "linux">("win");
  const [snippetMode, setSnippetMode] = useState<"bridge" | "native">("bridge");
  const [copiedPath, setCopiedPath] = useState(false);
  const [copiedSnippet, setCopiedSnippet] = useState(false);
  const [copiedInstall, setCopiedInstall] = useState(false);

  // Auto-detect OS in browser if possible
  useEffect(() => {
    if (typeof window !== "undefined" && window.navigator) {
      const ua = window.navigator.userAgent.toLowerCase();
      if (ua.includes("mac")) setPlatformOs("mac");
      else if (ua.includes("linux")) setPlatformOs("linux");
      else setPlatformOs("win");
    }
  }, []);

  const hosts = [
    { id: "claude-code", name: "Claude Code", ext: "CLI", type: "json" },
    { id: "codex", name: "OpenAI Codex", ext: "Terminal", type: "toml" },
    { id: "opencode", name: "OpenCode", ext: "CLI", type: "json" },
    { id: "cursor", name: "Cursor", ext: "Editor", type: "json" },
    { id: "cline", name: "Cline", ext: "VS Code", type: "json" },
    { id: "claude-desktop", name: "Claude Desktop", ext: "Desktop", type: "json" },
    { id: "pi-agent", name: "Pi Agent", ext: "Terminal", type: "json" },
    { id: "grok-build", name: "Grok Build", ext: "Terminal", type: "toml" },
  ];

  // Resolve exact config file path based on host and OS
  const getResolvedConfigPath = (hostId: string, os: "win" | "mac" | "linux") => {
    switch (hostId) {
      case "cline":
        if (os === "win") return "%APPDATA%\\Code\\User\\globalStorage\\saoudrizwan.claude-dev\\settings\\cline_mcp_settings.json";
        if (os === "mac") return "~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json";
        return "~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json";
      case "cursor":
        return os === "win" ? "%USERPROFILE%\\.cursor\\mcp.json" : "~/.cursor/mcp.json";
      case "claude-desktop":
        if (os === "win") return "%APPDATA%\\Claude\\claude_desktop_config.json";
        if (os === "mac") return "~/Library/Application Support/Claude/claude_desktop_config.json";
        return "~/.config/Claude/claude_desktop_config.json";
      case "pi-agent":
        return os === "win" ? "%USERPROFILE%\\.pi\\agent\\mcp.json" : "~/.pi/agent/mcp.json";
      case "grok-build":
        return os === "win" ? "%USERPROFILE%\\.grok\\config.toml" : "~/.grok/config.toml";
      case "codex":
        return os === "win" ? "%USERPROFILE%\\.codex\\config.toml" : "~/.codex/config.toml";
      case "claude-code":
        return os === "win" ? "%USERPROFILE%\\.claude.json" : "~/.claude.json";
      case "opencode":
        return os === "win" ? "%APPDATA%\\OpenCode\\opencode.json" : "~/.config/opencode/opencode.json";
      default:
        return "litepsm.config.json";
    }
  };

  // Generate exact ready-to-fill snippet
  const getGeneratedSnippet = () => {
    if (!item) return "";
    const isToml = activeHost === "grok-build" || activeHost === "codex";

    if (item.kind === "skill") {
      if (activeHost === "claude-code") {
        return `// Copy into ~/.claude/skills/${item.slug}/SKILL.md\n# ${item.name}\n\n${item.summary}\n\n<!-- Installed via litepsm install ${item.id} -->`;
      }
      return `// Universal Skill Directory:\n// Project: .agents/skills/${item.slug}/SKILL.md\n// Global:  ~/.gemini/config/skills/${item.slug}/SKILL.md\n\n# Direct Setup Command:\nlitepsm install ${item.id}`;
    }

    if (snippetMode === "bridge") {
      // LitePSM Bridge Managed Mode
      if (isToml) {
        return `# Generated for ${activeHost.toUpperCase()} via LitePSM Stdio Bridge\n[mcp_servers.${item.slug.replace(/[-.]/g, "_")}]\ncommand = "litepsm"\nargs = ["bridge", "stdio", "--host", "${activeHost}"]`;
      }
      if (activeHost === "opencode") {
        return `{\n  "mcp": {\n    "servers": {\n      "${item.slug}": {\n        "command": "litepsm",\n        "args": ["bridge", "stdio", "--host", "opencode"]\n      }\n    }\n  }\n}`;
      }
      return `{\n  "mcpServers": {\n    "${item.slug}": {\n      "command": "litepsm",\n      "args": ["bridge", "stdio", "--host", "${activeHost}"]\n    }\n  }\n}`;
    } else {
      // Direct Native Mode
      const cmd = item.command || "npx";
      const args = item.args || ["-y", `@modelcontextprotocol/server-${item.slug}`];

      if (isToml) {
        const formattedArgs = args.map((a) => `"${a}"`).join(", ");
        return `# Standalone Native Configuration\n[mcp_servers.${item.slug.replace(/[-.]/g, "_")}]\ncommand = "${cmd}"\nargs = [${formattedArgs}]`;
      }
      if (activeHost === "opencode") {
        return JSON.stringify(
          {
            mcp: {
              servers: {
                [item.slug]: {
                  command: cmd,
                  args: args,
                },
              },
            },
          },
          null,
          2
        );
      }
      return JSON.stringify(
        {
          mcpServers: {
            [item.slug]: {
              command: cmd,
              args: args,
            },
          },
        },
        null,
        2
      );
    }
  };

  const copyConfigPath = () => {
    navigator.clipboard.writeText(getResolvedConfigPath(activeHost, platformOs));
    setCopiedPath(true);
    setTimeout(() => setCopiedPath(false), 2000);
  };

  const copySnippet = () => {
    navigator.clipboard.writeText(getGeneratedSnippet());
    setCopiedSnippet(true);
    setTimeout(() => setCopiedSnippet(false), 2000);
  };

  const copyInstallCommand = () => {
    navigator.clipboard.writeText(`litepsm install ${item.id}`);
    setCopiedInstall(true);
    setTimeout(() => setCopiedInstall(false), 2000);
  };

  // Find related capabilities
  const relatedItems = useMemo(() => {
    return items
      .filter((i) => i.id !== item.id && (i.category === item.category || i.kind === item.kind))
      .slice(0, 3);
  }, [items, item]);

  return (
    <div className="min-h-screen flex flex-col justify-between bg-[#f0f2f6] text-slate-800">
      <div>
        <Header activeTab="all" setActiveTab={() => router.push("/")} />

        {/* Breadcrumb Bar */}
        <div className="w-full bg-white/70 backdrop-blur-md border-b border-slate-200/80 px-4 lg:px-8 py-3">
          <div className="max-w-6xl mx-auto flex items-center justify-between text-xs text-slate-500 font-mono">
            <div className="flex items-center gap-2">
              <Link
                href="/"
                className="flex items-center gap-1.5 text-slate-600 hover:text-emerald-600 transition-colors font-sans font-medium"
              >
                <ArrowLeft className="w-3.5 h-3.5" /> Back to Catalog
              </Link>
              <span className="text-slate-300">/</span>
              <span className="capitalize">{item.kind}</span>
              <span className="text-slate-300">/</span>
              <span className="text-slate-900 font-semibold">{item.name}</span>
            </div>

            <div className="hidden sm:flex items-center gap-2">
              <span className="text-[11px] text-slate-400">ID:</span>
              <code className="bg-slate-100 px-2 py-0.5 rounded text-slate-600 border border-slate-200">
                {item.id}
              </code>
            </div>
          </div>
        </div>

        {/* Main Content Container */}
        <main className="max-w-6xl mx-auto px-4 lg:px-8 py-8 space-y-8">
          {/* Hero Capsule */}
          <div className="bg-white rounded-3xl p-6 sm:p-8 border border-slate-200/80 shadow-[0_4px_20px_-4px_rgba(15,23,42,0.05)] relative overflow-hidden">
            {/* Top dither strip */}
            <div className="card-dither-strip absolute top-0 left-0 right-0 h-1.5" />

            <div className="flex flex-col md:flex-row md:items-start justify-between gap-6 pt-2">
              <div className="space-y-3 max-w-3xl">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="px-2.5 py-1 rounded-full text-xs font-mono font-medium bg-emerald-50 text-emerald-700 border border-emerald-200/80 uppercase">
                    {item.kind}
                  </span>
                  <span className="text-xs font-mono text-slate-500 bg-slate-100 px-2.5 py-1 rounded-full border border-slate-200">
                    {item.category}
                  </span>
                  {item.runtime && (
                    <span className="text-xs font-mono text-slate-500 bg-slate-50 px-2.5 py-1 rounded-full border border-slate-200">
                      Runtime: {item.runtime}
                    </span>
                  )}
                  <span className="text-xs font-mono text-slate-400 bg-slate-50 px-2 py-1 rounded-full border border-slate-200">
                    v{item.version || "1.0.0"}
                  </span>
                </div>

                <h1 className="text-2xl sm:text-4xl font-extrabold text-slate-900 tracking-tight">
                  {item.name}
                </h1>

                <p className="text-sm sm:text-base text-slate-600 leading-relaxed">
                  {item.summary}
                </p>

                <div className="flex items-center gap-4 text-xs text-slate-500 pt-1 flex-wrap">
                  <div className="flex items-center gap-1.5">
                    <span>Published by</span>
                    <strong className="text-slate-800 font-semibold">{item.publisher.name}</strong>
                    {item.publisher.verified && (
                      <span className="inline-flex items-center text-emerald-600 gap-0.5" title="Verified Publisher">
                        <ShieldCheck className="w-4 h-4" />
                      </span>
                    )}
                  </div>

                  <div className="flex items-center gap-1 text-amber-600 bg-amber-50 px-2.5 py-0.5 rounded-full border border-amber-200/60 font-mono">
                    <Star className="w-3.5 h-3.5 fill-amber-500 text-amber-500" />
                    <span className="font-semibold">{item.stars.toLocaleString()} Stars</span>
                  </div>

                  {item.publisher.url && (
                    <a
                      href={item.publisher.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 text-emerald-600 hover:text-emerald-700 underline underline-offset-4"
                    >
                      Upstream Source <ExternalLink className="w-3 h-3" />
                    </a>
                  )}
                </div>
              </div>

              {/* Fast Install Card */}
              <div className="w-full md:w-80 shrink-0 bg-slate-900 rounded-2xl p-5 text-white shadow-xl border border-slate-800 space-y-4">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-emerald-400 font-semibold tracking-wider uppercase">
                    One-Click Install
                  </span>
                  <span className="flex h-2 w-2 relative">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
                  </span>
                </div>

                <div className="bg-black/60 p-3 rounded-xl border border-slate-800 text-xs font-mono text-emerald-300 break-all select-all">
                  litepsm install {item.id}
                </div>

                <button
                  onClick={copyInstallCommand}
                  className="w-full py-2.5 rounded-xl bg-emerald-500 hover:bg-emerald-400 text-slate-950 font-bold text-xs transition-all shadow-md flex items-center justify-center gap-2"
                >
                  {copiedInstall ? (
                    <>
                      <Check className="w-4 h-4" /> Copied Command!
                    </>
                  ) : (
                    <>
                      <Copy className="w-4 h-4" /> Copy Install Command
                    </>
                  )}
                </button>
              </div>
            </div>
          </div>

          {/* Dedicated Agent Setup Section */}
          <section className="bg-white rounded-3xl p-6 sm:p-8 border border-slate-200/80 shadow-[0_4px_20px_-4px_rgba(15,23,42,0.05)] space-y-6">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-slate-100 pb-5">
              <div>
                <h2 className="text-lg sm:text-xl font-bold text-slate-900">
                  Agent Configuration & File Path Matrix
                </h2>
                <p className="text-xs sm:text-sm text-slate-500 mt-0.5">
                  Select your agent host to view its exact configuration file path and ready-to-fill snippet for Claude Code, Codex, OpenCode, and many more.
                </p>
              </div>

              {/* OS Selector Toggle */}
              <div className="flex items-center gap-1 bg-slate-100 p-1 rounded-xl border border-slate-200 text-xs font-medium text-slate-600 shrink-0 self-start sm:self-auto">
                <button
                  onClick={() => setPlatformOs("win")}
                  className={`px-3 py-1.5 rounded-lg transition-all ${
                    platformOs === "win" ? "bg-white text-slate-900 shadow-sm font-semibold" : "hover:text-slate-900"
                  }`}
                >
                  Windows
                </button>
                <button
                  onClick={() => setPlatformOs("mac")}
                  className={`px-3 py-1.5 rounded-lg transition-all ${
                    platformOs === "mac" ? "bg-white text-slate-900 shadow-sm font-semibold" : "hover:text-slate-900"
                  }`}
                >
                  macOS
                </button>
                <button
                  onClick={() => setPlatformOs("linux")}
                  className={`px-3 py-1.5 rounded-lg transition-all ${
                    platformOs === "linux" ? "bg-white text-slate-900 shadow-sm font-semibold" : "hover:text-slate-900"
                  }`}
                >
                  Linux
                </button>
              </div>
            </div>

            {/* Agent Switcher Pills */}
            <div className="flex items-center gap-2 overflow-x-auto no-scrollbar pb-2">
              {hosts.map((h) => {
                const isActive = activeHost === h.id;
                return (
                  <button
                    key={h.id}
                    onClick={() => setActiveHost(h.id)}
                    className={`px-4 py-2.5 rounded-xl text-xs font-semibold whitespace-nowrap transition-all border flex items-center gap-2 ${
                      isActive
                        ? "bg-slate-900 text-white border-slate-900 shadow-md"
                        : "bg-slate-50 text-slate-600 border-slate-200 hover:bg-slate-100 hover:border-slate-300"
                    }`}
                  >
                    <span>{h.name}</span>
                    <span
                      className={`text-[10px] font-mono px-1.5 py-0.5 rounded ${
                        isActive ? "bg-slate-800 text-emerald-400" : "bg-white text-slate-400 border border-slate-200"
                      }`}
                    >
                      {h.type.toUpperCase()}
                    </span>
                  </button>
                );
              })}
            </div>

            {/* Config File Path Bar */}
            <div className="bg-slate-50 rounded-2xl p-4 border border-slate-200/90 space-y-2">
              <div className="flex items-center justify-between text-xs text-slate-500 font-medium">
                <span className="flex items-center gap-1.5 font-mono text-slate-700">
                  <FolderOpen className="w-4 h-4 text-emerald-600" />
                  Target Configuration File Path:
                </span>
                <span className="text-[11px] font-mono text-slate-400 uppercase">
                  {platformOs.toUpperCase()} Filesystem
                </span>
              </div>

              <div className="flex items-center justify-between gap-3 bg-white px-3.5 py-2.5 rounded-xl border border-slate-200 shadow-sm">
                <code className="text-xs font-mono text-slate-800 break-all select-all">
                  {getResolvedConfigPath(activeHost, platformOs)}
                </code>

                <button
                  onClick={copyConfigPath}
                  className="flex items-center gap-1 px-3 py-1.5 rounded-lg bg-slate-100 hover:bg-emerald-600 hover:text-white text-slate-700 text-xs font-medium transition-all shrink-0 border border-slate-200 shadow-sm"
                  title="Copy full path"
                >
                  {copiedPath ? (
                    <>
                      <Check className="w-3.5 h-3.5 text-emerald-600 group-hover:text-white" />
                      <span>Copied!</span>
                    </>
                  ) : (
                    <>
                      <Copy className="w-3.5 h-3.5" />
                      <span>Copy Path</span>
                    </>
                  )}
                </button>
              </div>
            </div>

            {/* Snippet Mode Toggle + Code Block */}
            <div className="space-y-3">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-bold text-slate-900 uppercase tracking-wider">
                    Configuration Snippet
                  </span>
                  <span className="text-[11px] text-slate-400">
                    ({activeHost === "grok-build" || activeHost === "codex" ? "TOML block" : "JSON block"})
                  </span>
                </div>

                {item.kind !== "skill" && (
                  <div className="flex items-center gap-1 bg-slate-100 p-1 rounded-xl border border-slate-200 text-xs">
                    <button
                      onClick={() => setSnippetMode("bridge")}
                      className={`px-3 py-1 rounded-lg transition-all ${
                        snippetMode === "bridge"
                          ? "bg-emerald-600 text-white font-semibold shadow-sm"
                          : "text-slate-600 hover:text-slate-900"
                      }`}
                    >
                      LitePSM Bridge (Recommended)
                    </button>
                    <button
                      onClick={() => setSnippetMode("native")}
                      className={`px-3 py-1 rounded-lg transition-all ${
                        snippetMode === "native"
                          ? "bg-slate-800 text-white font-semibold shadow-sm"
                          : "text-slate-600 hover:text-slate-900"
                      }`}
                    >
                      Direct Native
                    </button>
                  </div>
                )}
              </div>

              {/* Code Box */}
              <div className="relative rounded-2xl bg-[#0d1117] border border-slate-800 shadow-2xl overflow-hidden">
                <div className="flex items-center justify-between px-4 py-2.5 bg-[#161b22] border-b border-slate-800 text-xs font-mono text-slate-400">
                  <div className="flex items-center gap-2">
                    <span className="w-3 h-3 rounded-full bg-[#ff5f56]" />
                    <span className="w-3 h-3 rounded-full bg-[#ffbd2e]" />
                    <span className="w-3 h-3 rounded-full bg-[#27c93f]" />
                    <span className="ml-2 text-slate-300 font-semibold">{activeHost} configuration</span>
                  </div>

                  <button
                    onClick={copySnippet}
                    className="flex items-center gap-1.5 px-3 py-1 rounded-lg bg-[#21262d] hover:bg-emerald-500 hover:text-slate-950 text-slate-200 text-xs font-sans transition-all border border-slate-700 shadow"
                  >
                    {copiedSnippet ? (
                      <>
                        <Check className="w-3.5 h-3.5 text-emerald-400 group-hover:text-slate-950" />
                        <span>Copied Snippet!</span>
                      </>
                    ) : (
                      <>
                        <Copy className="w-3.5 h-3.5" />
                        <span>Copy Snippet</span>
                      </>
                    )}
                  </button>
                </div>

                <pre className="p-5 font-mono text-xs sm:text-sm text-slate-200 overflow-x-auto whitespace-pre-wrap leading-relaxed">
                  {getGeneratedSnippet()}
                </pre>
              </div>

              <div className="flex items-start gap-2 p-3 bg-amber-50 rounded-xl border border-amber-200/80 text-xs text-amber-800">
                <Info className="w-4 h-4 shrink-0 mt-0.5 text-amber-600" />
                <span>
                  <strong>Tip:</strong> With <code>litepsm</code> installed, simply running{" "}
                  <code>litepsm</code> in your terminal automatically discovers your agent's config file and injects this snippet safely with automatic backup!
                </span>
              </div>
            </div>
          </section>

          {/* Related Capabilities */}
          {relatedItems.length > 0 && (
            <section className="space-y-4 pt-4">
              <h3 className="text-base font-bold text-slate-900">
                Related Capabilities in {item.category}
              </h3>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                {relatedItems.map((r) => (
                  <Link
                    key={r.id}
                    href={`/item?id=${encodeURIComponent(r.id)}`}
                    className="bg-white p-4 rounded-2xl border border-slate-200/80 hover:border-slate-300 shadow-sm hover:shadow-md transition-all group block"
                  >
                    <div className="flex items-center justify-between text-xs mb-2">
                      <span className="font-mono text-slate-500 text-[10px] uppercase">{r.kind}</span>
                      <span className="text-amber-600 font-mono text-xs">★ {r.stars.toLocaleString()}</span>
                    </div>
                    <h4 className="text-sm font-bold text-slate-900 group-hover:text-emerald-600 transition-colors line-clamp-1">
                      {r.name}
                    </h4>
                    <p className="text-xs text-slate-500 line-clamp-2 mt-1">
                      {r.summary}
                    </p>
                  </Link>
                ))}
              </div>
            </section>
          )}
        </main>
      </div>

      {/* Footer */}
      <footer className="w-full bg-white border-t border-slate-200 py-8 text-center text-xs text-slate-500 font-mono mt-12">
        <div className="max-w-6xl mx-auto px-4 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div>LitePSM Architecture · Universal AI Agent Capability Manager</div>
          <div className="flex items-center gap-4">
            <a
              href="https://github.com/sarv-projects/LitePSM"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-emerald-600 transition-colors"
            >
              GitHub
            </a>
            <span>·</span>
            <Link href="/" className="hover:text-emerald-600 transition-colors">
              Browse All 5,185 Capabilities
            </Link>
          </div>
        </div>
      </footer>
    </div>
  );
}

export default function ItemDetailPage() {
  return (
    <Suspense fallback={<div className="p-12 text-center text-slate-500 font-mono">Loading Capability...</div>}>
      <ItemDetailContent />
    </Suspense>
  );
}
