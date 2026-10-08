"use client";

import React, { useState } from "react";
import Link from "next/link";
import { SearchX, RotateCw } from "lucide-react";
import { CapabilityCard } from "../catalog/CapabilityCard";
import type { Listing } from "../../lib/telemetry";
import type { AgentChoice } from "../../lib/landing";
import { formatCount } from "../../lib/format";

const RESULTS_PAGE_SIZE = 24;

/**
 * The hero search's result surface — cards, because this is a discovery page,
 * not the comparison table Explore is. Explore keeps `ExtensionGrid` and the
 * ruled `CatalogRow` density untouched.
 *
 * Fetch states are the same three the E1 grid distinguishes, and they are
 * distinguished for the same reason: a request that has not started, a request
 * that failed, and zero matches are three different facts. The reader is never
 * shown "Nothing matches" for rows that were never fetched.
 */
export interface SearchResultsProps {
  items: Listing[];
  query: string;
  /** Authoritative count once the rows are in; before that, nothing. */
  count?: number;
  loading: boolean;
  awaiting: boolean;
  error: boolean;
  onRetry: () => void;
  onClear: () => void;
  agent: AgentChoice | null;
  total: number;
}

export function SearchResults({
  items,
  query,
  count,
  loading,
  awaiting,
  error,
  onRetry,
  onClear,
  agent,
  total,
}: SearchResultsProps) {
  const reported = count ?? items.length;
  const showSkeleton = items.length === 0 && awaiting;
  const [page, setPage] = useState<{ query: string; limit: number }>({
    query,
    limit: RESULTS_PAGE_SIZE,
  });
  // Tie pagination to the visible query so a new search starts at its first
  // page immediately, without waiting for an effect to reset component state.
  const visibleLimit = page.query === query ? page.limit : RESULTS_PAGE_SIZE;
  const visibleItems = items.slice(0, visibleLimit);
  const remaining = Math.max(0, items.length - visibleItems.length);

  return (
    <section className="shell band" aria-labelledby="results-title">
      <div className="flex flex-wrap items-baseline justify-between gap-3 border-b border-rule pb-3">
        <div className="min-w-0">
          <h2 id="results-title" className="text-[18px] font-semibold tracking-tight text-ink">
            {query ? <>Results for &ldquo;{query}&rdquo;</> : "Search the catalog"}
          </h2>
          <p
            className="mt-0.5 text-[12.5px] text-ink-3"
            role="status"
            aria-live="polite"
            aria-atomic="true"
          >
            Showing{" "}
            <span className="t-mono t-tabular text-ink">{formatCount(visibleItems.length)}</span> of{" "}
            <span className="t-mono t-tabular text-ink">{formatCount(reported)}</span> results ·{" "}
            <span className="t-mono t-tabular">{formatCount(total)}</span> entries
            {loading && " · updating results…"}
            {awaiting && " · loading the full snapshot…"}
            {error && " · the snapshot could not be loaded"}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <Link href={`/explore/?q=${encodeURIComponent(query)}`} className="touch-link text-[13px] text-ink-2 hover:text-accent-text hover:underline">
            Open in Explore →
          </Link>
          <button type="button" onClick={onClear} className="btn">
            Clear search
          </button>
        </div>
      </div>

      {error && (
        <div className="mt-4 border border-rule-2 bg-sunken px-4 py-3">
          <div className="flex max-w-prose flex-col items-start gap-3">
            <RotateCw className="h-5 w-5 text-ink-3" aria-hidden="true" />
            <p className="text-[13px] text-ink-2">
              The catalog rows could not be loaded, so this is not a &ldquo;no matches&rdquo; answer —
              nothing has been searched yet.
            </p>
            <button type="button" onClick={onRetry} className="btn btn-solid">
              Try again
            </button>
          </div>
        </div>
      )}

      {items.length > 0 ? (
        <ul className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {visibleItems.map((item) => (
            <li key={item.id}>
              <CapabilityCard item={item} agent={agent} className="h-full" />
            </li>
          ))}
        </ul>
      ) : showSkeleton ? (
        <ul className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-3" aria-hidden="true">
          {Array.from({ length: 6 }).map((_, i) => (
            <li key={i} className="rounded-card border border-rule bg-surface p-4">
              <span className="block h-3.5 w-1/2 animate-pulse rounded-chip bg-sunken-2" />
              <span className="mt-3 block h-2.5 w-full animate-pulse bg-sunken" />
              <span className="mt-2 block h-2.5 w-4/5 animate-pulse bg-sunken" />
              <span className="mt-4 block h-4 w-24 animate-pulse rounded-chip bg-sunken" />
            </li>
          ))}
        </ul>
      ) : !error ? (
        <div className="mt-6 flex max-w-prose flex-col items-start gap-3 border-b border-rule pb-12">
          <SearchX className="h-6 w-6 text-ink-3" aria-hidden="true" />
          <h3 className="text-[15px] font-semibold">Nothing matches that search</h3>
          <p className="text-[13px] text-ink-2">
            No entry in the bundled catalog contains &ldquo;{query}&rdquo;. Search covers names,
            summaries, publishers, categories, runtimes and transports.
          </p>
          <button type="button" onClick={onClear} className="btn btn-solid mt-1">
            Clear search
          </button>
        </div>
      ) : null}

      {remaining > 0 && (
        <div className="mt-6 flex justify-center">
          <button
            type="button"
            onClick={() =>
              setPage((current) => ({
                query,
                limit:
                  (current.query === query ? current.limit : RESULTS_PAGE_SIZE) + RESULTS_PAGE_SIZE,
              }))
            }
            className="btn"
          >
            Show {formatCount(Math.min(RESULTS_PAGE_SIZE, remaining))} more
          </button>
        </div>
      )}
    </section>
  );
}
