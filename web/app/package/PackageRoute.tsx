"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { PackageViewProps } from "./PackageView";

/** Client half of the route — see `app/HomeRoute.tsx` for why it exists. */
const PackageView = dynamic(() => import("./PackageView"), { ssr: true });

export default function PackageRoute(props: PackageViewProps) {
  return (
    <Suspense fallback={null}>
      <PackageView {...props} />
    </Suspense>
  );
}
