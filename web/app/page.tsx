"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

/**
 * Route-level split for the catalog.
 *
 * `HomeView` statically imports `data/catalog.json` (≈2.8 MB of rows), so
 * importing it directly from this file would put that chunk in this route's
 * synchronous script set. Loading it through `next/dynamic` keeps it in one
 * shared async chunk: the framework and layout hydrate first and the catalog
 * arrives separately, at low fetch priority, for every route that renders rows.
 *
 * `ssr: true` is deliberate — the prerendered HTML still contains the real
 * sections, and React keeps that server HTML in place across the suspended
 * hydration rather than swapping in the `null` fallback.
 */
const HomeView = dynamic(() => import("./HomeView"), { ssr: true });

export default function Home() {
  return (
    <Suspense fallback={null}>
      <HomeView />
    </Suspense>
  );
}
