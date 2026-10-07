"use client";

import React, { useMemo, useState, useDeferredValue, useCallback } from "react";
import { Header } from "../components/navigation/Header";
import { HeroSection } from "../components/hero/HeroSection";
import { SearchBar } from "../components/navigation/SearchBar";
import { CategoryRail } from "../components/hero/CategoryRail";
import { ExtensionGrid } from "../components/catalog/ExtensionGrid";
import { SectionRow } from "../components/catalog/SectionRow";
import { ClientGrid } from "../components/home/ClientGrid";
import { FaqSection } from "../components/home/FaqSection";
import { SiteFooter } from "../components/layout/SiteFooter";
import { Listing, useTelemetry } from "../lib/telemetry";
import { useCatalogSearch } from "../lib/useCatalogSearch";
import { useCatalog } from "../lib/catalogData";
import {
  categoryFacets,
  deriveKindCounts,
  homeSections,
  hostUniverse,
  kindBreakdown,
} from "../lib/catalog";
import { formatCount } from "../lib/format";

/**
 * Every field here is computed by `app/page.tsx` — a Server Component — from
 * `data/catalog.json` at build time, and serialized into the prerendered HTML.
 * That is what keeps the dataset out of this module: nothing here imports the
 * rows, so hydration runs off the props alone and the reader sees real content
 * (counts, facets, four sections, one page of rows) before any dataset request
 * happens. The full rows arrive later, through `useCatalog`, only when a
 * search, a filter or "Show more" needs them — and when they do, they are
 * re-derived with the same helpers the build used, so no number moves.
 */
export interface HomeViewProps {
  total: number;
  kindCounts: { all: number; mcp: number; skill: number; plugin: number };
  hostCount: number;
  breakdown: Array<{ kind: Listing["kind"]; count: number; share: number }>;
  facets: Array<{ name: string; count: number }>;
  sections: {
    official: Listing[];
    skills: Listing[];
    plugins: Listing[];
    tail: Listing[];
  };
  /** First page of the grid, in index order — the rows the HTML already has. */
  gridRows: Listing[];
}

export default function Home(props: HomeViewProps) {
  const { status, items: full, request } = useCatalog();
  const telemetry = useTelemetry(props.kindCounts);

  const [activeTab, setActiveTab] = useState("all");
  const [selectedCategory, setSelectedCategory] = useState("all");
  const [searchQuery, setSearchQuery] = useState("");

  const deferredQuery = useDeferredValue(searchQuery);

  const live = useMemo(() => {
    if (!full) return null;
    return {
      total: full.length,
      kindCounts: deriveKindCounts(full),
      hostCount: hostUniverse(full).length,
      breakdown: kindBreakdown(full),
      facets: categoryFacets(full, 18),
      sections: homeSections(full),
    };
  }, [full]);

  const total = live ? live.total : props.total;
  const kindCounts = live ? live.kindCounts : props.kindCounts;
  const hostCount = live ? live.hostCount : props.hostCount;
  const breakdown = live ? live.breakdown : props.breakdown;
  const facets = live ? live.facets : props.facets;
  const sections = live ? live.sections : props.sections;

  const { results: filteredItems, searching } = useCatalogSearch(full ?? [], deferredQuery, {
    kind: activeTab === "all" ? null : activeTab,
    category: selectedCategory === "all" ? null : selectedCategory,
  });

  const activeFilters =
    activeTab !== "all" || selectedCategory !== "all" || deferredQuery.trim() !== "";

  const isFiltering = searching || deferredQuery !== searchQuery;
  const browsing = !activeFilters;
  // Until the rows land the grid shows exactly what the HTML already has; a
  // filter set before then has nothing to filter, so it shows the skeleton
  // (never the "Nothing matches" state — nothing has been matched against yet).
  const gridItems = full ? filteredItems : activeFilters ? [] : props.gridRows;

  const scrollToCatalog = useCallback(() => {
    document.getElementById("catalog")?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);

  const handleTabChange = (tab: string) => {
    request();
    setActiveTab(tab);
    setSelectedCategory("all");
    scrollToCatalog();
  };

  const handleCategory = (cat: string) => {
    request();
    setSelectedCategory(cat);
  };

  const handleClearFilters = () => {
    setActiveTab("all");
    setSelectedCategory("all");
    setSearchQuery("");
  };

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "88px" }}>
      <Header activeTab={activeTab} setActiveTab={handleTabChange} counts={kindCounts} />

      <main id="main" className="flex-1">
        <HeroSection telemetry={telemetry} total={total} breakdown={breakdown} hostCount={hostCount} />

        <div className="shell pb-6">
          <SearchBar
            query={searchQuery}
            setQuery={(q) => {
              request();
              setSearchQuery(q);
            }}
            totalMatches={deferredQuery && full ? filteredItems.length : undefined}
            totalCount={total}
            className="max-w-2xl"
          />
        </div>

        <CategoryRail
          selectedCategory={selectedCategory}
          onSelectCategory={handleCategory}
          facets={facets}
          total={total}
        />

        {browsing && (
          <>
            <SectionRow
              title="Verified publishers"
              note="MCP servers listed in the publisher's own repository manifest (not an audit)."
              items={sections.official}
              hostCount={hostCount}
              viewAllHref="/explore/?kind=mcp&verified=1"
              viewAllLabel="see all MCP servers"
            />
            <SectionRow
              title="Agent skills"
              note="Portable SKILL.md workflows that agents load on demand."
              items={sections.skills}
              hostCount={hostCount}
              viewAllHref="/explore/?kind=skill"
              viewAllLabel={`see all ${formatCount(kindCounts.skill)} skills`}
            />
            <SectionRow
              title="Plugins"
              note="Multi-component toolkits that bundle skills, MCP servers and hooks."
              items={sections.plugins}
              hostCount={hostCount}
              viewAllHref="/explore/?kind=plugin"
              viewAllLabel={`see all ${formatCount(kindCounts.plugin)} plugins`}
            />
            <SectionRow
              title="End of the release manifest"
              note="The last entries in this catalog snapshot. The dataset carries no publish timestamps, so no recency claim is made."
              items={sections.tail}
              hostCount={hostCount}
              viewAllHref="/explore/"
              viewAllLabel="browse the full index"
            />
            <ClientGrid />
          </>
        )}

        <div id="catalog" className="scroll-mt-24">
          <ExtensionGrid
            items={gridItems}
            query={deferredQuery}
            count={full ? filteredItems.length : total}
            loading={isFiltering}
            awaiting={status === "loading"}
            error={status === "error"}
            onRetry={request}
            onShowMore={request}
            onClearFilters={handleClearFilters}
            hostCount={hostCount}
          />
        </div>

        {browsing && <FaqSection />}
      </main>

      <SiteFooter />
    </div>
  );
}
