import catalogData from "../data/catalog.json";
import type { Listing } from "../lib/telemetry";
import { deriveKindCounts, hostUniverse, withLinkKeys } from "../lib/catalog";
import {
  deriveProvenance,
  deriveStart,
  deriveTopics,
  OFFICIAL_SKILLS_CATEGORY,
} from "../lib/landing";
import { HOSTS } from "../lib/hosts";
import type { HomeViewProps } from "./HomeView";
import HomeRoute from "./HomeRoute";

/**
 * This page is a **Server Component on purpose**.
 *
 * `data/catalog.json` (~3.8 MB) is imported here, which is the only place it
 * is ever imported from: server code runs at build time during static export,
 * so its import lands in the server bundle and never in a browser chunk. What
 * crosses the boundary is `HomeViewProps` — counts, the topic taxonomy with
 * its example names, the provenance totals and twelve curated rows — serialized
 * beside the prerendered HTML it describes.
 *
 * The view itself is loaded through `HomeRoute`, a thin client wrapper that
 * owns the `next/dynamic` import, so the landing UI stays in an async chunk and
 * the route's First Load JS stays at the framework + layout baseline. The
 * browser hydrates from the props and requests the full row set only when a
 * search asks for it — see `lib/catalogData.ts`.
 *
 * Derivations live in `lib/landing.ts` (pure functions over a row set), so the
 * counts in the HTML and the counts Explore reports after a fetch come from the
 * same code path.
 */
export default function Home() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: HomeViewProps = {
    total: items.length,
    kindCounts: deriveKindCounts(items),
    hostCount: hostUniverse(items).length,
    adapterCount: HOSTS.length,
    provenance: deriveProvenance(items),
    topics: deriveTopics(items),
    start: deriveStart(items),
    officialCount: items.filter((i) => i.category === OFFICIAL_SKILLS_CATEGORY).length,
    vendorCount: items.filter((i) => i.publisher?.verified === true).length,
  };

  return <HomeRoute {...props} />;
}
