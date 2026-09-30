"use client";

import React, { useEffect, useRef } from "react";
import { Search, X } from "lucide-react";

interface SearchBarProps {
  query: string;
  setQuery: (q: string) => void;
}

export function SearchBar({ query, setQuery }: SearchBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        inputRef.current?.focus();
      }
      if (e.key === "/" && document.activeElement !== inputRef.current) {
        e.preventDefault();
        inputRef.current?.focus();
      }
      if (e.key === "Escape" && document.activeElement === inputRef.current) {
        inputRef.current?.blur();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  return (
    <div className="w-full max-w-2xl mx-auto mb-6 px-4">
      <div className="relative flex items-center glass-panel rounded-2xl border border-[#232734] shadow-xl focus-within:border-emerald-500/60 focus-within:ring-2 focus-within:ring-emerald-500/20 transition-all overflow-hidden">
        <Search className="w-5 h-5 text-gray-400 ml-4 pointer-events-none" />
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search MCP servers, skills, and plugins (e.g. postgres, github, release)..."
          className="w-full bg-transparent px-4 py-3.5 text-sm text-white placeholder-gray-500 focus:outline-none"
        />
        {query ? (
          <button
            onClick={() => setQuery("")}
            className="p-1.5 mr-3 text-gray-400 hover:text-white rounded-md hover:bg-[#232734] transition-all"
          >
            <X className="w-4 h-4" />
          </button>
        ) : (
          <div className="hidden sm:flex items-center gap-1 mr-4 pointer-events-none">
            <kbd className="px-2 py-0.5 text-[11px] font-mono bg-[#171a23] text-gray-400 rounded border border-[#232734] shadow-sm">
              ⌘K
            </kbd>
          </div>
        )}
      </div>
    </div>
  );
}
