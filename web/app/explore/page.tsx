"use client";

import React, { useDeferredValue, useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { SearchBar } from "../../components/navigation/SearchBar";
import { ExtensionGrid } from "../../components/catalog/ExtensionGrid";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { Listing, categoryFacets } from "../../lib/telemetry";
import { useCatalogSearch } from "../../lib/useCatalogSearch";
import { agentFacets, hostUniverse, kindLabel, matchesHost, sortListings, SortMode } from "../../lib/catalog";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

const KINDS = ["all", "mcp", "skill", "plugin"] as const;
const SORTS: Array<{ id: SortMode; label: string }> = [
  { id: "index", label: "Index order" },
  { id: "name", label: "Name" },
  { id: "publisher", label: "Publisher" },
];

const KINDS_SET = new Set<string>(["all", "mcp", "skill", "plugin"]);
const SORTS_SET = new Set<string>(["index", "name", "publisher"]);

/**
 * Filters are seeded from the query string after mount rather than through
 * `useSearchParams`. Reading search params during render would opt this route
 * out of static prerendering, and the whole point of the build is that the
 * catalog ships as HTML. The trade-off is one frame of the unfiltered state on
 * a deep link, which is cheaper than an empty page.
 */
function useQueryFilters() {
  const [filters, setFilters] = useState({
    query: "",
    kind: "all",
    category: "all",
    agent: "all",
    verifiedOnly: false,
    sort: "index" as SortMode,
  });

  useEffect(() => {
    const p = new URLSearchParams(window.location.search);
    const sort = p.get("sort") ?? "";
    setFilters({
      query: p.get("q") ?? "",
      kind: KINDS_SET.has(p.get("kind") ?? "") ? (p.get("kind") as string) : "all",
      category: p.get("category") ?? "all",
      agent: p.get("host") ?? "all",
      verifiedOnly: p.get("verified") === "1",
      sort: SORTS_SET.has(sort) ? (sort as SortMode) : "index",
    });
  }, []);

  return [filters, setFilters] as const;
}

function ExploreContent() {
  const [filters, setFilters] = useQueryFilters();
  const { query, kind, category, agent, verifiedOnly, sort } = filters;

  const setQuery = (q: string) => setFilters((f) => ({ ...f, query: q }));
  const setKind = (k: string) => setFilters((f) => ({ ...f, kind: k }));
  const setCategory = (c: string) => setFilters((f) => ({ ...f, category: c }));
  const setAgent = (a: string) => setFilters((f) => ({ ...f, agent: a }));
  const setVerifiedOnly = (v: boolean) => setFilters((f) => ({ ...f, verifiedOnly: v }));
  const setSort = (s: SortMode) => setFilters((f) => ({ ...f, sort: s }));

  const deferredQuery = useDeferredValue(query);

  const categories = useMemo(() => categoryFacets(items, 200), []);
  const agents = useMemo(() => agentFacets(items), []);
  const hostCount = useMemo(() => hostUniverse(items).length, []);

  const { results, searching } = useCatalogSearch(items, deferredQuery, {
    kind: kind === "all" ? null : kind,
    category: category === "all" ? null : category,
  });

  const filtered = useMemo(() => {
    const scoped = results.filter((item) => {
      if (agent !== "all" && !matchesHost(item, agent)) return false;
      if (verifiedOnly && !item.publisher?.verified) return false;
      return true;
    });
    return sortListings(scoped, sort);
  }, [results, agent, verifiedOnly, sort]);

  const hasFilters =
    query !== "" || kind !== "all" || category !== "all" || agent !== "all" || verifiedOnly || sort !== "index";

  const clear = () => {
    setQuery("");
    setKind("all");
    setCategory("all");
    setAgent("all");
    setVerifiedOnly(false);
    setSort("index");
  };

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="flex-1">
        <div className="shell pt-8">
          <div className="flex flex-wrap items-baseline justify-between gap-3">
            <h1 className="t-cond text-[24px] font-semibold tracking-tight text-ink">Explore</h1>
            <p className="t-mono text-[11px] text-ink-3">
              {items.length.toLocaleString("en-US")} entries · {categories.length} categories ·{" "}
              {hostCount} hosts
            </p>
          </div>

          <div className="mt-4 max-w-2xl">
            <SearchBar
              query={query}
              setQuery={setQuery}
              totalMatches={deferredQuery ? filtered.length : undefined}
              totalCount={items.length}
            />
          </div>

          {/* Filters are a single ruled row. Kind is segmented; category and
              agent are selects because 63 and 9 options do not fit as chips. */}
          <div className="mt-5 flex flex-wrap items-center gap-x-5 gap-y-3 border-y border-rule py-3">
            <div
              className="flex items-center gap-0.5 border border-ink-3 bg-sunken p-0.5"
              role="group"
              aria-label="Capability kind"
            >
              {KINDS.map((k) => (
                <button
                  key={k}
                  type="button"
                  aria-pressed={kind === k}
                  onClick={() => setKind(k)}
                  className={`flex h-7 items-center gap-1.5 rounded-[3px] px-2.5 text-[12px] font-medium transition-colors ${
                    kind === k ? "bg-ink text-surface" : "text-ink-2 hover:text-ink"
                  }`}
                >
                  {k !== "all" && (
                    <span
                      aria-hidden="true"
                      className="h-2.5 w-[3px] rounded-[1px]"
                      style={{ backgroundColor: kind === k ? "currentColor" : `var(--${k})` }}
                    />
                  )}
                  {k === "all" ? "All types" : kindLabel(k)}
                </button>
              ))}
            </div>

            <label className="flex items-center gap-2 text-[12px] text-ink-2">
              Category
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="field"
              >
                <option value="all">All categories</option>
                {categories.map((c) => (
                  <option key={c.name} value={c.name}>
                    {c.name} ({c.count})
                  </option>
                ))}
              </select>
            </label>

            <label className="flex items-center gap-2 text-[12px] text-ink-2">
              Works with
              <select value={agent} onChange={(e) => setAgent(e.target.value)} className="field">
                <option value="all">Any agent</option>
                {agents.map((a) => (
                  <option key={a.slug} value={a.name}>
                    {a.name} ({a.count})
                  </option>
                ))}
              </select>
            </label>

            <label className="flex items-center gap-2 text-[12px] text-ink-2">
              <input
                type="checkbox"
                checked={verifiedOnly}
                onChange={(e) => setVerifiedOnly(e.target.checked)}
                className="h-3.5 w-3.5 accent-ink"
              />
              Verified publishers only
            </label>

            {hasFilters && (
              <button type="button" onClick={clear} className="btn ml-auto">
                <X className="h-3.5 w-3.5" aria-hidden="true" />
                Reset
              </button>
            )}
          </div>
        </div>

        <div className="pt-7">
          <ExtensionGrid
            items={filtered}
            query={deferredQuery}
            loading={searching}
            onClearFilters={clear}
            hostCount={hostCount}
            toolbar={
              <div className="flex shrink-0 items-center gap-2">
                <label htmlFor="explore-sort" className="t-mono text-[11px] text-ink-3">
                  Sort
                </label>
                <select
                  id="explore-sort"
                  value={sort}
                  onChange={(e) => setSort(e.target.value as SortMode)}
                  className="field !h-7 !py-0 text-[12px]"
                >
                  {SORTS.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.label}
                    </option>
                  ))}
                </select>
              </div>
            }
          />
        </div>
      </main>

      <SiteFooter />
    </div>
  );
}

export default function ExplorePage() {
  return <ExploreContent />;
}
