"use client";

import React, { Suspense, useDeferredValue, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { SlidersHorizontal, X } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { SearchBar } from "../../components/navigation/SearchBar";
import { ExtensionGrid } from "../../components/catalog/ExtensionGrid";
import { Listing, categoryFacets, useTelemetry } from "../../lib/telemetry";
import { useCatalogSearch } from "../../lib/useCatalogSearch";
import { agentFacets, kindLabel, matchesHost } from "../../lib/catalog";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

function ExploreContent() {
  const params = useSearchParams();
  const search = useTelemetry(items);

  const [query, setQuery] = useState(params.get("q") ?? "");
  const [kind, setKind] = useState<string>(params.get("kind") ?? "all");
  const [category, setCategory] = useState<string>(params.get("category") ?? "all");
  const [agent, setAgent] = useState<string>(params.get("host") ?? "all");
  const [verifiedOnly, setVerifiedOnly] = useState(false);

  const deferredQuery = useDeferredValue(query);

  const categories = useMemo(() => categoryFacets(items, 60), []);
  const agents = useMemo(() => agentFacets(items), []);

  const { results, searching } = useCatalogSearch(items, deferredQuery, {
    kind: kind === "all" ? null : kind,
    category: category === "all" ? null : category,
  });

  const filtered = useMemo(() => {
    return results.filter((item) => {
      if (agent !== "all" && !matchesHost(item, agent)) return false;
      if (verifiedOnly && !item.publisher?.verified) return false;
      return true;
    });
  }, [results, agent, verifiedOnly]);

  const hasFilters =
    query !== "" || kind !== "all" || category !== "all" || agent !== "all" || verifiedOnly;

  const clear = () => {
    setQuery("");
    setKind("all");
    setCategory("all");
    setAgent("all");
    setVerifiedOnly(false);
  };

  return (
    <div className="flex min-h-screen flex-col bg-[#f0f2f6]">
      <Header />

      <main className="mx-auto w-full max-w-7xl px-4 pt-8 lg:px-8">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="text-2xl font-black tracking-tight text-slate-900">Explore</h1>
            <p className="mt-0.5 text-sm text-slate-500">
              {items.length.toLocaleString()} capabilities across MCP servers, agent skills, and plugins.
            </p>
          </div>
          {hasFilters && (
            <button
              type="button"
              onClick={clear}
              className="inline-flex items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-3 py-1.5 text-xs font-semibold text-slate-600 transition-colors hover:bg-slate-50"
            >
              <X className="h-3.5 w-3.5" aria-hidden="true" /> Clear filters
            </button>
          )}
        </div>

        <div className="mb-4 max-w-none">
          <SearchBar
            query={query}
            setQuery={setQuery}
            totalMatches={deferredQuery ? filtered.length : undefined}
            totalCount={search.data.itemCount || items.length}
          />
        </div>

        <div className="mb-6 flex flex-col gap-3 rounded-2xl border border-slate-200/80 bg-white p-4 lg:flex-row lg:items-center">
          <div className="flex items-center gap-1 rounded-xl border border-slate-200 bg-slate-100 p-1 text-xs" role="group" aria-label="Package type">
            {["all", "mcp", "skill", "plugin"].map((k) => (
              <button
                key={k}
                type="button"
                aria-pressed={kind === k}
                onClick={() => setKind(k)}
                className={`rounded-lg px-3 py-1.5 font-semibold transition-all ${
                  kind === k ? "bg-white text-slate-900 shadow-sm" : "text-slate-600 hover:text-slate-900"
                }`}
              >
                {k === "all" ? "All types" : kindLabel(k)}
              </button>
            ))}
          </div>

          <div className="flex flex-1 flex-wrap items-center gap-3">
            <label className="flex items-center gap-1.5 text-xs text-slate-500">
              <SlidersHorizontal className="h-3.5 w-3.5 text-slate-400" aria-hidden="true" />
              Category
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="rounded-xl border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-800 shadow-sm focus:border-emerald-500 focus:outline-none"
              >
                <option value="all">All categories</option>
                {categories.map((c) => (
                  <option key={c.name} value={c.name}>
                    {c.name} ({c.count})
                  </option>
                ))}
              </select>
            </label>

            <label className="flex items-center gap-1.5 text-xs text-slate-500">
              Works with
              <select
                value={agent}
                onChange={(e) => setAgent(e.target.value)}
                className="rounded-xl border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-800 shadow-sm focus:border-emerald-500 focus:outline-none"
              >
                <option value="all">Any agent</option>
                {agents.map((a) => (
                  <option key={a.slug} value={a.name}>
                    {a.name} ({a.count})
                  </option>
                ))}
              </select>
            </label>

            <label className="flex items-center gap-2 text-xs font-medium text-slate-600">
              <input
                type="checkbox"
                checked={verifiedOnly}
                onChange={(e) => setVerifiedOnly(e.target.checked)}
                className="h-3.5 w-3.5 rounded border-slate-300 text-emerald-600 focus:ring-emerald-500"
              />
              Verified publishers only
            </label>
          </div>
        </div>
      </main>

      <ExtensionGrid
        items={filtered}
        query={deferredQuery}
        loading={searching}
        onClearFilters={clear}
      />
    </div>
  );
}

export default function ExplorePage() {
  return (
    <Suspense fallback={<div className="p-12 text-center font-mono text-slate-500">Loading explore...</div>}>
      <ExploreContent />
    </Suspense>
  );
}
