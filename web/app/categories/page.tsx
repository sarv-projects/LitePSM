"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { Header } from "../../components/navigation/Header";
import { Listing, categoryFacets } from "../../lib/telemetry";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

export default function CategoriesPage() {
  const categories = useMemo(() => categoryFacets(items, 200), []);

  return (
    <div className="flex min-h-screen flex-col bg-[#f0f2f6]">
      <Header />
      <main className="mx-auto w-full max-w-7xl px-4 py-10 lg:px-8">
        <h1 className="text-2xl font-black tracking-tight text-slate-900">Categories</h1>
        <p className="mt-1 text-sm text-slate-500">
          {categories.length} categories across {items.length.toLocaleString()} catalog capabilities.
        </p>

        <div className="mt-8 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {categories.map((cat) => (
            <Link
              key={cat.name}
              href={`/explore/?category=${encodeURIComponent(cat.name)}`}
              className="flex items-center justify-between gap-3 rounded-xl border border-slate-200/80 bg-white px-4 py-3 shadow-sm transition-all hover:border-slate-300 hover:shadow-md"
            >
              <span className="truncate text-sm font-semibold text-slate-800">{cat.name}</span>
              <span className="shrink-0 font-mono text-xs text-slate-400">{cat.count.toLocaleString()}</span>
            </Link>
          ))}
        </div>
      </main>
    </div>
  );
}
