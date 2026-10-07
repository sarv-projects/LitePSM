import catalogData from "../../data/catalog.json";
import type { Listing } from "../../lib/telemetry";
import {
  agentFacets,
  categoryIndex,
  kindBreakdown,
  kindLabel,
  publisherFacets,
  withLinkKeys,
} from "../../lib/catalog";
import type { TrendingViewProps } from "./TrendingView";
import TrendingRoute from "./TrendingRoute";

/**
 * Server Component: the coverage boards are counts over the full snapshot,
 * computed at build time. Rows are never rendered here, so the browser gets a
 * few hundred bytes of board data and no reason to fetch the dataset at all.
 */
export default function TrendingPage() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: TrendingViewProps = {
    total: items.length,
    boards: {
      categories: categoryIndex(items)
        .slice(0, 15)
        .map((c) => ({ label: c.name, count: c.count })),
      hosts: agentFacets(items)
        .slice(0, 15)
        .map((a) => ({ label: a.name, count: a.count })),
      publishers: publisherFacets(items, 15).map((p) => ({ label: p.name, count: p.count })),
      kinds: kindBreakdown(items)
        .map((k) => ({ label: kindLabel(k.kind), count: k.count, kind: k.kind }))
        .sort((a, b) => b.count - a.count),
    },
  };

  return <TrendingRoute {...props} />;
}
