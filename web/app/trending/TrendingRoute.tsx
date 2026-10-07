"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { TrendingViewProps } from "./TrendingView";

/** Client half of the route — see `app/HomeRoute.tsx` for why it exists. */
const TrendingView = dynamic(() => import("./TrendingView"), { ssr: true });

export default function TrendingRoute(props: TrendingViewProps) {
  return (
    <Suspense fallback={null}>
      <TrendingView {...props} />
    </Suspense>
  );
}
