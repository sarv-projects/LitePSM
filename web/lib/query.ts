// Client-side lexical matching over the fields a listing actually exposes.
//
// Deliberately its own module with no `"use client"` directive and no runtime
// imports: `lib/telemetry.ts` re-exports it for the search hook, and the
// build-time Server Components import it directly to count what a landing-page
// topic link will show on `/explore`. One matcher, two callers — a build-time
// count and the runtime filter cannot disagree, because they are the same
// function over the same rows.
//
// No usage, download, install or star data exists in the dataset, so nothing
// here ranks by popularity.

import type { Listing } from "./telemetry";

/** Client-side lexical search across the fields the catalog actually exposes. */
export function matchesQuery(item: Listing, q: string): boolean {
  const query = q.toLowerCase().trim();
  if (!query) return true;
  const haystack = [
    item.name,
    item.summary,
    item.category,
    item.publisher?.name,
    item.id,
    item.slug,
    item.runtime,
    item.transport,
    ...(item.tools || []).map((t) => `${t.name} ${t.description}`),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return query.split(/\s+/).every((token) => haystack.includes(token));
}
