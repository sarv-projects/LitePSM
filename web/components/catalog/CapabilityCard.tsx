"use client";

import React from "react";
import Link from "next/link";
import { ArrowUpRight, Check, ChevronDown } from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { kindLabel, listingHref } from "../../lib/catalog";
import { hostsFor } from "../../lib/hosts";
import type { AgentChoice } from "../../lib/landing";
import { rowWorksWithAgent } from "../../lib/landing";
import { PublisherTile, VerifiedMark } from "./PublisherMark";

/**
 * `CapabilityCard` is the DISCOVERY density — home, ecosystem, collections.
 * `ExtensionCard` (the ruled `CatalogRow` density) stays what Explore uses.
 * The split is the point: a card can afford a plain-language summary, a
 * friendly name in Plex Sans and a hover lift; a result row cannot, because a
 * result row's job is comparison at 60px a line.
 *
 * Two layers, exactly as the review asked for:
 *   1. plain — name, what it does, its kind in words;
 *   2. technical — id, runtime, transport, version, source, host count —
 *      behind a `<details>` disclosure, because a hover-only reveal is not
 *      reachable by keyboard and this site's focus order is not decorative.
 *
 * The card is a link to the package page through a stretched anchor, so the
 * whole card is one target while the disclosure stays independently operable.
 */
interface CapabilityCardProps {
  item: Listing;
  /** Chosen in the "What do you use?" strip; renders the Works-with badge. */
  agent?: AgentChoice | null;
  /** Extra classes for grid placement. */
  className?: string;
}

const KIND_BADGE: Record<Listing["kind"], string> = {
  mcp: "meta-kind meta-kind-mcp",
  skill: "meta-kind meta-kind-skill",
  plugin: "meta-kind meta-kind-plugin",
};

/** Only fields the wire format actually publishes. Absent is stated, not guessed. */
function TechRow({ label, value }: { label: string; value?: string | null }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-rule py-1.5 last:border-b-0">
      <dt className="t-mono shrink-0 text-[10.5px] uppercase tracking-wider text-ink-3">{label}</dt>
      <dd className="t-mono min-w-0 truncate text-right text-[11.5px] text-ink-2">
        {value ? value : <span className="absent">not published</span>}
      </dd>
    </div>
  );
}

export function CapabilityCard({ item, agent, className = "" }: CapabilityCardProps) {
  const worksWith = rowWorksWithAgent(item, agent ?? null);
  const hosts = hostsFor(item);
  const detail = [
    kindLabel(item.kind),
    item.publisher?.name ? `by ${item.publisher.name}` : "publisher unspecified",
    item.summary,
  ].join(". ");

  return (
    <article
      className={`group relative flex flex-col rounded-card border border-rule bg-surface p-4 transition-[transform,border-color,box-shadow] duration-hover ease-out hover:-translate-y-0.5 hover:border-ink-3 hover:shadow-card focus-within:border-ink-3 ${className}`}
    >
      <div className="flex min-w-0 items-start gap-3">
        <span className="transition-transform duration-hover ease-out group-hover:scale-105">
          <PublisherTile name={item.publisher?.name} kind={item.kind} />
        </span>

        <div className="min-w-0 flex-1">
          <h3 className="pr-6 text-[15px] font-semibold leading-snug tracking-tight text-ink">
            <Link
              href={listingHref(item)}
              aria-label={`${item.name}. ${detail}`}
              className="after:absolute after:inset-0 after:content-[''] focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink"
            >
              {item.name}
            </Link>
            <ArrowUpRight
              className="ml-1 inline h-3.5 w-3.5 align-baseline text-ink-3 opacity-0 transition-transform duration-hover ease-out group-hover:translate-x-0.5 group-hover:opacity-100"
              aria-hidden="true"
            />
          </h3>

          {item.summary && (
            <p className="mt-1 line-clamp-3 text-[13px] leading-relaxed text-ink-2">{item.summary}</p>
          )}
        </div>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <span className={KIND_BADGE[item.kind]}>{kindLabel(item.kind)}</span>
        {item.category && <span className="text-[11.5px] text-ink-3">{item.category}</span>}
        {item.publisher?.verified && <VerifiedMark glyph />}
        {agent && agent.hosts.length > 0 && (
          <span
            className={`inline-flex items-center gap-1 rounded-chip border px-1.5 py-0.5 text-[10.5px] font-medium ${
              worksWith
                ? "border-accent/30 bg-accent-wash text-accent-text"
                : "border-rule bg-sunken text-ink-3"
            }`}
            title={
              worksWith
                ? "Derived from the capability's kind: MCP servers install through the bridge into any host with an adapter, skills into any host with a skills directory, plugins list their own hosts. A declaration, not a test result."
                : "This capability does not list that agent among its compatible hosts. Publisher-declared, not a test result."
            }
          >
            {worksWith && <Check className="h-3 w-3" aria-hidden="true" />}
            {worksWith ? `Works with ${agent.label}` : `Not declared for ${agent.label}`}
          </span>
        )}
      </div>

      <details className="group/tech relative z-10 mt-3 border-t border-rule pt-1">
        <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[12px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[36px]">
          Technical details
          <ChevronDown
            className="h-3.5 w-3.5 text-ink-3 transition-transform duration-panel ease-out group-open/tech:rotate-180"
            aria-hidden="true"
          />
        </summary>
        <dl className="mt-1 border-t border-rule pt-1">
          <TechRow label="id" value={item.id} />
          <TechRow label="runtime" value={item.runtime} />
          <TechRow label="transport" value={item.transport} />
          <TechRow label="version" value={item.version} />
          <TechRow label="source" value={item.publisher?.provenance} />
          <TechRow label="hosts" value={hosts.length ? `${hosts.length} declared` : null} />
        </dl>
      </details>
    </article>
  );
}
