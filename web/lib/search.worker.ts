/// <reference lib="webworker" />

// Dedicated Web Worker that owns a MiniSearch index over the catalog.
// The main thread sends the full listing set once (and again if it changes),
// then asks for ranked ids. All matching state lives here so the UI thread
// stays responsive even for large catalogs.

import MiniSearch from "minisearch";
import type { Listing } from "./telemetry";

export type ListingKind = "mcp" | "skill" | "plugin";

/** Messages the main thread sends to the worker. */
export type WorkerInboundMessage =
  | { type: "init"; listings: Listing[] }
  | {
      type: "search";
      id: number;
      query: string;
      kind?: ListingKind | null;
      category?: string | null;
    };

/** Messages the worker sends back to the main thread. */
export type WorkerOutboundMessage =
  | { type: "ready"; count: number }
  | { type: "results"; id: number; ids: string[] };

/** Flattened document shape that MiniSearch actually indexes. */
interface IndexDoc {
  id: string;
  name: string;
  slug: string;
  summary: string;
  category: string;
  publisher: string;
  keywords: string;
}

const SEARCH_FIELDS = ["name", "slug", "summary", "category", "publisher", "keywords"];

/** Bound as a dedicated worker; `dom` types would otherwise type this as Window. */
const scope = self as unknown as DedicatedWorkerGlobalScope;

let index: MiniSearch<IndexDoc> | null = null;
const listingsById = new Map<string, Listing>();
// Searches that arrive before the index exists are queued and flushed on init.
const pending: Array<Extract<WorkerInboundMessage, { type: "search" }>> = [];
const MAX_PENDING = 25;

function post(message: WorkerOutboundMessage): void {
  scope.postMessage(message);
}

/** Collect the non-obvious searchable text: runtime, transport and tool names. */
function buildKeywords(listing: Listing): string {
  const parts: string[] = [];
  if (listing.runtime) parts.push(listing.runtime);
  if (listing.transport) parts.push(listing.transport);
  for (const tool of listing.tools ?? []) {
    if (tool?.name) parts.push(tool.name);
    if (tool?.description) parts.push(tool.description);
  }
  return parts.join(" ");
}

function toIndexDoc(listing: Listing): IndexDoc {
  return {
    id: listing.id,
    name: listing.name ?? "",
    slug: listing.slug ?? "",
    summary: listing.summary ?? "",
    category: listing.category ?? "",
    // `publisher` is an object; flatten it to its name for indexing.
    publisher: listing.publisher?.name ?? "",
    keywords: buildKeywords(listing),
  };
}

function initIndex(listings: Listing[]): void {
  listingsById.clear();
  const docs: IndexDoc[] = [];
  for (const listing of listings) {
    if (!listing || !listing.id) continue;
    listingsById.set(listing.id, listing);
    docs.push(toIndexDoc(listing));
  }

  const engine = new MiniSearch<IndexDoc>({
    fields: SEARCH_FIELDS,
    storeFields: [],
    searchOptions: {
      prefix: true,
      fuzzy: 0.2,
      boost: { name: 3, slug: 2, category: 1.5, summary: 1 },
    },
  });
  engine.addAll(docs);
  index = engine;

  post({ type: "ready", count: docs.length });
  flushPending();
}

function matchesFilters(
  listing: Listing,
  kind?: ListingKind | null,
  category?: string | null
): boolean {
  if (kind && listing.kind !== kind) return false;
  if (category && listing.category !== category) return false;
  return true;
}

function allMatchingIds(kind?: ListingKind | null, category?: string | null): string[] {
  const ids: string[] = [];
  for (const [id, listing] of listingsById) {
    if (matchesFilters(listing, kind, category)) ids.push(id);
  }
  return ids;
}

function runSearch(message: Extract<WorkerInboundMessage, { type: "search" }>): string[] {
  const { kind, category } = message;
  const query = (message.query ?? "").trim();

  // Empty query means "browse": every listing that passes the filters.
  if (!query) return allMatchingIds(kind, category);
  if (!index) return [];

  const ids: string[] = [];
  for (const result of index.search(query)) {
    const id = String(result.id);
    const listing = listingsById.get(id);
    if (listing && matchesFilters(listing, kind, category)) ids.push(id);
  }
  return ids;
}

function respond(message: Extract<WorkerInboundMessage, { type: "search" }>): void {
  try {
    post({ type: "results", id: message.id, ids: runSearch(message) });
  } catch {
    // Never crash the worker on a bad index/search; report "no results".
    post({ type: "results", id: message.id, ids: [] });
  }
}

function flushPending(): void {
  while (pending.length > 0) {
    const message = pending.shift();
    if (message) respond(message);
  }
}

function handleSearch(message: Extract<WorkerInboundMessage, { type: "search" }>): void {
  if (!index) {
    if (pending.length >= MAX_PENDING) pending.shift();
    pending.push(message);
    return;
  }
  respond(message);
}

scope.onmessage = (event: MessageEvent<WorkerInboundMessage>): void => {
  const message = event.data;
  try {
    if (!message || typeof message !== "object") return;
    if (message.type === "init") {
      initIndex(Array.isArray(message.listings) ? message.listings : []);
      return;
    }
    if (message.type === "search") {
      handleSearch(message);
    }
  } catch {
    if (message && typeof message === "object" && message.type === "search") {
      post({ type: "results", id: message.id, ids: [] });
    } else {
      post({ type: "ready", count: 0 });
    }
  }
};

export {};
