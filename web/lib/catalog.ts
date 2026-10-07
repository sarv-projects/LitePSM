// Catalog derivation helpers shared by Explore, Coverage, Agents and Categories.
// Everything is computed from a row set that is handed in — no module state, no
// JSON import, no `"use client"`. That is deliberate:
//
//   * the build-time pages (Server Components) import `data/catalog.json` and
//     call these helpers to prerender real HTML, and
//   * the browser calls them again on the row set it fetches lazily at
//     /data/catalog.json after the first interaction.
//
// A module that statically imported the ~3.8 MB dataset would drag it into
// every route's client bundle, which is exactly what this split removes. Both
// sides therefore derive from the same functions over the same rows, so the
// prerendered numbers and the post-fetch numbers cannot disagree.
//
// No usage, download, install or star data exists in the dataset, so no helper
// here ranks by popularity.

import type { Listing } from "./telemetry";
import { hostsFor } from "./hosts";

export const KIND_ORDER: Listing["kind"][] = ["mcp", "skill", "plugin"];

/** Rows rendered per page by ExtensionGrid; also the prerendered slice. */
export const GRID_PAGE = 48;

/** Rows per discovery section on the home page. */
export const SECTION_SIZE = 8;

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

/**
 * Sort modes are limited to axes the data actually carries.
 *
 * "stars" and "newest" were removed: the catalog publishes no star counts (the
 * upstream sources do not expose them) and carries no publish timestamps, so
 * both modes were ordering by nothing while their labels claimed otherwise.
 */
export type SortMode = "index" | "name" | "publisher";

export function sortListings<T extends Listing>(items: T[], mode: SortMode): T[] {
  const copy = [...items];
  if (mode === "name") return copy.sort((a, b) => a.name.localeCompare(b.name));
  if (mode === "publisher") {
    return copy.sort(
      (a, b) =>
        (a.publisher?.name || "").localeCompare(b.publisher?.name || "") ||
        a.name.localeCompare(b.name)
    );
  }
  // "index" is the builder's own order: verified publishers first, then kind,
  // then name. Deterministic and identical to the release artifact.
  return copy;
}

/** Distinct agents this catalog can install into, with compatible package counts. */
export function agentFacets(items: Listing[]): Array<{ name: string; slug: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    for (const host of hostsFor(item)) {
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
  return hostsFor(item).some((h) => h.toLowerCase() === target);
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

export function topByPublisher(items: Listing[], n: number): Listing[] {
  return sortListings(items, "publisher").slice(0, n);
}

export function verifiedItems(items: Listing[]): Listing[] {
  return items.filter((i) => i.publisher?.verified);
}

/** Distinct host names across the whole catalog - the denominator for coverage. */
export function hostUniverse(items: Listing[]): string[] {
  const hosts = new Set<string>();
  for (const item of items) {
    for (const host of hostsFor(item)) {
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

/** What is browsable in this row set, counted rather than trusted from a manifest. */
export function deriveKindCounts(items: Listing[]): {
  all: number;
  mcp: number;
  skill: number;
  plugin: number;
} {
  let mcp = 0;
  let skill = 0;
  let plugin = 0;
  for (const item of items) {
    if (item.kind === "mcp") mcp += 1;
    else if (item.kind === "skill") skill += 1;
    else if (item.kind === "plugin") plugin += 1;
  }
  return { all: items.length, mcp, skill, plugin };
}

/** Distinct categories with counts, ordered by frequency (then name). */
export function categoryFacets(items: Listing[], limit = 16): Array<{ name: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    const key = item.category?.trim();
    if (!key) continue;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return Array.from(counts, ([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, limit);
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
 * `withLinkKeys` resolves that once per row set — on the server at build time
 * and in the browser after the lazily-fetched rows arrive — so the link target
 * rides along on the row instead of needing the whole row set in every
 * component that renders a card. Counts are computed over the full set, never
 * over the slice a page happens to render.
 */
export function withLinkKeys(items: Listing[]): Listing[] {
  const counts = new Map<string, number>();
  for (const item of items) counts.set(item.slug, (counts.get(item.slug) ?? 0) + 1);
  return items.map((item) => ({
    ...item,
    linkKey: counts.get(item.slug) === 1 ? item.slug : item.id,
  }));
}

/** Canonical link target for a listing. Always `/package/?slug=<key>`. */
export function listingHref(item: Listing): string {
  // `linkKey` is absent only for a row that never passed `withLinkKeys`; the id
  // always resolves, so that is the safe fallback (never the ambiguous slug).
  const key = item.linkKey ?? item.id;
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

/**
 * The four discovery sections on the home page.
 *
 * No popularity ordering exists: the catalog publishes no star, download or
 * install figures. Each section is alphabetical, which is a fact, and the tail
 * is the manifest's own last rows — the dataset carries no publish timestamps,
 * so "new" is not derivable.
 */
export function homeSections(items: Listing[], size = SECTION_SIZE): {
  official: Listing[];
  skills: Listing[];
  plugins: Listing[];
  tail: Listing[];
} {
  const byName = (list: Listing[]) => [...list].sort((a, b) => a.name.localeCompare(b.name));
  return {
    official: byName(verifiedItems(items.filter((i) => i.kind === "mcp"))).slice(0, size),
    skills: byName(items.filter((i) => i.kind === "skill")).slice(0, size),
    plugins: byName(items.filter((i) => i.kind === "plugin")).slice(0, size),
    tail: items.slice(-size).reverse(),
  };
}
