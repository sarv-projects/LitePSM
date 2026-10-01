"use client";

import React, { useMemo, useState, useDeferredValue } from "react";
import { Header } from "../components/navigation/Header";
import { HeroSection } from "../components/hero/HeroSection";
import { SearchBar } from "../components/navigation/SearchBar";
import { CategoryRail } from "../components/hero/CategoryRail";
import { ExtensionGrid } from "../components/catalog/ExtensionGrid";
import { DetailDrawer } from "../components/catalog/DetailDrawer";
import { Listing, TelemetryState, matchesQuery, categoryFacets, useTelemetry } from "../lib/telemetry";
import catalogData from "../data/catalog.json";

export default function Home() {
  const items = catalogData as unknown as Listing[];
  const telemetry = useTelemetry(items);

  const [activeTab, setActiveTab] = useState<string>("all");
  const [selectedCategory, setSelectedCategory] = useState<string>("all");
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [selectedItem, setSelectedItem] = useState<Listing | null>(null);

  // Deferred query keeps typing responsive while filtering 5k+ rows.
  const deferredQuery = useDeferredValue(searchQuery);
  const isFiltering = deferredQuery !== searchQuery;

  const facets = useMemo(() => categoryFacets(items, 18), [items]);

  const filteredItems = useMemo(() => {
    return items.filter((item) => {
      if (activeTab !== "all" && item.kind !== activeTab) return false;
      if (selectedCategory !== "all" && item.category !== selectedCategory) return false;
      return matchesQuery(item, deferredQuery);
    });
  }, [items, activeTab, selectedCategory, deferredQuery]);

  const handleTabChange = (tab: string) => {
    setActiveTab(tab);
    setSelectedCategory("all");
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
          onSelectCategory={setSelectedCategory}
          facets={facets}
          total={items.length}
        />
        <ExtensionGrid
          items={filteredItems}
          onSelectItem={setSelectedItem}
          query={deferredQuery}
          loading={isFiltering}
          onClearFilters={handleClearFilters}
        />
      </div>

      <DetailDrawer item={selectedItem} onClose={() => setSelectedItem(null)} />

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
