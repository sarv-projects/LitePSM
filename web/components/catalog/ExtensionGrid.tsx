"use client";

import React, { useState } from "react";
import { ExtensionCard, ExtensionItem } from "./ExtensionCard";
import { SlidersHorizontal, PackageSearch } from "lucide-react";

interface ExtensionGridProps {
  items: ExtensionItem[];
  onSelectItem: (item: ExtensionItem) => void;
  query: string;
}

export function ExtensionGrid({ items, onSelectItem, query }: ExtensionGridProps) {
  const [sortBy, setSortBy] = useState<"stars" | "name" | "recent">("stars");

  const sortedItems = [...items].sort((a, b) => {
    if (sortBy === "stars") return b.stars - a.stars;
    if (sortBy === "name") return a.name.localeCompare(b.name);
    return 0;
  });

  return (
    <section className="max-w-7xl mx-auto px-4 lg:px-8 pb-20">
      {/* Header Bar */}
      <div className="flex items-center justify-between mb-6 pb-3 border-b border-[#232734]">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-white">
            Discovered Capabilities
          </span>
          <span className="text-xs font-mono text-emerald-400 bg-emerald-500/10 px-2 py-0.5 rounded-full border border-emerald-500/20">
            {sortedItems.length}
          </span>
        </div>

        <div className="flex items-center gap-2 text-xs text-gray-400">
          <SlidersHorizontal className="w-3.5 h-3.5 text-gray-500" />
          <span>Sort by:</span>
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as any)}
            className="bg-[#11131a] text-gray-200 border border-[#232734] rounded-lg px-2.5 py-1 text-xs focus:outline-none focus:border-emerald-500"
          >
            <option value="stars">Most Stars</option>
            <option value="name">Alphabetical</option>
            <option value="recent">Recently Added</option>
          </select>
        </div>
      </div>

      {/* Grid */}
      {sortedItems.length > 0 ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
          {sortedItems.map((item) => (
            <ExtensionCard key={item.id} item={item} onSelect={onSelectItem} />
          ))}
        </div>
      ) : (
        <div className="py-20 text-center glass-panel rounded-2xl border border-[#232734] max-w-md mx-auto">
          <PackageSearch className="w-12 h-12 text-gray-600 mx-auto mb-3" />
          <h4 className="text-base font-medium text-white mb-1">No capabilities found</h4>
          <p className="text-xs text-gray-400">
            No matching MCP servers or skills found for &ldquo;{query}&rdquo;. Try another search term.
          </p>
        </div>
      )}
    </section>
  );
}
