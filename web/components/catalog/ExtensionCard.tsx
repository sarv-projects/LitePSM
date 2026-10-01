"use client";

import React from "react";
import { Copy, Star, ShieldCheck, Sparkles, Box, ArrowUpRight, Database, Bug, Globe, Cloud } from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { copyText } from "../../lib/clipboard";
import { formatStars } from "../../lib/format";

export type ExtensionItem = Listing;

interface ExtensionCardProps {
  item: Listing;
  onSelect: (item: Listing) => void;
}

function kindBadge(item: Listing) {
  switch (item.kind) {
    case "mcp":
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-blue-200/80 bg-blue-50 px-2 py-0.5 font-mono text-[10px] font-medium text-blue-700">
          MCP: {item.transport || "stdio"}
        </span>
      );
    case "skill":
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-purple-200/80 bg-purple-50 px-2 py-0.5 font-mono text-[10px] font-medium text-purple-700">
          <Sparkles className="h-3 w-3 text-purple-600" aria-hidden="true" /> Skill
        </span>
      );
    default:
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-amber-200/80 bg-amber-50 px-2 py-0.5 font-mono text-[10px] font-medium text-amber-700">
          <Box className="h-3 w-3 text-amber-600" aria-hidden="true" /> Plugin
        </span>
      );
  }
}

function PublisherAvatar({ name, url }: { name: string; url?: string }) {
  const initial = (name || "?").trim().charAt(0).toUpperCase();
  if (url && /^https?:\/\//.test(url)) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={url}
        alt=""
        aria-hidden="true"
        width={20}
        height={20}
        className="h-5 w-5 rounded-full border border-slate-200 object-cover"
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      className="flex h-5 w-5 items-center justify-center rounded-full bg-slate-900 text-[10px] font-bold text-emerald-400"
    >
      {initial}
    </span>
  );
}

/** Pick a single secondary category-style tag for the second tag row. */
function secondaryTag(item: Listing): string | null {
  if (item.category && item.category !== item.kind) return item.category;
  if (item.runtime) return item.runtime;
  return null;
}

export function ExtensionCard({ item, onSelect }: ExtensionCardProps) {
  const tag = secondaryTag(item);
  const onCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    await copyText(`litepsm install ${item.id}`);
  };

  return (
    <article className="lift-on-hover group relative flex transform-gpu flex-col justify-between overflow-hidden rounded-2xl border border-slate-200/80 bg-white shadow-[0_1px_3px_rgba(15,23,42,0.03),0_4px_12px_rgba(15,23,42,0.02)] transition-all duration-200 hover:border-slate-300 hover:shadow-[0_12px_28px_-6px_rgba(15,23,42,0.09)] focus-within:ring-2 focus-within:ring-emerald-500/60">
      {/* Stretched primary target: one focusable control, no nested interactives. */}
      <button
        type="button"
        onClick={() => onSelect(item)}
        aria-label={`View details for ${item.name}`}
        className="absolute inset-0 z-10 rounded-2xl focus:outline-none"
      />

      <div className="p-5">
        <div className="mb-3 flex items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-1.5">{kindBadge(item)}</div>
          <div className="flex items-center gap-1 rounded-full border border-amber-200/60 bg-amber-50/80 px-2 py-0.5 font-mono text-xs text-amber-600">
            <Star className="h-3 w-3 fill-amber-500 text-amber-500" aria-hidden="true" />
            <span className="font-semibold">{formatStars(item.stars)}</span>
          </div>
        </div>

        <div className="mb-2">
          <div className="flex items-center justify-between gap-1">
            <h3 className="line-clamp-1 text-[15px] font-bold tracking-tight text-slate-900 transition-colors group-hover:text-emerald-600">
              {item.name}
            </h3>
            <ArrowUpRight
              className="h-4 w-4 shrink-0 text-slate-400 transition-all group-hover:-translate-y-0.5 group-hover:translate-x-0.5 group-hover:text-emerald-600"
              aria-hidden="true"
            />
          </div>
          <div className="mt-1 flex items-center gap-2 text-xs text-slate-500">
            <PublisherAvatar name={item.publisher?.name} url={item.publisher?.avatarUrl} />
            <span className="truncate">
              by <strong className="font-medium text-slate-700">{item.publisher?.name || "unknown"}</strong>
            </span>
            {item.publisher?.verified && (
              <ShieldCheck className="h-3.5 w-3.5 text-emerald-600" aria-label="Verified publisher" />
            )}
          </div>
        </div>

        <p className="mb-3 line-clamp-2 text-xs leading-relaxed text-slate-600">{item.summary}</p>

        {tag && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[10px] text-slate-600">
              <Database className="h-3 w-3 text-slate-400" aria-hidden="true" />
              {tag}
            </span>
          </div>
        )}
      </div>

      <div className="relative z-20 flex items-center justify-between gap-2 border-t border-slate-100 bg-slate-50/70 px-5 py-3">
        <div className="flex flex-wrap items-center gap-1">
          {(item.testedHosts || []).slice(0, 3).map((h) => (
            <span
              key={h}
              className="rounded-md border border-slate-200 bg-white px-2 py-0.5 font-mono text-[10px] text-slate-600 shadow-[0_1px_1px_rgba(0,0,0,0.03)]"
            >
              {h}
            </span>
          ))}
          {(item.testedHosts || []).length > 3 && (
            <span className="font-mono text-[10px] text-slate-400">+{(item.testedHosts || []).length - 3}</span>
          )}
        </div>

        <button
          type="button"
          onClick={onCopy}
          aria-label={`Copy install command for ${item.name}`}
          className="flex items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 shadow-sm transition-all hover:border-emerald-600 hover:bg-emerald-600 hover:text-white"
        >
          <Copy className="h-3.5 w-3.5" aria-hidden="true" />
          <span className="text-[11px]">Install</span>
        </button>
      </div>
    </article>
  );
}

export const KIND_ICONS = { Database, Bug, Globe, Cloud };
