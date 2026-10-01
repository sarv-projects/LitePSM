"use client";

import React, { useState } from "react";
import { Check, Copy, Star, ShieldCheck, Sparkles, Box, ArrowUpRight } from "lucide-react";

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
    avatarUrl?: string;
  };
  transport?: string;
  runtime?: string;
  stars: number;
  version: string;
  testedHosts: string[];
  readme?: string;
  command?: string;
  args?: string[];
  skillSource?: string;
  schemaFingerprint?: string;
  tools?: Array<{
    name: string;
    description: string;
    inputSchema?: any;
  }>;
  effects?: Array<{
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
          <span className="px-2 py-0.5 rounded-full text-[10px] font-mono font-medium bg-blue-50 text-blue-700 border border-blue-200/80">
            MCP: {item.transport || "stdio"}
          </span>
        );
      case "skill":
        return (
          <span className="px-2 py-0.5 rounded-full text-[10px] font-mono font-medium bg-purple-50 text-purple-700 border border-purple-200/80 flex items-center gap-1">
            <Sparkles className="w-3 h-3 text-purple-600" /> Skill
          </span>
        );
      case "plugin":
        return (
          <span className="px-2 py-0.5 rounded-full text-[10px] font-mono font-medium bg-amber-50 text-amber-700 border border-amber-200/80 flex items-center gap-1">
            <Box className="w-3 h-3 text-amber-600" /> Plugin
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
      className="group relative flex flex-col justify-between bg-white rounded-2xl border border-slate-200/80 hover:border-slate-300 shadow-[0_1px_3px_rgba(15,23,42,0.03),0_4px_12px_rgba(15,23,42,0.02)] hover:shadow-[0_12px_28px_-6px_rgba(15,23,42,0.09)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/60 focus-visible:border-emerald-500 transition-all duration-200 cursor-pointer overflow-hidden transform-gpu hover:-translate-y-1"
    >
      {/* MCPMarket signature micro-dither strip */}
      <div className="card-dither-strip w-full h-[5px] border-b border-slate-100" />

      <div className="p-5">
        {/* Top Badges & Stars */}
        <div className="flex items-center justify-between gap-2 mb-3">
          <div className="flex items-center gap-1.5 flex-wrap">
            {getKindBadge()}
            {item.runtime && (
              <span className="text-[10px] font-mono text-slate-500 bg-slate-100 px-2 py-0.5 rounded-full border border-slate-200">
                {item.runtime}
              </span>
            )}
            <span className="text-[10px] font-mono text-slate-400 bg-slate-50 px-1.5 py-0.5 rounded border border-slate-200">
              v{item.version || "1.0.0"}
            </span>
          </div>
          <div className="flex items-center gap-1 text-xs text-amber-600 font-mono bg-amber-50/80 px-2 py-0.5 rounded-full border border-amber-200/60">
            <Star className="w-3 h-3 fill-amber-500 text-amber-500" />
            <span className="font-semibold">{item.stars.toLocaleString()}</span>
          </div>
        </div>

        {/* Title & Publisher */}
        <div className="mb-2">
          <div className="flex items-center justify-between gap-1">
            <h3 className="text-[15px] font-bold text-slate-900 group-hover:text-emerald-600 transition-colors line-clamp-1 tracking-tight">
              {item.name}
            </h3>
            <ArrowUpRight className="w-4 h-4 text-slate-400 group-hover:text-emerald-600 group-hover:translate-x-0.5 group-hover:-translate-y-0.5 transition-all shrink-0" />
          </div>
          <div className="flex items-center gap-1.5 text-xs text-slate-500 mt-1">
            <span>by <strong className="text-slate-700 font-medium">{item.publisher.name}</strong></span>
            {item.publisher.verified && (
              <span className="inline-flex items-center gap-0.5 text-emerald-600" title="Verified Publisher">
                <ShieldCheck className="w-3.5 h-3.5" />
              </span>
            )}
          </div>
        </div>

        {/* Description */}
        <p className="text-xs text-slate-600 line-clamp-2 leading-relaxed mb-4">
          {item.summary}
        </p>
      </div>

      {/* Footer: Tested Hosts & Copy Install */}
      <div className="px-5 py-3 bg-slate-50/70 border-t border-slate-100 flex items-center justify-between gap-2">
        <div className="flex items-center gap-1 flex-wrap">
          {item.testedHosts.slice(0, 3).map((h) => (
            <span
              key={h}
              className="text-[10px] font-mono text-slate-600 bg-white px-2 py-0.5 rounded-md border border-slate-200 shadow-[0_1px_1px_rgba(0,0,0,0.03)]"
            >
              {h}
            </span>
          ))}
          {item.testedHosts.length > 3 && (
            <span className="text-[10px] font-mono text-slate-400">
              +{item.testedHosts.length - 3}
            </span>
          )}
        </div>

        <button
          onClick={copyInstall}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white hover:bg-emerald-600 hover:text-white text-slate-700 text-xs font-medium transition-all border border-slate-200 shadow-sm hover:border-emerald-600 hover:shadow"
          title={`Copy: litepsm install ${item.id}`}
        >
          {copied ? (
            <>
              <Check className="w-3.5 h-3.5 text-emerald-600 group-hover:text-white" />
              <span className="text-[11px]">Copied</span>
            </>
          ) : (
            <>
              <Copy className="w-3.5 h-3.5 text-slate-500 group-hover:text-white" />
              <span className="text-[11px]">Install</span>
            </>
          )}
        </button>
      </div>
    </div>
  );
}
