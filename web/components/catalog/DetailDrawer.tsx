"use client";

import React, { useState } from "react";
import {
  X,
  Copy,
  Check,
  Shield,
  ShieldCheck,
  Star,
  FileText,
  Code2,
  Lock,
  Terminal,
  ExternalLink,
} from "lucide-react";
import { ExtensionItem } from "./ExtensionCard";

interface DetailDrawerProps {
  item: ExtensionItem | null;
  onClose: () => void;
}

export function DetailDrawer({ item, onClose }: DetailDrawerProps) {
  const [activeTab, setActiveTab] = useState<"readme" | "schema" | "security" | "connect">("readme");
  const [selectedHost, setSelectedHost] = useState<string>("cline");
  const [copiedSnippet, setCopiedSnippet] = useState(false);
  const closeButtonRef = React.useRef<HTMLButtonElement>(null);

  // Platform-controls-dismiss-dialog & Accessibility Focus Routing
  React.useEffect(() => {
    if (!item) return;

    const previousFocusedElement = document.activeElement as HTMLElement | null;
    closeButtonRef.current?.focus();

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      previousFocusedElement?.focus();
    };
  }, [item, onClose]);

  if (!item) return null;

  const getHostSnippet = (host: string) => {
    switch (host) {
      case "cline":
        return JSON.stringify(
          {
            mcpServers: {
              [item.slug]: {
                command: "litepsm",
                args: ["bridge", "stdio", "--host", "cline"],
              },
            },
          },
          null,
          2
        );
      case "pi-agent":
        return `// ~/.pi/agent/mcp.json\n{\n  "mcpServers": {\n    "${item.slug}": {\n      "command": "litepsm",\n      "args": ["bridge", "stdio", "--host", "pi-agent"]\n    }\n  }\n}`;
      case "grok-build":
        return `# ~/.grok/config.toml\n[mcp_servers.${item.slug}]\ncommand = "litepsm"\nargs = ["bridge", "stdio", "--host", "grok-build"]`;
      case "claude-code":
        return `// ~/.claude.json\n{\n  "mcpServers": {\n    "${item.slug}": {\n      "command": "litepsm",\n      "args": ["bridge", "stdio", "--host", "claude-code"]\n    }\n  }\n}`;
      case "codex":
        return `# ~/.codex/config.toml\n[mcp_servers.${item.slug}]\ncommand = "litepsm"\nargs = ["bridge", "stdio", "--host", "codex"]`;
      case "opencode":
        return `// opencode.json\n{\n  "mcp": {\n    "${item.slug}": {\n      "command": "litepsm",\n      "args": ["bridge", "stdio", "--host", "opencode"]\n    }\n  }\n}`;
      default:
        return `litepsm install ${item.id}`;
    }
  };

  const copyHostSnippet = () => {
    navigator.clipboard.writeText(getHostSnippet(selectedHost));
    setCopiedSnippet(true);
    setTimeout(() => setCopiedSnippet(false), 2000);
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="drawer-title"
      aria-describedby="drawer-summary"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="fixed inset-0 z-50 flex justify-end bg-black/70 backdrop-blur-sm drawer-backdrop"
    >
      {/* Drawer Container */}
      <div className="w-full max-w-2xl h-full bg-[#0d0f16] border-l border-[#232734] flex flex-col shadow-2xl overflow-hidden drawer-panel">
        {/* Top Header */}
        <div className="p-6 border-b border-[#232734] flex items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 mb-2 flex-wrap">
              <span className="px-2 py-0.5 rounded text-[11px] font-mono bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 uppercase">
                {item.kind}
              </span>
              <span className="text-xs font-mono text-gray-500">v{item.version}</span>
              <span className="flex items-center gap-1 text-xs text-amber-400 font-mono bg-[#171a23] px-2 py-0.5 rounded border border-[#232734]">
                <Star className="w-3 h-3 fill-amber-400" />
                {item.stars.toLocaleString()}
              </span>
            </div>
            <h2 id="drawer-title" className="text-xl font-bold text-white">{item.name}</h2>
            <p id="drawer-summary" className="text-xs text-gray-400 mt-1">
              Publisher:{" "}
              <span className="text-gray-200 font-medium">{item.publisher.name}</span>
              {item.publisher.verified && (
                <span className="inline-flex items-center ml-1 text-emerald-400 text-xs">
                  <ShieldCheck className="w-3.5 h-3.5 inline mr-0.5" /> Verified
                </span>
              )}
            </p>
          </div>

          <button
            ref={closeButtonRef}
            onClick={onClose}
            aria-label="Close details"
            className="p-2 text-gray-400 hover:text-white rounded-lg hover:bg-[#1a1e2a] transition-all focus:outline-none focus:ring-2 focus:ring-emerald-500/50"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Tab Navigation */}
        <div className="flex items-center border-b border-[#232734] px-6 bg-[#11131a]">
          <button
            onClick={() => setActiveTab("readme")}
            className={`flex items-center gap-2 py-3 px-4 text-xs font-medium border-b-2 transition-all ${
              activeTab === "readme"
                ? "border-emerald-500 text-emerald-400 font-semibold"
                : "border-transparent text-gray-400 hover:text-gray-200"
            }`}
          >
            <FileText className="w-3.5 h-3.5" />
            README
          </button>

          <button
            onClick={() => setActiveTab("schema")}
            className={`flex items-center gap-2 py-3 px-4 text-xs font-medium border-b-2 transition-all ${
              activeTab === "schema"
                ? "border-emerald-500 text-emerald-400 font-semibold"
                : "border-transparent text-gray-400 hover:text-gray-200"
            }`}
          >
            <Code2 className="w-3.5 h-3.5" />
            Tools & Schema
          </button>

          <button
            onClick={() => setActiveTab("security")}
            className={`flex items-center gap-2 py-3 px-4 text-xs font-medium border-b-2 transition-all ${
              activeTab === "security"
                ? "border-emerald-500 text-emerald-400 font-semibold"
                : "border-transparent text-gray-400 hover:text-gray-200"
            }`}
          >
            <Shield className="w-3.5 h-3.5" />
            Security & Effects
          </button>

          <button
            onClick={() => setActiveTab("connect")}
            className={`flex items-center gap-2 py-3 px-4 text-xs font-medium border-b-2 transition-all ${
              activeTab === "connect"
                ? "border-emerald-500 text-emerald-400 font-semibold"
                : "border-transparent text-gray-400 hover:text-gray-200"
            }`}
          >
            <Terminal className="w-3.5 h-3.5" />
            Host Connect
          </button>
        </div>

        {/* Content Body */}
        <div className="flex-1 overflow-y-auto p-6 text-sm text-gray-300 space-y-4">
          {activeTab === "readme" && (
            <div className="space-y-4">
              <div className="prose prose-invert max-w-none text-xs leading-relaxed space-y-3">
                <div className="p-4 rounded-xl bg-[#11131a] border border-[#232734]">
                  <h4 className="text-xs font-semibold text-gray-200 uppercase tracking-wider mb-2">
                    Description
                  </h4>
                  <p className="text-gray-300">{item.summary}</p>
                </div>

                <div className="p-4 rounded-xl bg-[#11131a] border border-[#232734]">
                  <h4 className="text-xs font-semibold text-gray-200 uppercase tracking-wider mb-2">
                    Documentation
                  </h4>
                  <pre className="text-[11px] font-mono whitespace-pre-wrap text-gray-300 bg-black/40 p-3 rounded-lg border border-[#232734]">
                    {item.readme}
                  </pre>
                </div>
              </div>
            </div>
          )}

          {activeTab === "schema" && (
            <div className="space-y-4">
              {/* Schema Fingerprint */}
              <div className="p-3.5 rounded-xl bg-[#11131a] border border-[#232734]">
                <span className="text-[10px] uppercase font-mono tracking-wider text-gray-400 block mb-1">
                  Canonical Schema Fingerprint (SHA-256)
                </span>
                <code className="text-xs font-mono text-emerald-400 break-all">
                  {item.schemaFingerprint}
                </code>
              </div>

              {/* Tools List */}
              <div className="space-y-3">
                <h4 className="text-xs font-semibold text-gray-300 uppercase tracking-wider">
                  Exposed Tool Functions ({(item.tools || []).length})
                </h4>
                {(item.tools || []).map((t) => (
                  <div
                    key={t.name}
                    className="p-4 rounded-xl bg-[#11131a] border border-[#232734] space-y-2"
                  >
                    <div className="flex items-center justify-between">
                      <span className="font-mono text-sm font-semibold text-white">
                        {t.name}
                      </span>
                      <span className="text-[10px] font-mono text-emerald-400 bg-emerald-500/10 px-2 py-0.5 rounded border border-emerald-500/20">
                        Tool
                      </span>
                    </div>
                    <p className="text-xs text-gray-400">{t.description}</p>
                    <pre className="text-[11px] font-mono bg-black/50 p-2.5 rounded-lg border border-[#232734] text-gray-300 overflow-x-auto">
                      {JSON.stringify(t.inputSchema, null, 2)}
                    </pre>
                  </div>
                ))}
              </div>
            </div>
          )}

          {activeTab === "security" && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-emerald-950/20 border border-emerald-500/30 flex items-start gap-3">
                <ShieldCheck className="w-5 h-5 text-emerald-400 shrink-0 mt-0.5" />
                <div className="text-xs">
                  <span className="font-semibold text-white block mb-0.5">
                    Fail-Closed Policy Enforcement
                  </span>
                  <p className="text-gray-400">
                    All declared effects undergo 5-tier evaluation before runtime execution. Unauthorized actions trigger explicit user prompts.
                  </p>
                </div>
              </div>

              <h4 className="text-xs font-semibold text-gray-300 uppercase tracking-wider">
                Declared Effects & Capabilities
              </h4>

              <div className="space-y-2">
                {(item.effects || []).map((eff, idx) => (
                  <div
                    key={idx}
                    className="p-3 rounded-xl bg-[#11131a] border border-[#232734] flex items-center justify-between"
                  >
                    <code className="text-xs font-mono text-cyan-300">{eff.effect}</code>
                    <span className="text-[10px] font-mono text-gray-400 bg-[#171a23] px-2 py-0.5 rounded border border-[#232734]">
                      {eff.declaredBy}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {activeTab === "connect" && (
            <div className="space-y-4">
              <div className="space-y-2">
                <label className="text-xs font-semibold text-gray-300 uppercase tracking-wider block">
                  Select AI Agent Host
                </label>
                <select
                  value={selectedHost}
                  onChange={(e) => setSelectedHost(e.target.value)}
                  className="w-full bg-[#11131a] text-white border border-[#232734] rounded-xl px-4 py-2.5 text-xs focus:outline-none focus:border-emerald-500"
                >
                  <option value="cline">Cline (VS Code Extension)</option>
                  <option value="pi-agent">Pi Agent (Terminal CLI)</option>
                  <option value="grok-build">Grok Build (xAI Dev Tool)</option>
                  <option value="claude-code">Claude Code (Terminal CLI)</option>
                  <option value="codex">OpenAI Codex (Terminal CLI)</option>
                  <option value="opencode">OpenCode (CLI)</option>
                </select>
              </div>

              <div className="relative">
                <pre className="p-4 rounded-xl bg-black/60 border border-[#232734] font-mono text-xs text-emerald-300 overflow-x-auto whitespace-pre-wrap">
                  {getHostSnippet(selectedHost)}
                </pre>
                <button
                  onClick={copyHostSnippet}
                  className="absolute top-3 right-3 flex items-center gap-1 px-2.5 py-1.5 rounded-lg bg-[#1e2230] hover:bg-emerald-500 hover:text-black text-gray-200 text-xs font-sans transition-all border border-[#2c3244]"
                >
                  {copiedSnippet ? (
                    <>
                      <Check className="w-3.5 h-3.5 text-emerald-400" />
                      <span>Copied!</span>
                    </>
                  ) : (
                    <>
                      <Copy className="w-3.5 h-3.5" />
                      <span>Copy Config</span>
                    </>
                  )}
                </button>
              </div>
            </div>
          )}
        </div>

        {/* Bottom Action Footer */}
        <div className="p-4 border-t border-[#232734] bg-[#11131a] flex items-center justify-between">
          <div className="text-xs font-mono text-gray-400">
            <code>litepsm install {item.id}</code>
          </div>
          <button
            onClick={() => {
              navigator.clipboard.writeText(`litepsm install ${item.id}`);
              alert(`Copied: litepsm install ${item.id}`);
            }}
            className="px-4 py-2 rounded-xl bg-emerald-500 hover:bg-emerald-400 text-black font-semibold text-xs transition-all shadow-lg shadow-emerald-500/20"
          >
            Copy Install Command
          </button>
        </div>
      </div>
    </div>
  );
}
