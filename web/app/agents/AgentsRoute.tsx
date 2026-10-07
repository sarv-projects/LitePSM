"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { AgentsViewProps } from "./AgentsView";

/** Client half of the route — see `app/HomeRoute.tsx` for why it exists. */
const AgentsView = dynamic(() => import("./AgentsView"), { ssr: true });

export default function AgentsRoute(props: AgentsViewProps) {
  return (
    <Suspense fallback={null}>
      <AgentsView {...props} />
    </Suspense>
  );
}
