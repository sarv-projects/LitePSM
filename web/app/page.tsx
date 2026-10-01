"use client";

import React, { useState, useMemo } from "react";
import { Header } from "../components/navigation/Header";
import { HeroSection } from "../components/hero/HeroSection";
import { SearchBar } from "../components/navigation/SearchBar";
import { CategoryRail } from "../components/hero/CategoryRail";
import { ExtensionGrid } from "../components/catalog/ExtensionGrid";
import { DetailDrawer } from "../components/catalog/DetailDrawer";
import { ExtensionItem } from "../components/catalog/ExtensionCard";
import catalogData from "../data/catalog.json";

export default function Home() {
  const [activeTab, setActiveTab] = useState<string>("all");
  const [selectedCategory, setSelectedCategory] = useState<string>("all");
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [selectedItem, setSelectedItem] = useState<ExtensionItem | null>(null);

  const items = catalogData as ExtensionItem[];

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
        const matchesTools = item.tools.some((t) => t.name.toLowerCase().includes(q));

        if (!matchesName && !matchesSummary && !matchesCategory && !matchesPublisher && !matchesTools) {
          return false;
        }
      }

      return true;
    });
  }, [items, activeTab, selectedCategory, searchQuery]);

  const [toastMessage, setToastMessage] = useState<string | null>(null);

  const showToast = (msg: string) => {
    setToastMessage(msg);
    setTimeout(() => {
      setToastMessage(null);
    }, 2500);
  };

  const handleTabChange = (tab: string) => {
    if (typeof document !== "undefined" && "startViewTransition" in document) {
      (document as any).startViewTransition(() => setActiveTab(tab));
    } else {
      setActiveTab(tab);
    }
  };

  const handleCategoryChange = (cat: string) => {
    if (typeof document !== "undefined" && "startViewTransition" in document) {
      (document as any).startViewTransition(() => setSelectedCategory(cat));
    } else {
      setSelectedCategory(cat);
    }
  };

  const handleSelectItem = (item: ExtensionItem | null) => {
    if (typeof document !== "undefined" && "startViewTransition" in document) {
      (document as any).startViewTransition(() => setSelectedItem(item));
    } else {
      setSelectedItem(item);
    }
  };

  return (
    <main className="min-h-screen flex flex-col justify-between">
      <div>
        <Header activeTab={activeTab} setActiveTab={handleTabChange} />
        <HeroSection />
        <SearchBar query={searchQuery} setQuery={setSearchQuery} />
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

      <DetailDrawer item={selectedItem} onClose={() => handleSelectItem(null)} />

      {/* Modern Toast Notification (persistent-toast-notifications) */}
      {toastMessage && (
        <div
          role="status"
          aria-live="polite"
          className="fixed bottom-6 right-6 z-50 flex items-center gap-2 bg-[#171a23] border border-emerald-500/40 text-emerald-400 px-4 py-2.5 rounded-xl shadow-2xl backdrop-blur-md text-xs font-mono animate-fadeIn"
        >
          <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
          <span>{toastMessage}</span>
        </div>
      )}

      {/* Footer */}
      <footer className="w-full border-t border-[#232734] py-8 text-center text-xs text-gray-500 font-mono">
        <div className="max-w-7xl mx-auto px-4 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div>LitePSM Architecture · Universal AI Agent Capability Manager</div>
          <div className="flex items-center gap-4">
            <a
              href="https://github.com/sarv-projects/LitePSM"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-emerald-400 transition-colors"
            >
              GitHub
            </a>
            <span>·</span>
            <a href="/v1/current.json" className="hover:text-emerald-400 transition-colors">
              API Telemetry
            </a>
          </div>
        </div>
      </footer>
    </main>
  );
}
