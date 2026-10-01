"use client";

import React from "react";

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

export function CategoryRail({ selectedCategory, onSelectCategory, facets, total }: CategoryRailProps) {
  const items: CategoryFacet[] = [
    { name: "all", count: total },
    ...facets,
  ];

  return (
    <div className="mx-auto mb-8 w-full max-w-6xl px-4">
      <div className="category-rail-mask no-scrollbar snap-x snap-mandatory overflow-x-auto overscroll-x-contain py-2">
        <div className="flex min-w-max items-center gap-2 px-4" role="group" aria-label="Filter by category">
          {items.map((cat) => {
            const isSelected = selectedCategory === cat.name;
            const label = cat.name === "all" ? "All Categories" : cat.name;
            return (
              <button
                key={cat.name}
                type="button"
                aria-pressed={isSelected}
                aria-current={isSelected ? "true" : undefined}
                onClick={() => onSelectCategory(cat.name)}
                className={`flex snap-start items-center gap-2 rounded-xl border px-3.5 py-2 text-xs font-semibold transition-all ${
                  isSelected
                    ? "border-slate-900 bg-slate-900 text-white shadow-sm"
                    : "border-slate-200/90 bg-white text-slate-600 shadow-sm hover:border-slate-300 hover:text-slate-900"
                }`}
              >
                <span className="truncate">{label}</span>
                <span
                  className={`rounded-md px-1.5 py-0.5 font-mono text-[10px] ${
                    isSelected ? "bg-slate-800 text-emerald-400" : "bg-slate-100 text-slate-500"
                  }`}
                >
                  {cat.count.toLocaleString()}
                </span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
