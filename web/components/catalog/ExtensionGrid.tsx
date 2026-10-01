"use client";

import React, { useState, useMemo } from "react";
import { ExtensionCard, ExtensionItem } from "./ExtensionCard";
import { SlidersHorizontal, PackageSearch, ArrowDown } from "lucide-react";

interface ExtensionGridProps {
  items: ExtensionItem[];
  onSelectItem: (item: ExtensionItem) => void;
  query: string;
}

const ITEMS_PER_PAGE = 48;

export function ExtensionGrid({ items, onSelectItem, query }: ExtensionGridProps) {
  const [sortBy, setSortBy] = useState<"stars" | "name" | "recent">("stars");
  const [visibleCount, setVisibleCount] = useState<number>(ITEMS_PER_PAGE);

  // Reset visible count when search query or items change
  React.useEffect(() => {
    setVisibleCount(ITEMS_PER_PAGE);
  }, [query, items.length]);

  const sortedItems = useMemo(() => {
    return [...items].sort((a, b) => {
      if (sortBy === "stars") return b.stars - a.stars;
      if (sortBy === "name") return a.name.localeCompare(b.name);
      return 0;
    });
  }, [items, sortBy]);

  const displayedItems = useMemo(() => {
    return sortedItems.slice(0, visibleCount);
  }, [sortedItems, visibleCount]);

  const hasMore = visibleCount < sortedItems.length;

  const loadMore = () => {
    setVisibleCount((prev) => prev + ITEMS_PER_PAGE);
  };

  return (
    <section className="max-w-7xl mx-auto px-4 lg:px-8 pb-20">
      {/* Header Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-6 pb-4 border-b border-slate-200">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-bold text-slate-900 tracking-tight">
            Discovered Capabilities
          </h2>
          <span className="text-xs font-mono font-semibold text-emerald-800 bg-emerald-100/80 px-2.5 py-0.5 rounded-full border border-emerald-300">
            {sortedItems.length.toLocaleString()} Total
          </span>
        </div>

        <div className="flex items-center gap-3 text-xs text-slate-500">
          <div className="flex items-center gap-1.5">
            <SlidersHorizontal className="w-3.5 h-3.5 text-slate-400" />
            <span>Sort by:</span>
          </div>
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as any)}
            className="bg-white text-slate-800 border border-slate-200 rounded-xl px-3 py-1.5 text-xs font-medium focus:outline-none focus:border-emerald-500 shadow-sm"
          >
            <option value="stars">Most Popular / Stars</option>
            <option value="name">Alphabetical (A-Z)</option>
          </select>
        </div>
      </div>

      {/* Grid */}
      {displayedItems.length > 0 ? (
        <div className="space-y-10">
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
            {displayedItems.map((item) => (
              <ExtensionCard key={item.id} item={item} onSelect={onSelectItem} />
            ))}
          </div>

          {/* Load More Button */}
          {hasMore && (
            <div className="text-center pt-4">
              <button
                onClick={loadMore}
                className="inline-flex items-center gap-2 px-6 py-3 rounded-2xl bg-white hover:bg-slate-900 hover:text-white text-slate-800 text-xs font-bold transition-all border border-slate-300 shadow-sm hover:shadow-lg"
              >
                <span>Load More Capabilities ({sortedItems.length - visibleCount} remaining)</span>
                <ArrowDown className="w-4 h-4" />
              </button>
            </div>
          )}
        </div>
      ) : (
        <div className="py-20 text-center bg-white rounded-3xl border border-slate-200 max-w-md mx-auto shadow-sm p-8">
          <PackageSearch className="w-12 h-12 text-slate-400 mx-auto mb-3" />
          <h4 className="text-base font-bold text-slate-900 mb-1">No capabilities found</h4>
          <p className="text-xs text-slate-500 leading-relaxed">
            No matching MCP servers or skills found for &ldquo;{query}&rdquo;. Try another search keyword or clear filters.
          </p>
        </div>
      )}
    </section>
  );
}
