"use client";

import React from "react";
import Link from "next/link";
import { CapabilityCard } from "../catalog/CapabilityCard";
import type { Listing } from "../../lib/telemetry";
import type { AgentChoice } from "../../lib/landing";
import { OFFICIAL_SKILLS_CATEGORY } from "../../lib/landing";
import { formatCount } from "../../lib/format";

/**
 * "Good places to start" — the home grid, and the only selection on this page
 * that could be mistaken for an opinion, so the rule that produced it is
 * printed underneath it.
 *
 * The dataset publishes no stars, downloads or install figures, so there is no
 * honest way to say "popular", "trending" or "top". What it DOES publish is
 * provenance: a category for skills the publisher itself ships, and a
 * `vendor-manifest` flag for rows read out of the publisher's own repository
 * manifest. Those are the two filters, both one click away, both reproducible
 * in Explore.
 */
export interface StartSectionProps {
  items: Listing[];
  officialCount: number;
  vendorCount: number;
  agent: AgentChoice | null;
}

export function StartSection({ items, officialCount, vendorCount, agent }: StartSectionProps) {
  if (!items.length) return null;

  return (
    <section className="shell band" aria-labelledby="start-title">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="kicker">Good places to start</p>
          <h2 id="start-title" className="band-title mt-2">
            Twelve entries worth opening first
          </h2>
        </div>
      </div>

      <ul className="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {items.map((item) => (
          <li key={item.id}>
            <CapabilityCard item={item} agent={agent} className="h-full" />
          </li>
        ))}
      </ul>

      <div className="mt-5 flex flex-col gap-2 border-t border-rule pt-4">
        <p className="text-[13px] leading-relaxed text-ink-2">
          Selected because they come from official publishers — not a popularity ranking.{" "}
          <span className="text-ink-3">
            The catalog publishes no star, download or install figures, so no ranking exists to show.
          </span>
        </p>
        <p className="flex flex-wrap items-center gap-x-5 gap-y-1 text-[12.5px] text-ink-3">
          <Link
            href={`/explore/?category=${encodeURIComponent(OFFICIAL_SKILLS_CATEGORY)}`}
            className="touch-link text-ink-2 underline decoration-ink-3 underline-offset-4 hover:text-accent-text hover:decoration-accent-text"
          >
            See all {formatCount(officialCount)} org-published skills →
          </Link>
          <Link
            href="/explore/?verified=1"
            className="touch-link text-ink-2 underline decoration-ink-3 underline-offset-4 hover:text-accent-text hover:decoration-accent-text"
          >
            See all {formatCount(vendorCount)} publisher-manifest entries →
          </Link>
        </p>
      </div>
    </section>
  );
}
