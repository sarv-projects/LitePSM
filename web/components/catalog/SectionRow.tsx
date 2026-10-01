"use client";

import React from "react";
import Link from "next/link";
import { Listing } from "../../lib/telemetry";
import { ExtensionCard } from "./ExtensionCard";

interface SectionRowProps {
  title: string;
  note?: string;
  items: Listing[];
  hostCount?: number;
  viewAllHref?: string;
  viewAllLabel?: string;
  onViewAll?: () => void;
}

/**
 * A discovery section. The head is a rule with a sentence-case label left and a
 * counted link right, so the count is part of the signposting rather than a
 * separate badge.
 */
export function SectionRow({
  title,
  note,
  items,
  hostCount,
  viewAllHref,
  viewAllLabel,
  onViewAll,
}: SectionRowProps) {
  if (!items.length) return null;

  return (
    <section className="shell pb-9">
      <div className="section-head">
        <div className="min-w-0">
          <h2>{title}</h2>
          {note && <p className="section-note mt-0.5">{note}</p>}
        </div>

        {viewAllHref ? (
          <Link href={viewAllHref} className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline">
            {viewAllLabel || `see all ${items.length}`}
          </Link>
        ) : onViewAll && viewAllLabel ? (
          <button
            type="button"
            onClick={onViewAll}
            className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline"
          >
            {viewAllLabel}
          </button>
        ) : null}
      </div>

      <div className="border-t border-transparent">
        {items.map((item) => (
          <ExtensionCard key={item.id} item={item} hostCount={hostCount} />
        ))}
      </div>
    </section>
  );
}
