"use client";

// Lazily-fetched catalog rows.
//
// The 5,825-row dataset is NOT part of any JavaScript bundle: the build-time
// pages prerender from `data/catalog.json` through a Server Component (server
// bundles never reach the browser), and the browser fetches the same bytes as
// a plain JSON document — exported byte-for-byte by `app/data/catalog.json/
// route.ts` — only when a page actually needs the full row set.
//
// Timing is the point of the design. A first visit therefore downloads the
// page's HTML and ~107 kB of First Load JS and nothing else; the dataset is
// requested on the first interaction that needs it (typing in search, changing
// a filter, opening an entry), and never on a passive visit. Until then the
// prerendered rows are on screen and stay on screen — this module does not
// replace them with a spinner.

import { useCallback, useEffect, useState } from "react";
import type { Listing } from "./telemetry";
import { withLinkKeys } from "./catalog";
import bundledRelease from "../data/release.json";

/** Rows as served: `web/data/catalog.json`, re-keyed with `withLinkKeys`. */
export const CATALOG_PATH = "/data/catalog.json";

/**
 * Cache-buster bound to the dataset itself, not to the deploy: `datasetDigest`
 * is the sha256 the release pointer publishes over these rows, so a digest
 * change means the bytes changed. It keeps a CDN that cached the previous
 * dataset from answering a newer build with stale rows (which would silently
 * disagree with the prerendered HTML beside them).
 */
const DATASET_VERSION = (() => {
  const digest = (bundledRelease as Record<string, unknown>).datasetDigest;
  return typeof digest === "string" && digest.length > 0
    ? `?v=${encodeURIComponent(digest)}`
    : "";
})();

export const CATALOG_URL = `${CATALOG_PATH}${DATASET_VERSION}`;

export type CatalogStatus = "idle" | "loading" | "ready" | "error";

let cached: Listing[] | null = null;
let inflight: Promise<Listing[]> | null = null;

function fetchCatalog(): Promise<Listing[]> {
  if (cached) return Promise.resolve(cached);
  if (inflight) return inflight;
  inflight = fetch(CATALOG_URL, { cache: "no-cache" })
    .then((res) => {
      if (!res.ok) throw new Error(`catalog fetch failed: HTTP ${res.status}`);
      return res.json();
    })
    .then((rows) => {
      const items = withLinkKeys(rows as unknown as Listing[]);
      cached = items;
      inflight = null;
      return items;
    })
    .catch((err) => {
      inflight = null;
      throw err;
    });
  return inflight;
}

/** Kick off the dataset fetch. Idempotent; concurrent callers share one request. */
export function loadCatalog(): Promise<Listing[]> {
  return fetchCatalog();
}

/**
 * Row-set state for a view that starts from prerendered props and upgrades to
 * the full catalog when it needs to filter, search or resolve an entry.
 *
 * `idle` is the normal first-visit state and must stay that way until something
 * asks for more: views call `request()` from an interaction, never from render.
 * `ready` is sticky for the session, so navigating between routes costs
 * nothing after the first request.
 */
export function useCatalog(): {
  status: CatalogStatus;
  items: Listing[] | null;
  request: () => void;
} {
  const [status, setStatus] = useState<CatalogStatus>(() => (cached ? "ready" : "idle"));
  const [items, setItems] = useState<Listing[] | null>(() => cached);

  const request = useCallback(() => {
    // A session that already has the rows needs no request; a failed one is
    // allowed to try again, so `error → loading` is a legal transition.
    setStatus((s) => (s === "idle" || s === "error" ? "loading" : s));
  }, []);

  useEffect(() => {
    if (status !== "loading") return;
    let alive = true;
    fetchCatalog()
      .then((rows) => {
        if (!alive) return;
        setItems(rows);
        setStatus("ready");
      })
      .catch(() => {
        if (!alive) return;
        setStatus("error");
      });
    return () => {
      alive = false;
    };
  }, [status]);

  return { status, items, request };
}
