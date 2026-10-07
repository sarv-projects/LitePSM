import catalogData from "../../data/catalog.json";
import type { Listing } from "../../lib/telemetry";
import { agentFacets, hostUniverse, withLinkKeys } from "../../lib/catalog";
import type { AgentsViewProps } from "./AgentsView";
import AgentsRoute from "./AgentsRoute";

/**
 * Server Component: host coverage is an aggregate over the full snapshot,
 * computed at build time and passed as props. Like `/categories/` and
 * `/trending/`, this route never requests the dataset in the browser.
 */
export default function AgentsPage() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: AgentsViewProps = {
    hostCount: hostUniverse(items).length,
    agents: agentFacets(items),
  };

  return <AgentsRoute {...props} />;
}
