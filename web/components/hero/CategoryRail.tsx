"use client";

import React from "react";
import { formatCount } from "../../lib/format";

export interface CategoryFacet {
  name: string;
  count: number;
}

interface CategoryRailProps {
  selectedCategory: string;
  onSelectCategory: (cat: string) => void;
  facets: CategoryFacet[];
  total: number;
}

/**
 * Wrapped chips with their counts inline. Counts are part of the label, so a
 * category is never a mystery token.
 */
export function CategoryRail({ selectedCategory, onSelectCategory, facets, total }: CategoryRailProps) {
  return (
    <div className="shell pb-8">
      <div className="section-head">
        <h2>Categories</h2>
        <span className="section-note t-tabular">
          {facets.length} most common of {formatCount(total)} entries
        </span>
      </div>

      <div className="flex flex-wrap gap-1.5 pt-3" role="group" aria-label="Filter by category">
        <button
          type="button"
          aria-pressed={selectedCategory === "all"}
          onClick={() => onSelectCategory("all")}
          className="chip"
        >
          All categories
          <span className="chip-count t-tabular">{formatCount(total)}</span>
        </button>

        {facets.map((cat) => (
          <button
            key={cat.name}
            type="button"
            aria-pressed={selectedCategory === cat.name}
            onClick={() => onSelectCategory(cat.name)}
            className="chip"
          >
            <span className="max-w-[22ch] truncate">{cat.name}</span>
            <span className="chip-count t-tabular">{formatCount(cat.count)}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
