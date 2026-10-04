"use client";

import React from "react";
import Link from "next/link";
import { Listing } from "../../lib/telemetry";
import { kindLabel, listingHref } from "../../lib/catalog";
import { PublisherTile, VerifiedMark } from "./PublisherMark";
import { hostsFor } from "../../lib/hosts";

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
 *
 * The row carries the description visibly. It used to appear only in the
 * aria-label, which meant a sighted reader learned nothing about what a
 * capability does without opening it.
 *
 * There is no star column: the catalog publishes no popularity figures (the
 * upstream sources do not expose them), so a column that said "not published"
 * on every one of 5,814 rows was noise.
 */
export function ExtensionCard({ item, hostCount }: ExtensionCardProps) {
  const hosts = hostsFor(item);
  const total = hostCount ?? hosts.length;
  const detail = [
    kindLabel(item.kind),
    item.publisher?.name ? `by ${item.publisher.name}` : "publisher unspecified",
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

      <span className="min-w-0 py-0.5">
        <span className="row-name">{item.name}</span>
        {item.summary && <span className="row-summary">{item.summary}</span>}
        <span className="row-meta">
          <span className="meta-publisher">{item.publisher?.name || "unspecified"}</span>
          {item.publisher?.verified && <VerifiedMark glyph />}
          <span className={`meta-kind meta-kind-${item.kind}`}>{KIND_TAG[item.kind]}</span>
          {item.transport && <span className="meta-spec">{item.transport}</span>}
          {item.runtime && <span className="meta-spec">{item.runtime}</span>}
          <span className="meta-category truncate">{item.category}</span>
        </span>
      </span>

      <span className="row-side">
        <span
          className="t-mono t-tabular rounded-[3px] bg-sunken px-2 py-0.5 text-[11px] font-medium text-ink-2 border border-rule"
          title={`Declares compatibility with ${hosts.length} of the ${total} agent hosts named in this catalog. Publisher-declared, not a test result.`}
        >
          {hosts.length}/{total} hosts
        </span>
      </span>
    </Link>
  );
}
