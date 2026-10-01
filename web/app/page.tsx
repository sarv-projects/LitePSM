"use client";

import React, { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { Header } from "../components/navigation/Header";
import { HeroSection } from "../components/hero/HeroSection";
import { SearchBar } from "../components/navigation/SearchBar";
import { CategoryRail } from "../components/hero/CategoryRail";
import { ExtensionGrid } from "../components/catalog/ExtensionGrid";
import { ExtensionItem } from "../components/catalog/ExtensionCard";
import catalogData from "../data/catalog.json";

export default function Home() {
  const router = useRouter();
  const [activeTab, setActiveTab] = useState<string>("all");
  const [selectedCategory, setSelectedCategory] = useState<string>("all");
  const [searchQuery, setSearchQuery] = useState<string>("");

  const items = catalogData as ExtensionItem[];

  const counts = useMemo(() => {
    let mcp = 0;
    let skill = 0;
    let plugin = 0;
    for (const it of items) {
      if (it.kind === "mcp") mcp++;
      else if (it.kind === "skill") skill++;
      else if (it.kind === "plugin") plugin++;
    }
    return { all: items.length, mcp, skill, plugin };
  }, [items]);

  const filteredItems = useMemo(() => {
    return items.filter((item) => {
      // Filter by Top Navigation Tab
      if (activeTab !== "all" && item.kind !== activeTab) {
        return false;
      }

      // Filter by Category Rail
      if (selectedCategory !== "all" && item.category !== selectedCategory) {
        return false;
      }

      // Filter by Search Query
      if (searchQuery.trim() !== "") {
        const q = searchQuery.toLowerCase();
        const matchesName = item.name.toLowerCase().includes(q);
        const matchesSummary = item.summary.toLowerCase().includes(q);
        const matchesCategory = item.category.toLowerCase().includes(q);
        const matchesPublisher = item.publisher.name.toLowerCase().includes(q);
        const matchesTools = item.tools?.some((t) => t.name.toLowerCase().includes(q)) || false;

        if (!matchesName && !matchesSummary && !matchesCategory && !matchesPublisher && !matchesTools) {
          return false;
        }
      }

      return true;
    });
  }, [items, activeTab, selectedCategory, searchQuery]);

  const handleTabChange = (tab: string) => {
    setActiveTab(tab);
  };

  const handleCategoryChange = (cat: string) => {
    setSelectedCategory(cat);
  };

  // Dedicated Detail Page Navigation: Opens dedicated page with agent file paths & JSON/TOML snippet
  const handleSelectItem = (item: ExtensionItem) => {
    router.push(`/item?id=${encodeURIComponent(item.id)}`);
  };

  return (
    <main className="min-h-screen flex flex-col justify-between bg-[#f0f2f6]">
      <div>
        <Header activeTab={activeTab} setActiveTab={handleTabChange} counts={counts} />
        <HeroSection />
        <SearchBar
          query={searchQuery}
          setQuery={setSearchQuery}
          totalMatches={searchQuery ? filteredItems.length : undefined}
        />
        <CategoryRail
          selectedCategory={selectedCategory}
          onSelectCategory={handleCategoryChange}
        />
        <ExtensionGrid
          items={filteredItems}
          onSelectItem={handleSelectItem}
          query={searchQuery}
        />
      </div>

      {/* Footer */}
      <footer className="w-full bg-white border-t border-slate-200 py-8 text-center text-xs text-slate-500 font-mono">
        <div className="max-w-7xl mx-auto px-4 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div>LitePSM Architecture · Universal AI Agent Capability Manager</div>
          <div className="flex items-center gap-4">
            <a
              href="https://github.com/sarv-projects/LitePSM"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-emerald-600 transition-colors"
            >
              GitHub
            </a>
            <span>·</span>
            <a href="/v1/current.json" className="hover:text-emerald-600 transition-colors">
              API Telemetry ({items.length.toLocaleString()} Items)
            </a>
          </div>
        </div>
      </footer>
    </main>
  );
}
