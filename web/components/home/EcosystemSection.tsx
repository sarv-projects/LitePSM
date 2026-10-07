"use client";

import React from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { formatCount } from "../../lib/format";

/**
 * Explore the ecosystem: three kind cards and the agents entry point.
 *
 * The J003 pattern shows up here first — a plain-language sentence ("Connect
 * your AI to apps and data") beside the exact technical label the catalog
 * uses (`MCP Server`), so a reader learns the vocabulary without the page
 * pretending the vocabulary is friendlier than it is.
 *
 * Counts come from the build-time slice in `app/page.tsx` and are replaced by
 * the same derivation over the live rows once those are loaded; both run
 * `deriveKindCounts`, so no number can move between the two.
 */
export interface EcosystemSectionProps {
  kindCounts: { all: number; mcp: number; skill: number; plugin: number };
  hostCount: number;
  /** Adapter count for the agents card — the compiled-in registry, not a claim. */
  adapterCount: number;
}

const KIND_CARDS = [
  {
    kind: "mcp",
    badge: "MCP Server",
    title: "MCP servers",
    blurb: "Connect your AI to apps and data.",
    href: "/explore/?kind=mcp",
    cta: "Browse MCP servers",
    countKey: "mcp" as const,
  },
  {
    kind: "skill",
    badge: "Agent Skill",
    title: "Agent skills",
    blurb: "Step-by-step workflows your agent loads when it needs them.",
    href: "/explore/?kind=skill",
    cta: "Browse agent skills",
    countKey: "skill" as const,
  },
  {
    kind: "plugin",
    badge: "Plugin",
    title: "Plugins",
    blurb: "Bundled toolkits that ship skills, servers and commands together.",
    href: "/explore/?kind=plugin",
    cta: "Browse plugins",
    countKey: "plugin" as const,
  },
];

export function EcosystemSection({ kindCounts, hostCount, adapterCount }: EcosystemSectionProps) {
  return (
    <section className="shell band" aria-labelledby="ecosystem-title">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="kicker">Three kinds, one install command</p>
          <h2 id="ecosystem-title" className="band-title mt-2">
            Explore the ecosystem
          </h2>
          <p className="band-sub">
            Everything in the catalog is one of three kinds. The kind tells you how it installs and
            what it can reach — not how good it is, which no catalog can measure.
          </p>
        </div>
      </div>

      <div className="mt-7 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {KIND_CARDS.map((card) => (
          <Link
            key={card.kind}
            href={card.href}
            className="group flex flex-col rounded-card border border-rule bg-surface p-5 transition-[transform,border-color,box-shadow] duration-hover ease-out hover:-translate-y-0.5 hover:border-ink-3 hover:shadow-card"
          >
            <div className="flex items-center justify-between gap-3">
              <span
                className={`meta-kind meta-kind-${card.kind}`}
                style={{ height: 22, fontSize: 11 }}
              >
                {card.badge}
              </span>
              <span
                aria-hidden="true"
                className="h-2.5 w-2.5 rounded-full"
                style={{ backgroundColor: `var(--${card.kind})` }}
              />
            </div>

            <h3 className="mt-4 text-[19px] font-semibold tracking-tight text-ink">{card.title}</h3>
            <p className="mt-1.5 text-[13.5px] leading-relaxed text-ink-2">{card.blurb}</p>

            <p className="mt-5 flex items-baseline gap-2">
              <span className="t-mono t-tabular text-[28px] font-medium leading-none text-ink">
                {formatCount(kindCounts[card.countKey])}
              </span>
              <span className="t-mono text-[11px] uppercase tracking-wider text-ink-3">
                entries
              </span>
            </p>

            <span className="mt-4 flex items-center gap-1.5 text-[12.5px] font-medium text-ink-3 transition-colors duration-state ease-out group-hover:text-accent-text">
              {card.cta}
              <ArrowRight
                className="h-3.5 w-3.5 transition-transform duration-hover ease-out group-hover:translate-x-0.5"
                aria-hidden="true"
              />
            </span>
          </Link>
        ))}

        <Link
          href="/agents/"
          className="group flex flex-col rounded-card border border-dashed border-rule-2 bg-sunken p-5 transition-[transform,border-color,box-shadow] duration-hover ease-out hover:-translate-y-0.5 hover:border-ink-3 hover:shadow-card"
        >
          <span className="kicker">The other half of &ldquo;what works with what&rdquo;</span>
          <h3 className="mt-4 text-[19px] font-semibold tracking-tight text-ink">Agents</h3>
          <p className="mt-1.5 text-[13.5px] leading-relaxed text-ink-2">
            Which agent hosts LiteSPM can configure itself, which are only declared by publishers,
            and the config file each one reads.
          </p>
          <p className="mt-5 flex items-baseline gap-2">
            <span className="t-mono t-tabular text-[28px] font-medium leading-none text-ink">
              {formatCount(adapterCount)}
            </span>
            <span className="t-mono text-[11px] uppercase tracking-wider text-ink-3">
              compiled-in config adapters
            </span>
          </p>
          <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
            The catalog names {formatCount(hostCount)} hosts in total; the rest are what publishers
            declared, and LiteSPM does not edit their config.
          </p>
          <span className="mt-4 flex items-center gap-1.5 text-[12.5px] font-medium text-ink-3 transition-colors duration-state ease-out group-hover:text-accent-text">
            Open the agent atlas
            <ArrowRight
              className="h-3.5 w-3.5 transition-transform duration-hover ease-out group-hover:translate-x-0.5"
              aria-hidden="true"
            />
          </span>
        </Link>
      </div>
    </section>
  );
}
