"use client";

import React, { useEffect, useMemo, useState } from "react";
import { SearchX, RotateCw } from "lucide-react";
import { ExtensionCard } from "./ExtensionCard";
import { Listing } from "../../lib/telemetry";
import { GRID_PAGE } from "../../lib/catalog";
import { formatCount } from "../../lib/format";

interface ExtensionGridProps {
  items: Listing[];
  query: string;
  /**
   * Entry count for the header line. The prerendered view starts with one page
   * of rows, so `items.length` would say "48 entries" over a catalog of 5,825;
   * the caller passes the authoritative count instead.
   */
  count?: number;
  loading?: boolean;
  /**
   * The full row set is being fetched (first search, first filter, first sort).
   * Distinct from `loading`: it starts on the interaction itself, so the empty
   * state must never flash "Nothing matches" before the rows have even been
   * requested.
   */
  awaiting?: boolean;
  /** The dataset fetch failed — say so instead of implying "no matches". */
  error?: boolean;
  onRetry?: () => void;
  onClearFilters?: () => void;
  /**
   * Fired by "Show more". On the prerendered page the browser only holds one
   * page of rows, so this is where the full row set is requested — the reader
   * asked for more, which is the interaction that justifies the fetch.
   */
  onShowMore?: () => void;
  /** Rendered above the rows; the caller owns the sort control. */
  toolbar?: React.ReactNode;
  hostCount?: number;
}

const SUGGESTIONS = ["postgres", "github", "playwright", "browser", "memory", "docx"];

/**
 * The row-set request failed. Never dressed up as "no matches": a failed
 * request and an empty result are different facts, and telling a reader the
 * catalog has nothing for them when it could not be read at all would be a
 * lie the UI is in a position to avoid. `inline` is the variant shown above
 * rows that are still on screen (a failed "show more"), the other replaces an
 * empty result set (a failed filter).
 */
function CatalogError({ onRetry, inline }: { onRetry?: () => void; inline?: boolean }) {
  return (
    <div className={inline ? "mb-4 border border-rule-2 bg-sunken px-4 py-3" : "border-b border-rule py-14"}>
      <div className="flex max-w-prose flex-col items-start gap-3">
        <RotateCw className="h-6 w-6 text-ink-3" aria-hidden="true" />
        <h3 className="text-[15px] font-semibold">The catalog rows could not be loaded</h3>
        <p className="text-[13px] text-ink-2">
          {inline
            ? "Search, filters and “show more” need the full snapshot, and that request failed — so they are unavailable right now. The rows below are the prerendered page, and their links still work."
            : "Filtering and search need the full snapshot, and that request failed. Nothing has been searched, so this is not a “no matches” answer."}
        </p>
        {onRetry && (
          <button type="button" onClick={onRetry} className="btn btn-solid mt-2">
            Try again
          </button>
        )}
      </div>
    </div>
  );
}

export function ExtensionGrid({
  items,
  query,
  count,
  loading,
  awaiting,
  error,
  onRetry,
  onClearFilters,
  onShowMore,
  toolbar,
  hostCount,
}: ExtensionGridProps) {
  const [visible, setVisible] = useState(GRID_PAGE);

  // One page of rows per query. Deliberately NOT reset when `items` changes:
  // the first change is the lazy dataset upgrade (the rows a reader asked for
  // arriving), and sending them back to 48 rows after they clicked "Show more"
  // would throw away the click that triggered it.
  useEffect(() => {
    setVisible(GRID_PAGE);
  }, [query]);

  // A slow *search* keeps the previous rows for 180ms before skeletonising (a
  // fast search must never flash a loading state); a dataset fetch has no rows
  // to keep, so it skeletonises on the same frame it starts.
  const [searchSkeleton, setSearchSkeleton] = useState(false);
  useEffect(() => {
    if (!loading) {
      setSearchSkeleton(false);
      return;
    }
    const t = window.setTimeout(() => setSearchSkeleton(true), 180);
    return () => window.clearTimeout(t);
  }, [loading]);

  const reported = count ?? items.length;
  const shown = useMemo(() => items.slice(0, visible), [items, visible]);
  const remaining = reported - shown.length;
  const showSkeleton = shown.length === 0 && (Boolean(awaiting) || searchSkeleton);
  const showMore = () => {
    setVisible((v) => v + GRID_PAGE);
    onShowMore?.();
  };

  return (
    <section className="shell pb-16">
      <div className="section-head sticky top-[var(--stack-top)] z-20 -mx-px bg-paper/95 px-px backdrop-blur-sm">
        <div className="flex min-w-0 items-baseline gap-3">
          <h2>
            {query ? `Results for “${query}”` : "All capabilities"}
          </h2>
          <span className="t-mono t-tabular text-[12px] text-ink-3" aria-live="polite">
            {formatCount(reported)} {reported === 1 ? "entry" : "entries"}
          </span>
        </div>
        {toolbar}
      </div>

      {/* A failed fetch is reported beside the rows it left untouched, not
          silently swallowed: an inert "Show more" would be a failure the reader
          has no way to diagnose. */}
      {error && shown.length > 0 && <CatalogError onRetry={onRetry} inline />}

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
              <button type="button" onClick={showMore} className="btn">
                Show {formatCount(Math.min(GRID_PAGE, remaining))} more
              </button>
              <span className="t-mono t-tabular text-[11px] text-ink-3">
                {formatCount(shown.length)} of {formatCount(reported)}
              </span>
            </div>
          )}
        </>
      ) : error ? (
        <CatalogError onRetry={onRetry} />
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
