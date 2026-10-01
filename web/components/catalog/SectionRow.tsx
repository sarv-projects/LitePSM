"use client";

import React from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { ExtensionCard } from "./ExtensionCard";

interface SectionRowProps {
  title: string;
  subtitle?: string;
  items: Listing[];
  onSelectItem: (item: Listing) => void;
  viewAllHref?: string;
  viewAllLabel?: string;
  onViewAll?: () => void;
}

/** A single mcpmarket-style discovery row with a header and "View all" action. */
export function SectionRow({
  title,
  subtitle,
  items,
  onSelectItem,
  viewAllHref,
  viewAllLabel = "View all",
  onViewAll,
}: SectionRowProps) {
  if (!items.length) return null;

  const viewAllClass =
    "inline-flex items-center gap-1 text-xs font-semibold text-emerald-700 transition-colors hover:text-emerald-600";

  return (
    <section className="mx-auto w-full max-w-7xl px-4 pb-10 lg:px-8">
      <div className="mb-4 flex items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-bold tracking-tight text-slate-900">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-slate-500">{subtitle}</p>}
        </div>
        {viewAllHref ? (
          <Link href={viewAllHref} className={viewAllClass}>
            {viewAllLabel}
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </Link>
        ) : onViewAll ? (
          <button type="button" onClick={onViewAll} className={viewAllClass}>
            {viewAllLabel}
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        ) : null}
      </div>

      <div className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3">
        {items.map((item) => (
          <ExtensionCard key={item.id} item={item} onSelect={onSelectItem} />
        ))}
      </div>
    </section>
  );
}
