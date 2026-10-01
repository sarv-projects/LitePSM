"use client";

import React, { useState } from "react";
import { Check, Copy, Star, ShieldCheck, ExternalLink, Terminal, Sparkles, Box } from "lucide-react";

export interface ExtensionItem {
  id: string;
  name: string;
  slug: string;
  kind: "mcp" | "skill" | "plugin";
  summary: string;
  category: string;
  publisher: {
    name: string;
    verified: boolean;
    url?: string;
  };
  transport?: string;
  stars: number;
  version: string;
  testedHosts: string[];
  readme: string;
  schemaFingerprint: string;
  tools: Array<{
    name: string;
    description: string;
    inputSchema: any;
  }>;
  effects: Array<{
    effect: string;
    declaredBy: string;
  }>;
}

interface ExtensionCardProps {
  item: ExtensionItem;
  onSelect: (item: ExtensionItem) => void;
}

export function ExtensionCard({ item, onSelect }: ExtensionCardProps) {
  const [copied, setCopied] = useState(false);

  const copyInstall = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard.writeText(`litepsm install ${item.id}`);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const getKindBadge = () => {
    switch (item.kind) {
      case "mcp":
        return (
          <span className="px-2 py-0.5 rounded text-[11px] font-mono bg-blue-500/10 text-blue-400 border border-blue-500/20">
            MCP: {item.transport || "stdio"}
          </span>
        );
      case "skill":
        return (
          <span className="px-2 py-0.5 rounded text-[11px] font-mono bg-purple-500/10 text-purple-400 border border-purple-500/20 flex items-center gap-1">
            <Sparkles className="w-3 h-3" /> Skill
          </span>
        );
      case "plugin":
        return (
          <span className="px-2 py-0.5 rounded text-[11px] font-mono bg-amber-500/10 text-amber-400 border border-amber-500/20 flex items-center gap-1">
            <Box className="w-3 h-3" /> Plugin
          </span>
        );
    }
  };

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onSelect(item)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(item);
        }
      }}
      className="group relative flex flex-col justify-between glass-panel rounded-2xl p-5 border border-[#232734] hover:border-emerald-500/50 hover:bg-[#151822] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/60 focus-visible:border-emerald-500 transition-all duration-200 cursor-pointer shadow-lg hover:shadow-emerald-950/20 will-change-transform transform-gpu hover:-translate-y-0.5"
    >
      <div>
        {/* Top Badges & Stars */}
        <div className="flex items-center justify-between gap-2 mb-3">
          <div className="flex items-center gap-1.5 flex-wrap">
            {getKindBadge()}
            <span className="text-[11px] font-mono text-gray-500 bg-[#171a23] px-2 py-0.5 rounded border border-[#232734]">
              v{item.version}
            </span>
          </div>
          <div className="flex items-center gap-1 text-xs text-amber-400 font-mono bg-[#171a23] px-2 py-0.5 rounded border border-[#232734]">
            <Star className="w-3.5 h-3.5 fill-amber-400 text-amber-400" />
            <span>{item.stars.toLocaleString()}</span>
          </div>
        </div>

        {/* Title & Verified Publisher */}
        <div className="mb-2">
          <h3 className="text-base font-semibold text-white group-hover:text-emerald-400 transition-colors line-clamp-1">
            {item.name}
          </h3>
          <div className="flex items-center gap-1.5 text-xs text-gray-400 mt-1">
            <span>by {item.publisher.name}</span>
            {item.publisher.verified && (
              <span className="inline-flex items-center gap-0.5 text-emerald-400" title="Verified Publisher">
                <ShieldCheck className="w-3.5 h-3.5" />
              </span>
            )}
          </div>
        </div>

        {/* Description */}
        <p className="text-xs text-gray-400 line-clamp-2 leading-relaxed mb-4">
          {item.summary}
        </p>
      </div>

      {/* Footer: Tested Hosts & Copy Install */}
      <div className="pt-3 border-t border-[#232734]/80 flex items-center justify-between gap-2">
        <div className="flex items-center gap-1 flex-wrap">
          {item.testedHosts.slice(0, 3).map((h) => (
            <span
              key={h}
              className="text-[10px] font-mono text-gray-400 bg-[#10121a] px-1.5 py-0.5 rounded border border-[#232734]"
            >
              {h}
            </span>
          ))}
          {item.testedHosts.length > 3 && (
            <span className="text-[10px] font-mono text-gray-500">
              +{item.testedHosts.length - 3}
            </span>
          )}
        </div>

        <button
          onClick={copyInstall}
          className="flex items-center gap-1 px-2.5 py-1.5 rounded-lg bg-[#1a1e2a] hover:bg-emerald-500 hover:text-black text-gray-300 text-xs font-mono transition-all border border-[#2c3244]"
          title={`Copy: litepsm install ${item.id}`}
        >
          {copied ? (
            <>
              <Check className="w-3.5 h-3.5 text-emerald-400 group-hover:text-black" />
              <span className="text-[11px] font-sans">Copied!</span>
            </>
          ) : (
            <>
              <Copy className="w-3.5 h-3.5" />
              <span className="text-[11px] font-sans">Install</span>
            </>
          )}
        </button>
      </div>
    </div>
  );
}
