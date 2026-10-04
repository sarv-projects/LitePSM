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
import { Listing, categoryFacets, useTelemetry } from "../lib/telemetry";
import { useCatalogSearch } from "../lib/useCatalogSearch";
import { hostUniverse, verifiedItems } from "../lib/catalog";
import { formatCount } from "../lib/format";
import catalogData from "../data/catalog.json";

const SECTION_SIZE = 8;

export default function Home() {
  const items = catalogData as unknown as Listing[];
  const telemetry = useTelemetry(items);

  const [activeTab, setActiveTab] = useState("all");
  const [selectedCategory, setSelectedCategory] = useState("all");
  const [searchQuery, setSearchQuery] = useState("");

  const deferredQuery = useDeferredValue(searchQuery);

  const facets = useMemo(() => categoryFacets(items, 18), [items]);
  const hostCount = useMemo(() => hostUniverse(items).length, [items]);

  // Kind tabs count what is actually browsable in this build, not what the
  // manifest claims. A tab reading "5,185" above a list of 5,816 rows would be
  // a contradiction the reader has to resolve.
  const kindCounts = useMemo(
    () => ({
      all: items.length,
      mcp: items.filter((i) => i.kind === "mcp").length,
      skill: items.filter((i) => i.kind === "skill").length,
      plugin: items.filter((i) => i.kind === "plugin").length,
    }),
    [items]
  );

  const sections = useMemo(() => {
    // No popularity ordering exists: the catalog publishes no star, download or
    // install figures. Each section is alphabetical, which is a fact.
    const byName = (list: Listing[]) => [...list].sort((a, b) => a.name.localeCompare(b.name));

    const official = byName(verifiedItems(items.filter((i) => i.kind === "mcp"))).slice(0, SECTION_SIZE);
    const skills = byName(items.filter((i) => i.kind === "skill")).slice(0, SECTION_SIZE);
    const plugins = byName(items.filter((i) => i.kind === "plugin")).slice(0, SECTION_SIZE);
    // The dataset carries no publish timestamps, so "new" is not derivable.
    // The tail of the manifest is the only ordering fact that exists.
    const tail = items.slice(-SECTION_SIZE).reverse();

    return { official, skills, plugins, tail };
  }, [items]);

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

  const handleClearFilters = () => {
    setActiveTab("all");
    setSelectedCategory("all");
    setSearchQuery("");
  };

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "88px" }}>
      <Header activeTab={activeTab} setActiveTab={handleTabChange} counts={kindCounts} />

      <main id="main" className="flex-1">
        <HeroSection telemetry={telemetry} items={items} hostCount={hostCount} />

        <div className="shell pb-6">
          <SearchBar
            query={searchQuery}
            setQuery={setSearchQuery}
            totalMatches={deferredQuery ? filteredItems.length : undefined}
            totalCount={items.length}
            className="max-w-2xl"
          />
        </div>

        <CategoryRail
          selectedCategory={selectedCategory}
          onSelectCategory={setSelectedCategory}
          facets={facets}
          total={items.length}
        />

        {browsing && (
          <>
            <SectionRow
              title="Verified publishers"
              note="MCP servers whose publisher carries a verified flag in the upstream registry."
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
              viewAllHref="/explore/?sort=newest"
              viewAllLabel="browse the full index"
            />
            <ClientGrid />
          </>
        )}

        <div id="catalog" className="scroll-mt-24">
          <ExtensionGrid
            items={filteredItems}
            query={deferredQuery}
            loading={isFiltering}
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
