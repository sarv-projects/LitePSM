"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";
import type { HomeViewProps } from "./HomeView";

/**
 * The client half of the route: a Server Component cannot use `next/dynamic`
 * as a code splitter (the import resolves during prerender and the module is
 * bundled into the route's synchronous chunk), so this thin wrapper owns the
 * dynamic import. That keeps the view — and every catalog component behind it
 * — in an async chunk, which is what holds the route's First Load JS at the
 * framework + layout baseline instead of adding ~22 kB of catalog UI to it.
 *
 * `ssr: true` is deliberate: the prerendered HTML still contains the real
 * sections, and React keeps that server HTML in place across the suspended
 * hydration rather than swapping in the `null` fallback.
 */
const HomeView = dynamic(() => import("./HomeView"), { ssr: true });

export default function HomeRoute(props: HomeViewProps) {
  return (
    <Suspense fallback={null}>
      <HomeView {...props} />
    </Suspense>
  );
}
