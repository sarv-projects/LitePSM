import catalogData from "../../data/catalog.json";
import type { Listing } from "../../lib/telemetry";
import { categoryIndex, withLinkKeys } from "../../lib/catalog";
import type { CategoriesViewProps } from "./CategoriesView";
import CategoriesRoute from "./CategoriesRoute";

/**
 * Server Component: the category index is pure aggregation over the full
 * snapshot, computed at build time. The route ships its counts as props and
 * never fetches the dataset in the browser — there is nothing here that a row
 * list would be needed for.
 */
export default function CategoriesPage() {
  const items = withLinkKeys(catalogData as unknown as Listing[]);

  const props: CategoriesViewProps = {
    total: items.length,
    categories: categoryIndex(items),
  };

  return <CategoriesRoute {...props} />;
}
