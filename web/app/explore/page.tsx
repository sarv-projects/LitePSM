import catalogData from "../../data/catalog.json";
import type { Listing } from "../../lib/telemetry";
import {
  agentFacets,
  categoryFacets,
  GRID_PAGE,
  hostUniverse,
  withLinkKeys,
} from "../../lib/catalog";
import type { ExploreViewProps } from "./ExploreView";
import ExploreRoute from "./ExploreRoute";

/**
 * Server Component: the full catalog is read here, at build time, and only
 * the slice this route shows (facets, totals, one page of rows) is serialized
 * into the HTML. The dataset itself never enters a client chunk — search and
 * filtering fetch it on first use (`lib/catalogData.ts`), so a first visit to
 * `/explore/` downloads the page, not 3.8 MB of rows it has not asked for.
 *
 * The view loads through `ExploreRoute` so its UI stays in an async chunk and
 * the route's First Load JS stays at the shared baseline.
 */
export default function ExplorePage() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: ExploreViewProps = {
    total: items.length,
    categories: categoryFacets(items, 200),
    agents: agentFacets(items),
    hostCount: hostUniverse(items).length,
    gridRows: items.slice(0, GRID_PAGE),
  };

  return <ExploreRoute {...props} />;
}
