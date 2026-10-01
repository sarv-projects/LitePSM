"use client";

// Client-side catalog search that prefers an off-thread MiniSearch worker and
// transparently degrades to in-thread matching when workers are unavailable.

import { useEffect, useRef, useState } from "react";
import type { Listing } from "./telemetry";
import { matchesQuery } from "./telemetry";
import type { ListingKind, WorkerInboundMessage, WorkerOutboundMessage } from "./search.worker";

export interface CatalogSearchFilters {
  kind?: string | null;
  category?: string | null;
}

export interface CatalogSearchState {
  results: Listing[];
  searching: boolean;
  mode: "worker" | "fallback";
}

const DEBOUNCE_MS = 120;

/** Cheap content signature so an identical listing set never re-indexes. */
function listSignature(listings: Listing[]): string {
  if (listings.length === 0) return "0";
  const first = listings[0]?.id ?? "";
  const last = listings[listings.length - 1]?.id ?? "";
  return `${listings.length}:${first}:${last}`;
}

function matchesFilters(
  listing: Listing,
  kind?: string | null,
  category?: string | null
): boolean {
  if (kind && listing.kind !== kind) return false;
  if (category && listing.category !== category) return false;
  return true;
}

/** In-thread equivalent of the worker search, used when no worker is available. */
function searchInThread(
  listings: Listing[],
  query: string,
  kind?: string | null,
  category?: string | null
): Listing[] {
  const trimmed = query.trim();
  return listings.filter((listing) => {
    if (!matchesFilters(listing, kind, category)) return false;
    if (!trimmed) return true;
    return matchesQuery(listing, trimmed);
  });
}

function toListingMap(listings: Listing[]): Map<string, Listing> {
  const map = new Map<string, Listing>();
  for (const listing of listings) {
    if (listing?.id) map.set(listing.id, listing);
  }
  return map;
}

/**
 * Search the catalog with a MiniSearch Web Worker, falling back to main-thread
 * matching. Results always reflect the latest query/filter combination.
 */
export function useCatalogSearch(
  listings: Listing[],
  query: string,
  filters: CatalogSearchFilters
): CatalogSearchState {
  const kind = filters.kind ?? null;
  const category = filters.category ?? null;

  const [mode, setMode] = useState<"worker" | "fallback">("worker");
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<Listing[]>(() =>
    searchInThread(listings, query, kind, category)
  );

  const workerRef = useRef<Worker | null>(null);
  const listingsRef = useRef<Listing[]>(listings);
  const listingMapRef = useRef<Map<string, Listing>>(toListingMap(listings));
  const requestIdRef = useRef(0);
  const initializedSigRef = useRef<string | null>(null);
  const lastKeyRef = useRef<string | null>(null);

  // Create the worker lazily on the client and seed it with the catalog.
  useEffect(() => {
    if (typeof window === "undefined" || typeof Worker === "undefined") {
      setMode("fallback");
      return;
    }

    let worker: Worker;
    try {
      worker = new Worker(new URL("./search.worker.ts", import.meta.url), {
        type: "module",
      });
    } catch {
      setMode("fallback");
      return;
    }

    workerRef.current = worker;

    worker.onmessage = (event: MessageEvent<WorkerOutboundMessage>) => {
      const message = event.data;
      if (!message || typeof message !== "object") return;
      if (message.type !== "results") return;
      // Ignore responses that are not for the most recently posted search.
      if (message.id !== requestIdRef.current) return;

      const map = listingMapRef.current;
      const next: Listing[] = [];
      for (const id of message.ids) {
        const listing = map.get(id);
        if (listing) next.push(listing);
      }
      setResults(next);
      setSearching(false);
    };

    worker.onerror = () => {
      // Worker failed at runtime: degrade to in-thread matching.
      setMode("fallback");
    };

    initializedSigRef.current = listSignature(listingsRef.current);
    const initMessage: WorkerInboundMessage = {
      type: "init",
      listings: listingsRef.current,
    };
    try {
      worker.postMessage(initMessage);
    } catch {
      setMode("fallback");
    }

    return () => {
      worker.onmessage = null;
      worker.onerror = null;
      worker.terminate();
      workerRef.current = null;
    };
  }, []);

  // Drive searches from listing/query/filter changes, debounced.
  useEffect(() => {
    listingMapRef.current = toListingMap(listings);

    const sig = listSignature(listings);
    const key = `${mode}::${sig}::${query}::${kind ?? ""}::${category ?? ""}`;

    if (mode === "fallback") {
      if (lastKeyRef.current === key) return;
      lastKeyRef.current = key;
      setResults(searchInThread(listings, query, kind, category));
      setSearching(false);
      return;
    }

    const worker = workerRef.current;
    if (!worker) {
      // Worker not available yet, but stay correct while we wait.
      setResults(searchInThread(listings, query, kind, category));
      return;
    }

    const needsInit = initializedSigRef.current !== sig;
    if (!needsInit && lastKeyRef.current === key) return;
    lastKeyRef.current = key;

    if (needsInit) {
      initializedSigRef.current = sig;
      const initMessage: WorkerInboundMessage = { type: "init", listings };
      try {
        worker.postMessage(initMessage);
      } catch {
        setMode("fallback");
        return;
      }
    }

    const id = requestIdRef.current + 1;
    requestIdRef.current = id;
    setSearching(true);

    const timer = window.setTimeout(() => {
      const searchMessage: WorkerInboundMessage = {
        type: "search",
        id,
        query,
        kind: kind as ListingKind | null,
        category,
      };
      try {
        worker.postMessage(searchMessage);
      } catch {
        setMode("fallback");
      }
    }, DEBOUNCE_MS);

    return () => window.clearTimeout(timer);
  }, [listings, query, kind, category, mode]);

  return { results, searching, mode };
}
