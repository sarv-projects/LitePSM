"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { ExploreViewProps } from "./ExploreView";

/** Client half of the route — see `app/HomeRoute.tsx` for why it exists. */
const ExploreView = dynamic(() => import("./ExploreView"), { ssr: true });

export default function ExploreRoute(props: ExploreViewProps) {
  return (
    <Suspense fallback={null}>
      <ExploreView {...props} />
    </Suspense>
  );
}
