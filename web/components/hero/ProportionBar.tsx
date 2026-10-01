"use client";

import React from "react";
import Link from "next/link";
import { Listing } from "../../lib/telemetry";
import { kindLabel } from "../../lib/catalog";
import { formatCount } from "../../lib/format";

interface ProportionBarProps {
  parts: Array<{ kind: Listing["kind"]; count: number; share: number }>;
}

/**
 * The one graphic moment on the site: a stacked bar of the real 70/19/11 split
 * of the catalog. Each segment is a link, so the graphic is also navigation.
 */
export function ProportionBar({ parts }: ProportionBarProps) {
  return (
    <div>
      <div
        className="flex h-2 w-full overflow-hidden rounded-chip bg-sunken-2"
        role="img"
        aria-label={parts
          .map((p) => `${formatCount(p.count)} ${kindLabel(p.kind).toLowerCase()}s, ${Math.round(p.share * 100)} percent`)
          .join("; ")}
      >
        {parts.map((p) => (
          <Link
            key={p.kind}
            href={`/explore/?kind=${p.kind}`}
            title={`${formatCount(p.count)} ${kindLabel(p.kind).toLowerCase()}s (${Math.round(p.share * 100)}%)`}
            className="h-full transition-[filter] duration-100 hover:brightness-110 focus-visible:brightness-110"
            style={{
              width: `${p.share * 100}%`,
              backgroundColor: `var(--${p.kind})`,
            }}
          >
            <span className="sr-only">Browse {formatCount(p.count)} {kindLabel(p.kind).toLowerCase()}s</span>
          </Link>
        ))}
      </div>

      <ul className="mt-2.5 flex flex-wrap items-center gap-x-5 gap-y-1.5">
        {parts.map((p) => (
          <li key={p.kind} className="flex items-center gap-1.5">
            <span
              aria-hidden="true"
              className="h-2.5 w-[3px] rounded-[1px]"
              style={{ backgroundColor: `var(--${p.kind})` }}
            />
            <Link href={`/explore/?kind=${p.kind}`} className="text-[12px] text-ink-2 hover:text-ink hover:underline">
              {kindLabel(p.kind)}s
            </Link>
            <span className="t-mono t-tabular text-[12px] font-medium text-ink">
              {formatCount(p.count)}
            </span>
            <span className="t-mono t-tabular text-[11px] text-ink-3">{Math.round(p.share * 100)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
