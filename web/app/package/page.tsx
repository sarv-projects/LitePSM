import catalogData from "../../data/catalog.json";
import type { Listing } from "../../lib/telemetry";
import { hostUniverse, sortListings, withLinkKeys } from "../../lib/catalog";
import type { PackageViewProps } from "./PackageView";
import PackageRoute from "./PackageRoute";

/**
 * Server Component: `/package/` prerenders the route index (a described frame
 * plus the first twelve rows) and nothing else — the entry itself is keyed off
 * `?slug=`, which only the browser can read. That is also why this route is
 * the one place the dataset is requested eagerly: resolving a slug needs the
 * rows, so a deep link fetches them on its first client frame, while a bare
 * `/package/` visit fetches nothing.
 */
export default function PackagePage() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: PackageViewProps = {
    total: items.length,
    hostTotal: hostUniverse(items).length,
    indexRows: sortListings(items, "index").slice(0, 12),
  };

  return <PackageRoute {...props} />;
}
