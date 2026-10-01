"use client";

import React from "react";
import Link from "next/link";
import { Listing } from "../../lib/telemetry";
import { kindLabel, listingHref } from "../../lib/catalog";
import { PublisherTile, StarCount, VerifiedMark } from "./PublisherMark";

export type ExtensionItem = Listing;

interface ExtensionCardProps {
  item: Listing;
  /** Denominator for "n of N agents", so coverage is never implied by a dot. */
  hostCount?: number;
}

const KIND_TAG: Record<Listing["kind"], string> = {
  mcp: "mcp",
  skill: "skill",
  plugin: "plugin",
};

/**
 * The primary listing unit: a ruled, 0-radius table row. Every row is a single
 * link to the package page, so the whole row is keyboard reachable.
 */
export function ExtensionCard({ item, hostCount }: ExtensionCardProps) {
  const hosts = item.testedHosts || [];
  const total = hostCount ?? hosts.length;
  const detail = [
    kindLabel(item.kind),
    item.publisher?.name ? `by ${item.publisher.name}` : "publisher not published",
    item.summary,
  ].join(". ");

  return (
    <Link
      href={listingHref(item)}
      data-kind={item.kind}
      aria-label={`${item.name}. ${detail}`}
      className="row"
    >
      <span className="row-spine" aria-hidden="true" />
      <PublisherTile name={item.publisher?.name} kind={item.kind} />

      <span className="min-w-0">
        <span className="row-name">{item.name}</span>
        <span className="row-meta">
          <span>{item.publisher?.name || "not published"}</span>
          {item.publisher?.verified && <VerifiedMark />}
          <span className="t-mono">{KIND_TAG[item.kind]}</span>
          {item.transport && <span className="t-mono">{item.transport}</span>}
          {item.runtime && <span className="t-mono">{item.runtime}</span>}
          <span className="truncate">{item.category}</span>
        </span>
      </span>

      <span className="row-side">
        <span className="flex w-[86px] justify-end">
          <StarCount stars={item.stars} />
        </span>
        <span
          className="t-mono t-tabular w-[62px] whitespace-nowrap text-[11px] text-ink-3"
          title={`Declares compatibility with ${hosts.length} of the ${total} agent hosts named in this catalog. Publisher-declared, not a test result.`}
        >
          {hosts.length}/{total} hosts
        </span>
      </span>
    </Link>
  );
}
