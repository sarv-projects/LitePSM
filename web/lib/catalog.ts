"use client";

// Catalog derivation helpers shared by Explore, Trending, Agents and Categories.
// Everything is computed from the static catalog; no usage/download data exists
// in the v1 dataset, so popularity means stars only.

import { Listing } from "./telemetry";
import catalogData from "../data/catalog.json";

export const KIND_ORDER: Listing["kind"][] = ["mcp", "skill", "plugin"];

const items = catalogData as unknown as Listing[];

export function kindLabel(kind: string): string {
  switch (kind) {
    case "mcp":
      return "MCP Server";
    case "skill":
      return "Agent Skill";
    case "plugin":
      return "Plugin";
    default:
      return kind;
  }
}

export type SortMode = "stars" | "name" | "newest";

export function sortListings<T extends Listing>(items: T[], mode: SortMode): T[] {
  const copy = [...items];
  if (mode === "name") return copy.sort((a, b) => a.name.localeCompare(b.name));
  if (mode === "newest") return copy.reverse();
  return copy.sort((a, b) => b.stars - a.stars || a.name.localeCompare(b.name));
}

/** Distinct agents advertised across `testedHosts`, with compatible package counts. */
export function agentFacets(items: Listing[]): Array<{ name: string; slug: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    for (const host of item.testedHosts || []) {
      const key = host.trim();
      if (key) counts.set(key, (counts.get(key) ?? 0) + 1);
    }
  }
  return Array.from(counts, ([name, count]) => ({ name, slug: slugifyAgent(name), count })).sort(
    (a, b) => b.count - a.count || a.name.localeCompare(b.name)
  );
}

export function slugifyAgent(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

export function matchesHost(item: Listing, agentName: string | null): boolean {
  if (!agentName) return true;
  const target = agentName.toLowerCase();
  return (item.testedHosts || []).some((h) => h.toLowerCase() === target);
}

export function publisherFacets(items: Listing[], limit = 24): Array<{ name: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    const name = item.publisher?.name?.trim();
    if (!name) continue;
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }
  return Array.from(counts, ([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, limit);
}

export function topStarred(items: Listing[], n: number): Listing[] {
  return sortListings(items, "stars").slice(0, n);
}

export function verifiedItems(items: Listing[]): Listing[] {
  return items.filter((i) => i.publisher?.verified);
}

/** Distinct host names across the whole catalog - the denominator for coverage. */
export function hostUniverse(items: Listing[]): string[] {
  const hosts = new Set<string>();
  for (const item of items) {
    for (const host of item.testedHosts || []) {
      const key = host.trim();
      if (key) hosts.add(key);
    }
  }
  return Array.from(hosts).sort((a, b) => a.localeCompare(b));
}

/** Share of the catalog each kind represents, for the proportional bar. */
export function kindBreakdown(items: Listing[]): Array<{
  kind: Listing["kind"];
  count: number;
  share: number;
}> {
  const total = items.length || 1;
  return KIND_ORDER.map((kind) => {
    const count = items.filter((i) => i.kind === kind).length;
    return { kind, count, share: count / total };
  });
}

/**
 * `slug` is NOT unique in this dataset: 121 slugs are shared by 402 entries
 * (up to 73 answer to one slug, e.g. `skill-creator`). Emitting
 * `?slug=<slug>` for those would send a reader to whichever entry happened to
 * be indexed last.
 *
 * So: the catalog id is the unique key and the slug is the readable one. Where
 * a slug is unambiguous we use it, and where it is not we fall back to the id.
 * Both go in the same `?slug=` parameter, so the route shape is unchanged.
 *
 * Counts are computed once per dataset rather than per render.
 */
const SLUG_COUNTS = (() => {
  const counts = new Map<string, number>();
  for (const item of items) counts.set(item.slug, (counts.get(item.slug) ?? 0) + 1);
  return counts;
})();

/** Canonical link target for a listing. Always `/package/?slug=<key>`. */
export function listingHref(item: Listing): string {
  const key = SLUG_COUNTS.get(item.slug) === 1 ? item.slug : item.id;
  return `/package/?slug=${encodeURIComponent(key)}`;
}

/** Category counts broken down by kind, for the categories index. */
export interface CategoryFacet {
  name: string;
  count: number;
  byKind: Record<Listing["kind"], number>;
  share: number;
}

export function categoryIndex(items: Listing[]): CategoryFacet[] {
  const map = new Map<string, CategoryFacet>();
  let max = 1;
  for (const item of items) {
    const name = item.category?.trim();
    if (!name) continue;
    let facet = map.get(name);
    if (!facet) {
      facet = { name, count: 0, byKind: { mcp: 0, skill: 0, plugin: 0 }, share: 0 };
      map.set(name, facet);
    }
    facet.count += 1;
    facet.byKind[item.kind] += 1;
    if (facet.count > max) max = facet.count;
  }
  return Array.from(map.values())
    .map((facet) => ({ ...facet, share: facet.count / max }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}
