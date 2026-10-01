"use client";

import React, { useEffect, useRef } from "react";
import { Search, X } from "lucide-react";

interface SearchBarProps {
  query: string;
  setQuery: (q: string) => void;
  totalMatches?: number;
}

export function SearchBar({ query, setQuery, totalMatches }: SearchBarProps) {
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
      <div className="relative flex items-center bg-white rounded-2xl border border-slate-200/90 shadow-[0_2px_8px_rgba(15,23,42,0.04)] focus-within:border-emerald-500 focus-within:ring-4 focus-within:ring-emerald-500/10 transition-all overflow-hidden">
        <Search className="w-5 h-5 text-slate-400 ml-4 pointer-events-none" />
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search 5,185 MCP servers, skills, and plugins (e.g. postgres, github, docx)..."
          className="w-full bg-transparent px-4 py-3.5 text-sm text-slate-900 placeholder-slate-400 focus:outline-none"
        />
        {query ? (
          <div className="flex items-center gap-2 mr-3">
            {totalMatches !== undefined && (
              <span className="text-[11px] font-mono text-slate-400">
                {totalMatches} matches
              </span>
            )}
            <button
              onClick={() => setQuery("")}
              className="p-1.5 text-slate-400 hover:text-slate-700 rounded-md hover:bg-slate-100 transition-all"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        ) : (
          <div className="hidden sm:flex items-center gap-1 mr-4 pointer-events-none">
            <kbd className="px-2 py-0.5 text-[10px] font-mono bg-slate-100 text-slate-500 rounded border border-slate-200 shadow-sm font-semibold">
              ⌘K
            </kbd>
          </div>
        )}
      </div>
    </div>
  );
}
