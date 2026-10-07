"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { CategoriesViewProps } from "./CategoriesView";

/** Client half of the route — see `app/HomeRoute.tsx` for why it exists. */
const CategoriesView = dynamic(() => import("./CategoriesView"), { ssr: true });

export default function CategoriesRoute(props: CategoriesViewProps) {
  return (
    <Suspense fallback={null}>
      <CategoriesView {...props} />
    </Suspense>
  );
}
