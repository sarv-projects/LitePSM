"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

/**
 * Route-level split for the catalog — see `web/app/page.tsx`. `TrendingView`
 * owns the 2.8 MB `data/catalog.json` import; keeping it behind `next/dynamic`
 * leaves it out of this route's synchronous script set while `ssr: true` keeps
 * the prerendered HTML (and React's server HTML across the suspended
 * hydration) intact.
 */
const TrendingView = dynamic(() => import("./TrendingView"), { ssr: true });

export default function TrendingPage() {
  return (
    <Suspense fallback={null}>
      <TrendingView />
    </Suspense>
  );
}
