"use client";

import React, { useEffect, useMemo, useState } from "react";
import { SearchX } from "lucide-react";
import { ExtensionCard } from "./ExtensionCard";
import { Listing } from "../../lib/telemetry";
import { formatCount } from "../../lib/format";

interface ExtensionGridProps {
  items: Listing[];
  query: string;
  loading?: boolean;
  onClearFilters?: () => void;
  /** Rendered above the rows; the caller owns the sort control. */
  toolbar?: React.ReactNode;
  hostCount?: number;
}

const PAGE = 48;
const SUGGESTIONS = ["postgres", "github", "playwright", "browser", "memory", "docx"];

export function ExtensionGrid({
  items,
  query,
  loading,
  onClearFilters,
  toolbar,
  hostCount,
}: ExtensionGridProps) {
  const [visible, setVisible] = useState(PAGE);

  useEffect(() => {
    setVisible(PAGE);
  }, [query, items]);

  // Only show skeleton rows if the (already deferred) result stays empty. A
  // search that is fast should never flash a loading state.
  const [showSkeleton, setShowSkeleton] = useState(false);
  useEffect(() => {
    if (!loading) {
      setShowSkeleton(false);
      return;
    }
    const t = window.setTimeout(() => setShowSkeleton(true), 180);
    return () => window.clearTimeout(t);
  }, [loading]);

  const shown = useMemo(() => items.slice(0, visible), [items, visible]);
  const remaining = items.length - shown.length;

  return (
    <section className="shell pb-16">
      <div className="section-head sticky top-[var(--stack-top)] z-20 -mx-px bg-paper/95 px-px backdrop-blur-sm">
        <div className="flex min-w-0 items-baseline gap-3">
          <h2>
            {query ? `Results for “${query}”` : "All capabilities"}
          </h2>
          <span className="t-mono t-tabular text-[12px] text-ink-3" aria-live="polite">
            {formatCount(items.length)} {items.length === 1 ? "entry" : "entries"}
          </span>
        </div>
        {toolbar}
      </div>

      {shown.length > 0 ? (
        <>
          <div className="t-mono hidden border-b border-rule px-3 py-2 text-[10.5px] font-medium uppercase tracking-wider text-ink-3 lg:grid lg:grid-cols-[3px_22px_minmax(0,1fr)_auto] lg:gap-x-3 items-center">
            <span />
            <span />
            <span>Capability &amp; Specifications</span>
            <span className="text-right pr-1">
              <span>Agent Compatibility</span>
            </span>
          </div>

          {shown.map((item) => (
            <ExtensionCard key={item.id} item={item} hostCount={hostCount} />
          ))}

          {remaining > 0 && (
            <div className="flex items-center justify-center gap-3 pt-6">
              <button type="button" onClick={() => setVisible((v) => v + PAGE)} className="btn">
                Show {formatCount(Math.min(PAGE, remaining))} more
              </button>
              <span className="t-mono t-tabular text-[11px] text-ink-3">
                {formatCount(shown.length)} of {formatCount(items.length)}
              </span>
            </div>
          )}
        </>
      ) : showSkeleton ? (
        <ul className="pt-px" aria-hidden="true">
          {Array.from({ length: 8 }).map((_, i) => (
            <li key={i} className="row">
              <span className="row-spine bg-rule" />
              <span className="tile bg-sunken-2" />
              <span className="min-w-0">
                <span className="block h-3 w-2/5 animate-pulse bg-sunken-2" />
                <span className="mt-2 block h-2.5 w-1/3 animate-pulse bg-sunken" />
              </span>
              <span className="row-side">
                <span className="block h-2.5 w-12 animate-pulse bg-sunken" />
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <div className="border-b border-rule py-14">
          <div className="flex max-w-prose flex-col items-start gap-3">
            <SearchX className="h-6 w-6 text-ink-3" aria-hidden="true" />
            <h3 className="text-[15px] font-semibold">Nothing matches that search</h3>
            <p className="text-[13px] text-ink-2">
              {query ? (
                <>
                  No entry in the bundled catalog contains “{query}”. Search covers names, summaries,
                  publishers, categories, runtimes and transports.
                </>
              ) : (
                <>No entry matches the current filters. Widen the kind, category or agent filter.</>
              )}
            </p>

            <div className="flex flex-wrap items-center gap-2 pt-1">
              <span className="text-[12px] text-ink-3">Try</span>
              {SUGGESTIONS.map((s) => (
                <span key={s} className="chip pointer-events-none">
                  {s}
                </span>
              ))}
            </div>

            {onClearFilters && (
              <button type="button" onClick={onClearFilters} className="btn btn-solid mt-2">
                Clear search and filters
              </button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
