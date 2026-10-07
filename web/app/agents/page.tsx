"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

/**
 * Route-level split for the catalog — see `web/app/page.tsx`. `AgentsView`
 * owns the 2.8 MB `data/catalog.json` import; keeping it behind `next/dynamic`
 * leaves it out of this route's synchronous script set while `ssr: true` keeps
 * the prerendered HTML (and React's server HTML across the suspended
 * hydration) intact.
 */
const AgentsView = dynamic(() => import("./AgentsView"), { ssr: true });

export default function AgentsPage() {
  return (
    <Suspense fallback={null}>
      <AgentsView />
    </Suspense>
  );
}
