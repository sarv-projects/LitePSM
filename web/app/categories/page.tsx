"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

/**
 * Route-level split for the catalog.
 *
 * `CategoriesView` statically imports `data/catalog.json` (~2.8 MB of rows), so
 * importing it directly from this file would put that chunk in every route's
 * synchronous script set. Loading it through `next/dynamic` keeps it in an
 * async chunk: the framework and layout hydrate first and the catalog arrives
 * as a separate, cacheable request shared by every route that renders rows.
 *
 * `ssr: true` is deliberate — the prerendered HTML still contains the real
 * category index, and React keeps that server HTML in place across the
 * suspended hydration rather than swapping in the `null` fallback.
 */
const CategoriesView = dynamic(() => import("./CategoriesView"), { ssr: true });

export default function CategoriesPage() {
  return (
    <Suspense fallback={null}>
      <CategoriesView />
    </Suspense>
  );
}
