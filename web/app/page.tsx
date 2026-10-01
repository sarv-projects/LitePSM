"use client";

import React, { useMemo, useState, useDeferredValue, useCallback } from "react";
import { Header } from "../components/navigation/Header";
import { HeroSection } from "../components/hero/HeroSection";
import { SearchBar } from "../components/navigation/SearchBar";
import { CategoryRail } from "../components/hero/CategoryRail";
import { ExtensionGrid } from "../components/catalog/ExtensionGrid";
import { SectionRow } from "../components/catalog/SectionRow";
import { ClientGrid } from "../components/home/ClientGrid";
import { LeaderboardPreview } from "../components/home/LeaderboardPreview";
import { FaqSection } from "../components/home/FaqSection";
import { Listing, TelemetryState, categoryFacets, useTelemetry } from "../lib/telemetry";
import { useCatalogSearch } from "../lib/useCatalogSearch";
import catalogData from "../data/catalog.json";

const SECTION_SIZE = 6;

export default function Home() {
  const items = catalogData as unknown as Listing[];
  const telemetry = useTelemetry(items);

  const [activeTab, setActiveTab] = useState<string>("all");
  const [selectedCategory, setSelectedCategory] = useState<string>("all");
  const [searchQuery, setSearchQuery] = useState<string>("");

  const deferredQuery = useDeferredValue(searchQuery);

  const facets = useMemo(() => categoryFacets(items, 18), [items]);

  const byStars = useMemo(
    () => (list: Listing[]) => [...list].sort((a, b) => b.stars - a.stars || a.name.localeCompare(b.name)),
    []
  );

  const sections = useMemo(() => {
    const officialMCP = byStars(items.filter((i) => i.kind === "mcp" && i.publisher?.verified)).slice(0, SECTION_SIZE);
    const topMCP = byStars(items.filter((i) => i.kind === "mcp")).slice(0, SECTION_SIZE);
    const featured = byStars(items).slice(0, SECTION_SIZE);
    const newest = items.slice(-SECTION_SIZE).reverse();
    const topSkills = byStars(items.filter((i) => i.kind === "skill")).slice(0, SECTION_SIZE);
    const plugins = byStars(items.filter((i) => i.kind === "plugin")).slice(0, SECTION_SIZE);
    const leaders = byStars(items).slice(0, 10);
    return { officialMCP, topMCP, featured, newest, topSkills, plugins, leaders };
  }, [items, byStars]);

  const { results: filteredItems, searching } = useCatalogSearch(items, deferredQuery, {
    kind: activeTab === "all" ? null : activeTab,
    category: selectedCategory === "all" ? null : selectedCategory,
  });

  const isFiltering = searching || deferredQuery !== searchQuery;

  const browsing = activeTab === "all" && selectedCategory === "all" && deferredQuery.trim() === "";

  const scrollToCatalog = useCallback(() => {
    document.getElementById("catalog")?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);

  const handleTabChange = (tab: string) => {
    setActiveTab(tab);
    setSelectedCategory("all");
    scrollToCatalog();
  };

  const handleCategoryChange = (cat: string) => {
    setSelectedCategory(cat);
    scrollToCatalog();
  };

  const handleViewAllKind = (kind: string) => {
    setActiveTab(kind);
    setSelectedCategory("all");
    scrollToCatalog();
  };

  const handleClearFilters = () => {
    setActiveTab("all");
    setSelectedCategory("all");
    setSearchQuery("");
  };

  return (
    <main className="flex min-h-screen flex-col justify-between bg-[#f0f2f6]">
      <div>
        <Header activeTab={activeTab} setActiveTab={handleTabChange} counts={telemetry.data.counts} />
        <HeroSection telemetry={telemetry} />
        <SearchBar
          query={searchQuery}
          setQuery={setSearchQuery}
          totalMatches={deferredQuery ? filteredItems.length : undefined}
          totalCount={telemetry.data.itemCount}
        />
        <CategoryRail
          selectedCategory={selectedCategory}
          onSelectCategory={handleCategoryChange}
          facets={facets}
          total={items.length}
        />

        {browsing && (
          <div className="pb-4">
            <SectionRow
              title="Official MCP Servers"
              subtitle="Verified publishers from the official registry."
              items={sections.officialMCP}
              onViewAll={() => handleViewAllKind("mcp")}
            />
            <SectionRow
              title="Featured"
              subtitle="Highest-signal capabilities across every kind."
              items={sections.featured}
              onViewAll={() => handleClearFilters()}
            />
            <SectionRow
              title="Top MCP Servers"
              subtitle="Most-starred Model Context Protocol servers."
              items={sections.topMCP}
              onViewAll={() => handleViewAllKind("mcp")}
            />
            <SectionRow
              title="New & Noteworthy"
              subtitle="Recently added to the catalog."
              items={sections.newest}
              onViewAll={() => handleClearFilters()}
            />
            <SectionRow
              title="Top Agent Skills"
              subtitle="Portable SKILL.md workflows."
              items={sections.topSkills}
              onViewAll={() => handleViewAllKind("skill")}
            />
            <SectionRow
              title="Plugins & Toolkits"
              subtitle="Curated multi-component bundles."
              items={sections.plugins}
              onViewAll={() => handleViewAllKind("plugin")}
            />
            <LeaderboardPreview items={sections.leaders} />
            <ClientGrid />
          </div>
        )}

        <div id="catalog" className="scroll-mt-24">
          <ExtensionGrid
            items={filteredItems}
            query={deferredQuery}
            loading={isFiltering}
            onClearFilters={handleClearFilters}
          />
        </div>

        {browsing && <FaqSection />}
      </div>

      <footer className="w-full border-t border-slate-200 bg-white py-8 text-center font-mono text-xs text-slate-500">
        <div className="mx-auto flex max-w-7xl flex-col items-center justify-between gap-4 px-4 sm:flex-row">
          <div>LitePSM Architecture · Universal AI Agent Capability Manager</div>
          <div className="flex items-center gap-4">
            <a
              href="https://github.com/sarv-projects/LitePSM"
              target="_blank"
              rel="noopener noreferrer"
              className="transition-colors hover:text-emerald-600"
            >
              GitHub
            </a>
            <span>·</span>
            <a href="/v1/current.json" className="transition-colors hover:text-emerald-600">
              API Telemetry
            </a>
          </div>
        </div>
      </footer>
    </main>
  );
}
