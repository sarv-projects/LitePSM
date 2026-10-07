import catalogData from "../data/catalog.json";
import type { Listing } from "../lib/telemetry";
import {
  categoryFacets,
  deriveKindCounts,
  GRID_PAGE,
  homeSections,
  hostUniverse,
  kindBreakdown,
  withLinkKeys,
} from "../lib/catalog";
import type { HomeViewProps } from "./HomeView";
import HomeRoute from "./HomeRoute";

/**
 * This page is a **Server Component on purpose**.
 *
 * `data/catalog.json` (~3.8 MB) is imported here, which is the only place it
 * is ever imported from: server code runs at build time during static export,
 * so its import lands in the server bundle and never in a browser chunk. What
 * crosses the boundary is `HomeViewProps` — counts, facets, four sections of
 * eight rows and one page of grid rows, a few dozen kilobytes — serialized
 * beside the prerendered HTML it describes.
 *
 * The view itself is loaded through `HomeRoute`, a thin client wrapper that
 * owns the `next/dynamic` import, so the catalog UI stays in an async chunk
 * and the route's First Load JS stays at the framework + layout baseline. The
 * browser hydrates from the props and requests the full row set only when a
 * search, a filter or "Show more" asks for it — see `lib/catalogData.ts`.
 */
export default function Home() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: HomeViewProps = {
    total: items.length,
    kindCounts: deriveKindCounts(items),
    hostCount: hostUniverse(items).length,
    breakdown: kindBreakdown(items),
    facets: categoryFacets(items, 18),
    sections: homeSections(items),
    gridRows: items.slice(0, GRID_PAGE),
  };

  return <HomeRoute {...props} />;
}
