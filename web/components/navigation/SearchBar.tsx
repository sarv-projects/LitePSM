"use client";

import React, { useEffect, useRef } from "react";
import { Search, X } from "lucide-react";

interface SearchBarProps {
  query: string;
  setQuery: (q: string) => void;
  totalMatches?: number;
  totalCount: number;
}

function isEditableTarget(el: EventTarget | null): boolean {
  const node = el as HTMLElement | null;
  if (!node) return false;
  const tag = node.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || node.isContentEditable;
}

export function SearchBar({ query, setQuery, totalMatches, totalCount }: SearchBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const typing = isEditableTarget(document.activeElement);
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        inputRef.current?.focus();
        return;
      }
      // "/" focuses search only when not already typing somewhere else.
      if (e.key === "/" && !typing) {
        e.preventDefault();
        inputRef.current?.focus();
      }
      if (e.key === "Escape" && document.activeElement === inputRef.current) {
        inputRef.current?.blur();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <div className="mx-auto mb-6 w-full max-w-2xl px-4">
      <div className="relative flex items-center overflow-hidden rounded-2xl border border-slate-200/90 bg-white shadow-[0_2px_8px_rgba(15,23,42,0.04)] transition-all focus-within:border-emerald-500 focus-within:ring-4 focus-within:ring-emerald-500/10">
        <Search className="pointer-events-none ml-4 h-5 w-5 text-slate-400" aria-hidden="true" />
        <input
          ref={inputRef}
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search capabilities"
          placeholder={`Search ${totalCount.toLocaleString()} MCP servers, skills, and plugins (e.g. postgres, github, docx)...`}
          className="w-full bg-transparent px-4 py-3.5 text-sm text-slate-900 placeholder-slate-400 focus:outline-none"
        />
        {query ? (
          <div className="mr-3 flex items-center gap-2">
            {totalMatches !== undefined && (
              <span className="font-mono text-[11px] text-slate-400" aria-live="polite">
                {totalMatches} matches
              </span>
            )}
            <button
              type="button"
              onClick={() => setQuery("")}
              aria-label="Clear search"
              className="rounded-md p-1.5 text-slate-400 transition-all hover:bg-slate-100 hover:text-slate-700"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>
        ) : (
          <div className="pointer-events-none mr-4 hidden items-center gap-1 sm:flex">
            <kbd className="rounded border border-slate-200 bg-slate-100 px-2 py-0.5 font-mono text-[10px] font-semibold text-slate-500 shadow-sm">
              ⌘K
            </kbd>
          </div>
        )}
      </div>
    </div>
  );
}
