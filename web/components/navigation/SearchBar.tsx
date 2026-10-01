"use client";

import React, { useEffect, useId, useRef, useState } from "react";
import { Search, X } from "lucide-react";
import { formatCount } from "../../lib/format";

interface SearchBarProps {
  query: string;
  setQuery: (q: string) => void;
  totalMatches?: number;
  totalCount: number;
  /** Extra classes so the same control can sit in the hero or a filter bar. */
  className?: string;
  autoFocusOnMount?: boolean;
}

function isEditableTarget(el: EventTarget | null): boolean {
  const node = el as HTMLElement | null;
  if (!node) return false;
  const tag = node.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || node.isContentEditable;
}

export function SearchBar({
  query,
  setQuery,
  totalMatches,
  totalCount,
  className = "",
  autoFocusOnMount = false,
}: SearchBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const labelId = useId();
  const [narrow, setNarrow] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia("(max-width: 640px)");
    const sync = () => setNarrow(mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);

  useEffect(() => {
    if (autoFocusOnMount) inputRef.current?.focus();
  }, [autoFocusOnMount]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const typing = isEditableTarget(document.activeElement);
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        inputRef.current?.focus();
        inputRef.current?.select();
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
    <div className={className}>
      <label htmlFor={labelId} className="sr-only">
        Search the capability catalog by name, summary, publisher, category, runtime or transport
      </label>
      {/* The search field is the primary control on the site, so it is the
          only one with a 2px ink border. That also means the focus indicator
          never disappears while the caret is inside the field. */}
      <div className="flex h-11 items-center gap-2 border-2 border-ink bg-surface px-2.5">
        <Search className="h-4 w-4 shrink-0 text-ink-3" aria-hidden="true" />
        <input
          ref={inputRef}
          id={labelId}
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          spellCheck={false}
          autoComplete="off"
          aria-describedby={totalMatches !== undefined ? `${labelId}-count` : undefined}
          // Short on a 390px viewport, so the example terms move below rather
          // than being clipped mid-word.
          placeholder={narrow ? `Search ${formatCount(totalCount)} capabilities` : `Search ${formatCount(totalCount)} capabilities — postgres, github, playwright…`}
          className="w-full min-w-0 bg-transparent text-[14px] text-ink outline-none placeholder:text-ink-3"
        />

        {query ? (
          <span className="flex shrink-0 items-center gap-2">
            {totalMatches !== undefined && (
              <span id={`${labelId}-count`} className="t-mono t-tabular text-[11px] text-ink-3" aria-live="polite">
                {formatCount(totalMatches)} found
              </span>
            )}
            <button type="button" onClick={() => setQuery("")} aria-label="Clear search" className="btn !h-6 !px-1.5">
              <X className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </span>
        ) : (
          <kbd className="t-mono hidden shrink-0 border border-rule bg-sunken px-1.5 py-0.5 text-[10px] text-ink-3 sm:block">
            /
          </kbd>
        )}
      </div>
    </div>
  );
}
