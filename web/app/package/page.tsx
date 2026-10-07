"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

/**
 * Route-level split for the catalog — see `web/app/page.tsx`. `PackageView`
 * owns the 2.8 MB `data/catalog.json` import; keeping it behind `next/dynamic`
 * leaves it out of this route's synchronous script set while `ssr: true` keeps
 * the prerendered shell (and React's server HTML across the suspended
 * hydration) intact.
 */
const PackageView = dynamic(() => import("./PackageView"), { ssr: true });

export default function PackagePage() {
  return (
    <Suspense fallback={null}>
      <PackageView />
    </Suspense>
  );
}
