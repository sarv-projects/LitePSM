"use client";

import React from "react";
import Link from "next/link";
import { Star, ShieldCheck, Sparkles, Box, ArrowUpRight, TerminalSquare, Blocks } from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { formatStars } from "../../lib/format";

export type ExtensionItem = Listing;

interface ExtensionCardProps {
  item: Listing;
}

const KIND_TILE: Record<Listing["kind"], string> = {
  mcp: "bg-blue-600",
  skill: "bg-purple-600",
  plugin: "bg-amber-600",
};

function kindBadge(item: Listing) {
  switch (item.kind) {
    case "mcp":
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-blue-200/80 bg-blue-50 px-2 py-0.5 font-mono text-[10px] font-medium text-blue-700">
          <TerminalSquare className="h-3 w-3 text-blue-600" aria-hidden="true" />
          MCP · {item.transport || "stdio"}
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

/** Letter tile with a kind-tinted background; GitHub/publisher avatars when published. */
function PublisherMark({ name, url, kind }: { name: string; url?: string; kind: Listing["kind"] }) {
  const initial = (name || "?").trim().charAt(0).toUpperCase();
  if (url && /^https?:\/\//.test(url)) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={url}
        alt=""
        aria-hidden="true"
        width={40}
        height={40}
        loading="lazy"
        className="h-10 w-10 shrink-0 rounded-xl border border-slate-200 object-cover"
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl text-base font-black text-white ${KIND_TILE[kind]}`}
    >
      {initial}
    </span>
  );
}

export function ExtensionCard({ item }: ExtensionCardProps) {
  return (
    <Link
      href={`/package/?slug=${encodeURIComponent(item.slug)}`}
      aria-label={`Open ${item.name} package page`}
      className="lift-on-hover group relative flex transform-gpu flex-col justify-between overflow-hidden rounded-2xl border border-slate-200/80 bg-white shadow-[0_1px_3px_rgba(15,23,42,0.03),0_4px_12px_rgba(15,23,42,0.02)] transition-all duration-200 hover:border-slate-300 hover:shadow-[0_12px_28px_-6px_rgba(15,23,42,0.09)] focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/60"
    >
      <div className="flex items-start gap-3 p-5 pb-3">
        <PublisherMark name={item.publisher?.name} url={item.publisher?.avatarUrl} kind={item.kind} />
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-1">
            <h3 className="line-clamp-1 font-mono text-[15px] font-bold tracking-tight text-slate-900 transition-colors group-hover:text-emerald-700">
              {item.name}
            </h3>
            <ArrowUpRight
              className="h-4 w-4 shrink-0 text-slate-300 transition-all group-hover:-translate-y-0.5 group-hover:translate-x-0.5 group-hover:text-emerald-600"
              aria-hidden="true"
            />
          </div>
          <div className="mt-0.5 flex items-center gap-1.5 text-xs text-slate-500">
            <span className="truncate">
              by <strong className="font-medium text-slate-700">{item.publisher?.name || "unknown"}</strong>
            </span>
            {item.publisher?.verified && (
              <ShieldCheck className="h-3.5 w-3.5 shrink-0 text-emerald-600" aria-label="Verified publisher" />
            )}
          </div>
        </div>
        <div
          className="flex shrink-0 items-center gap-1 rounded-full border border-amber-200/60 bg-amber-50/80 px-2 py-0.5 font-mono text-xs text-amber-600"
          title="GitHub stars (illustrative popularity)"
        >
          <Star className="h-3 w-3 fill-amber-500 text-amber-500" aria-hidden="true" />
          <span className="font-semibold">{formatStars(item.stars)}</span>
        </div>
      </div>

      <p className="line-clamp-2 px-5 text-xs leading-relaxed text-slate-600">{item.summary}</p>

      <div className="flex flex-wrap items-center gap-1.5 px-5 pt-3">
        {kindBadge(item)}
        <span className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[10px] text-slate-600">
          <Blocks className="h-3 w-3 text-slate-400" aria-hidden="true" />
          {item.category}
        </span>
      </div>

      <div className="mt-3 flex items-center gap-1 border-t border-slate-100 bg-slate-50/70 px-5 py-2.5">
        <span className="mr-1 font-mono text-[10px] uppercase tracking-wide text-slate-400">Works with</span>
        <div className="flex min-w-0 flex-wrap items-center gap-1">
          {(item.testedHosts || []).slice(0, 3).map((h) => (
            <span
              key={h}
              className="truncate rounded-md border border-slate-200 bg-white px-2 py-0.5 font-mono text-[10px] text-slate-600 shadow-[0_1px_1px_rgba(0,0,0,0.03)]"
            >
              {h}
            </span>
          ))}
          {(item.testedHosts || []).length > 3 && (
            <span className="font-mono text-[10px] text-slate-400">+{(item.testedHosts || []).length - 3}</span>
          )}
          {(item.testedHosts || []).length === 0 && (
            <span className="font-mono text-[10px] text-slate-400">compatibility unknown</span>
          )}
        </div>
      </div>
    </Link>
  );
}
