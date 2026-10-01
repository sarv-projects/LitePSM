"use client";

import React from "react";
import Link from "next/link";
import { Star } from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { formatStars } from "../../lib/format";

interface LeaderboardPreviewProps {
  items: Listing[];
}

/** Compact numbered "Top 10 by stars" board. Each row opens the package page. */
export function LeaderboardPreview({ items }: LeaderboardPreviewProps) {
  if (!items.length) return null;

  return (
    <section className="mx-auto w-full max-w-7xl px-4 pb-12 lg:px-8">
      <div className="mb-4 flex items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-bold tracking-tight text-slate-900">Top 10 leaderboard</h2>
          <p className="mt-0.5 text-xs text-slate-500">Most-starred capabilities in this release.</p>
        </div>
      </div>

      <ol className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {items.map((item, i) => (
          <li key={item.id}>
            <Link
              href={`/package/?slug=${encodeURIComponent(item.slug)}`}
              aria-label={`Open ${item.name} package page`}
              className="flex w-full items-center gap-3 rounded-xl border border-slate-200/80 bg-white px-4 py-3 text-left shadow-sm transition-all hover:border-slate-300 hover:shadow-md focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/60"
            >
              <span className="w-6 shrink-0 text-center font-mono text-sm font-bold text-slate-300">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold text-slate-900">{item.name}</span>
                <span className="block truncate text-xs text-slate-500">{item.summary}</span>
              </span>
              <span className="flex shrink-0 items-center gap-1 font-mono text-xs text-amber-600">
                <Star className="h-3 w-3 fill-amber-500 text-amber-500" aria-hidden="true" />
                {formatStars(item.stars)}
              </span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}
