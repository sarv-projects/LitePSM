"use client";

import React, { useEffect, useMemo, useState } from "react";
import { SlidersHorizontal, PackageSearch, ArrowDown } from "lucide-react";
import { ExtensionCard } from "./ExtensionCard";
import { Listing } from "../../lib/telemetry";

interface ExtensionGridProps {
  items: Listing[];
  onSelectItem: (item: Listing) => void;
  query: string;
  loading?: boolean;
  onClearFilters?: () => void;
}

const ITEMS_PER_PAGE = 48;
const SUGGESTIONS = ["postgres", "github", "playwright", "memory", "browser", "code review"];

export function ExtensionGrid({ items, onSelectItem, query, loading, onClearFilters }: ExtensionGridProps) {
  const [sortBy, setSortBy] = useState<"stars" | "name">("stars");
  const [visibleCount, setVisibleCount] = useState<number>(ITEMS_PER_PAGE);
  const [showSkeleton, setShowSkeleton] = useState(false);

  useEffect(() => {
    setVisibleCount(ITEMS_PER_PAGE);
  }, [query, items]);

  // Only show skeletons if the (already deferred) result stays empty briefly.
  useEffect(() => {
    if (!loading) {
      setShowSkeleton(false);
      return;
    }
    const t = window.setTimeout(() => setShowSkeleton(true), 180);
    return () => window.clearTimeout(t);
  }, [loading]);

  const sortedItems = useMemo(() => {
    return [...items].sort((a, b) => {
      if (sortBy === "stars") return b.stars - a.stars || a.name.localeCompare(b.name);
      return a.name.localeCompare(b.name);
    });
  }, [items, sortBy]);

  const displayedItems = useMemo(() => sortedItems.slice(0, visibleCount), [sortedItems, visibleCount]);
  const hasMore = visibleCount < sortedItems.length;

  return (
    <section className="mx-auto max-w-7xl px-4 pb-20 lg:px-8">
      <div className="mb-6 flex flex-col justify-between gap-3 border-b border-slate-200 pb-4 sm:flex-row sm:items-center">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-bold tracking-tight text-slate-900">Discovered Capabilities</h2>
          <span
            aria-live="polite"
            className="rounded-full border border-emerald-300 bg-emerald-100/80 px-2.5 py-0.5 font-mono text-xs font-semibold text-emerald-800"
          >
            {sortedItems.length.toLocaleString()} Total
          </span>
        </div>

        <div className="flex items-center gap-3 text-xs text-slate-500">
          <label className="flex items-center gap-1.5">
            <SlidersHorizontal className="h-3.5 w-3.5 text-slate-400" aria-hidden="true" />
            <span>Sort by:</span>
            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value as "stars" | "name")}
              className="rounded-xl border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-800 shadow-sm focus:border-emerald-500 focus:outline-none"
            >
              <option value="stars">Most Popular / Stars</option>
              <option value="name">Alphabetical (A-Z)</option>
            </select>
          </label>
        </div>
      </div>

      {displayedItems.length > 0 ? (
        <div className="space-y-10">
          <div className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3">
            {displayedItems.map((item) => (
              <ExtensionCard key={item.id} item={item} onSelect={onSelectItem} />
            ))}
          </div>

          {hasMore && (
            <div className="pt-4 text-center">
              <button
                type="button"
                onClick={() => setVisibleCount((prev) => prev + ITEMS_PER_PAGE)}
                className="inline-flex items-center gap-2 rounded-2xl border border-slate-300 bg-white px-6 py-3 text-xs font-bold text-slate-800 shadow-sm transition-all hover:bg-slate-900 hover:text-white hover:shadow-lg"
              >
                <span>Load More Capabilities ({sortedItems.length - visibleCount} remaining)</span>
                <ArrowDown className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
          )}
        </div>
      ) : showSkeleton ? (
        <div className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3" aria-hidden="true">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-44 animate-pulse rounded-2xl border border-slate-200 bg-slate-100/70" />
          ))}
        </div>
      ) : (
        <div className="mx-auto max-w-md rounded-3xl border border-slate-200 bg-white p-8 text-center shadow-sm">
          <PackageSearch className="mx-auto mb-3 h-12 w-12 text-slate-400" aria-hidden="true" />
          <h4 className="mb-1 text-base font-bold text-slate-900">No capabilities found</h4>
          <p className="text-xs leading-relaxed text-slate-500">
            {query ? (
              <>
                Nothing matched <span className="font-mono text-slate-700">&ldquo;{query}&rdquo;</span>. Try one of
                these:
              </>
            ) : (
              <>No capabilities match the current filters. Try one of these:</>
            )}
          </p>
          <div className="mt-4 flex flex-wrap justify-center gap-2">
            {SUGGESTIONS.map((s) => (
              <span key={s} className="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 font-mono text-[11px] text-slate-600">
                {s}
              </span>
            ))}
          </div>
          {onClearFilters && (
            <button
              type="button"
              onClick={onClearFilters}
              className="mt-5 rounded-xl border border-slate-300 bg-white px-4 py-2 text-xs font-semibold text-slate-800 transition-all hover:bg-slate-900 hover:text-white"
            >
              Clear all filters
            </button>
          )}
        </div>
      )}
    </section>
  );
}
