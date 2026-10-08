"use client";

import React, { useRef } from "react";
import { ExternalLink } from "lucide-react";
import type { Listing } from "../../lib/telemetry";
import type { AgentChoice } from "../../lib/landing";
import { kindLabel, listingHref } from "../../lib/catalog";
import { hostsFor } from "../../lib/hosts";
import { formatCount, shortDigest } from "../../lib/format";
import { VerifiedMark } from "../catalog/PublisherMark";
import { SITE_URL } from "../../lib/site";
import bundledRelease from "../../data/release.json";

/**
 * The technical layer of the package page: six tabs that sit *after* the five
 * plain-language questions (ARCH/25 §7.3), so a reader who just wants the
 * answer never has to open one — and a reader who wants the record can.
 *
 * Every cell is a field the snapshot actually carries. Where a field is empty
 * the tab prints an explicit absent state (`—` / "not published"), never a
 * guess and never a "not compatible": the catalog's semantics are
 * publisher-declared (plugins) or kind-derived (MCP servers, skills), and the
 * labels below never claim more than that.
 */

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "capabilities", label: "Capabilities" },
  { id: "compatibility", label: "Compatibility" },
  { id: "security", label: "Security" },
  { id: "versions", label: "Versions" },
  { id: "source", label: "Source" },
] as const;

export type PackageTabId = (typeof TABS)[number]["id"];

/**
 * Provenance labels, exactly the set the honesty overlay allows, mapped from
 * the field that decides them (`installability`). Nothing here is upgraded for
 * looking established: a `discovery_only` row stays "Discovery only".
 */
const INSTALLABILITY: Record<
  NonNullable<Listing["installability"]>,
  { label: string; plain: string }
> = {
  discovery_only: {
    label: "Discovery only",
    plain:
      "LiteSPM found this entry in a source list. Whether it installs — or what it would run — has not been checked.",
  },
  metadata_verified: {
    label: "LiteSPM-parsed",
    plain:
      "LiteSPM read this entry's metadata out of the publisher's own manifest — hosts and install command included — but has not run it.",
  },
  runtime_verified: {
    label: "LiteSPM-verified",
    plain: "The declared runtime behavior was proven; the entry has not been exercised end to end.",
  },
  litespm_tested: {
    label: "LiteSPM-tested",
    plain: "Exercised end to end by LiteSPM.",
  },
};

/** Where this row's host list comes from — stated at its real strength. */
const HOST_SOURCE: Record<Listing["kind"], { label: string; plain: string }> = {
  mcp: {
    label: "kind-derived",
    plain:
      "Every host with a LiteSPM bridge adapter: an MCP server installs through the one bridge entry, so this list comes from LiteSPM's adapter registry — not from the publisher, and not from a test.",
  },
  skill: {
    label: "kind-derived",
    plain:
      "Every host with a skills directory LiteSPM can write a SKILL.md into — from LiteSPM's skill-target registry, not from the publisher, and not from a test.",
  },
  plugin: {
    label: "Publisher-declared",
    plain:
      "The publisher listed these hosts in its own manifest. A declaration, not a LiteSPM test result.",
  },
};

function Absent({ text = "not published" }: { text?: string }) {
  return <span className="absent">{text}</span>;
}

function SpecRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function externalLink(href: string, text: string) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="link inline-flex items-center gap-1 break-all">
      {text}
      <ExternalLink className="h-3 w-3 shrink-0 text-ink-3" aria-hidden="true" />
    </a>
  );
}

function sourceLabel(item: Listing): string {
  return item.publisher?.provenance === "vendor-manifest"
    ? "read from the publisher's own repository manifest"
    : "claimed by a third-party list, not read from the publisher's own manifest";
}

/* ------------------------------ panels ------------------------------ */

function OverviewPanel({ item }: { item: Listing }) {
  return (
    <div className="grid gap-6 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div>
        <p className="max-w-prose text-[13.5px] leading-relaxed text-ink-2">
          This is the catalog&rsquo;s record for {item.name}. Everything in these tabs comes from
          the snapshot this page was built from — fields that were never published read{" "}
          <span className="absent">not published</span>, and nothing is inferred to fill a gap.
        </p>
        <p className="mt-3 max-w-prose text-[13px] leading-relaxed text-ink-3">
          Available from a source {sourceLabel(item)}.
        </p>
      </div>

      <dl className="spec-list">
        <SpecRow label="Catalog id">
          <span className="t-mono text-[12px]!">{item.id}</span>
        </SpecRow>
        <SpecRow label="Kind">{kindLabel(item.kind)}</SpecRow>
        <SpecRow label="Category">
          <span className="t-mono text-[12px]!">{item.category}</span>
        </SpecRow>
        <SpecRow label="Publisher">
          {item.publisher?.url && /^https?:\/\//i.test(item.publisher.url) ? (
            externalLink(item.publisher.url, item.publisher.name)
          ) : (
            item.publisher?.name || <Absent text="unspecified" />
          )}
        </SpecRow>
        <SpecRow label="Publisher listing">
          {item.publisher?.verified ? <VerifiedMark glyph /> : <Absent text="third-party list entry" />}
        </SpecRow>
        <SpecRow label="Version">
          {item.version ? (
            <span className="t-mono text-[12px]!">{item.version}</span>
          ) : (
            <Absent text="not published" />
          )}
        </SpecRow>
        <SpecRow label="Popularity">
          <span
            className="absent"
            title="The upstream sources expose no machine-readable star, download or install counts, so this catalog publishes none rather than an estimate."
          >
            unspecified
          </span>
        </SpecRow>
      </dl>
    </div>
  );
}

function CapabilitiesPanel({ item }: { item: Listing }) {
  const launch = item.command
    ? `${item.command}${item.args?.length ? ` ${item.args.join(" ")}` : ""}`
    : null;

  return (
    <div className="grid gap-6 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div>
        <p className="max-w-prose text-[13.5px] leading-relaxed text-ink-2">
          {item.kind === "mcp" && (
            <>
              The tools this server exposes are discovered by your agent once it is connected — the
              catalog does not carry a per-entry tool list, so this tab does not invent one.
            </>
          )}
          {item.kind === "skill" && (
            <>
              A skill&rsquo;s capability lives in its SKILL.md: instructions your agent reads and
              follows. The catalog indexes the skill and its source repository, not a machine-readable
              tool list.
            </>
          )}
          {item.kind === "plugin" && (
            <>
              A plugin bundles its own components inside the host; what it adds is visible to your
              agent after the host installs it. The catalog carries the publisher&rsquo;s description
              and hosts, not an inner component list.
            </>
          )}
        </p>
      </div>

      <dl className="spec-list">
        <SpecRow label="Tools">
          {item.tools?.length ? (
            <span className="t-mono text-[12px]!">{item.tools.length} declared</span>
          ) : (
            <Absent />
          )}
        </SpecRow>
        <SpecRow label="Access effects">
          {item.effects?.length ? (
            <span className="t-mono text-[12px]!">{item.effects.length} declared</span>
          ) : (
            <Absent text="not yet published" />
          )}
        </SpecRow>
        <SpecRow label="Runtime">
          {item.runtime ? (
            <span className="t-mono text-[12px]!">{item.runtime}</span>
          ) : (
            <Absent text={item.kind === "mcp" ? "unspecified" : "not applicable"} />
          )}
        </SpecRow>
        <SpecRow label="Transport">
          {item.transport ? (
            <span className="t-mono text-[12px]!">{item.transport}</span>
          ) : (
            <Absent text={item.kind === "mcp" ? "unspecified" : "not applicable"} />
          )}
        </SpecRow>
        <SpecRow label="Launch line">
          {launch ? <span className="t-mono break-all text-[12px]!">{launch}</span> : <Absent text="unspecified" />}
        </SpecRow>
        <SpecRow label="Host command">
          {item.installHint ? (
            <span className="t-mono break-all text-[12px]!">{item.installHint}</span>
          ) : (
            <Absent text={item.kind === "plugin" ? "unspecified" : "not applicable"} />
          )}
        </SpecRow>
        <SpecRow label="Skill source">
          {item.skillSource ? (
            <span className="t-mono break-all text-[12px]!">{item.skillSource}</span>
          ) : (
            <Absent text={item.kind === "skill" ? "unspecified" : "not applicable"} />
          )}
        </SpecRow>
      </dl>
    </div>
  );
}

function CompatibilityPanel({
  item,
  agent,
  hostTotal,
}: {
  item: Listing;
  agent: AgentChoice | null;
  hostTotal: number;
}) {
  const hosts = hostsFor(item);
  const source = HOST_SOURCE[item.kind];
  const wanted = (agent?.hosts ?? []).map((n) => n.toLowerCase());

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip pointer-events-none">{source.label}</span>
        <span className="t-mono t-tabular text-[12px] text-ink-3">
          {hosts.length} of {hostTotal} hosts in this catalog
        </span>
      </div>

      <p className="mt-3 max-w-prose text-[13.5px] leading-relaxed text-ink-2">{source.plain}</p>

      {hosts.length ? (
        <ul className="mt-4 flex flex-wrap gap-1.5">
          {hosts.map((h) => {
            const mine = wanted.includes(h.toLowerCase());
            return (
              <li key={h}>
                <span
                  title={
                    mine
                      ? `Matches the agent you selected. ${source.plain}`
                      : source.plain
                  }
                  className={`inline-flex min-h-[26px] items-center rounded-chip border px-2 py-0.5 text-[12px] ${
                    mine
                      ? "border-accent/40 bg-accent-wash text-accent-text"
                      : "border-rule bg-sunken text-ink-2"
                  }`}
                >
                  {h}
                </span>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="mt-4 text-[13px] text-ink-3">
          This entry declares no agent compatibility — <span className="absent">—</span>, not a
          claim that it fails anywhere.
        </p>
      )}

      <p className="mt-4 max-w-prose text-[12px] leading-relaxed text-ink-3">
        Compatibility here is either declared by the publisher or derived from the entry&rsquo;s kind.
        This site does not execute capabilities, so none of it is a test result.
      </p>
    </div>
  );
}

function SecurityPanel({ item }: { item: Listing }) {
  const installability = item.installability ? INSTALLABILITY[item.installability] : null;

  return (
    <div className="grid gap-6 md:grid-cols-2">
      <div className="border-t border-rule pt-3">
        <h3 className="text-[14px] font-semibold tracking-tight text-ink">How far this got</h3>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <span className="chip pointer-events-none">{installability ? installability.label : "—"}</span>
          <code className="t-mono text-[11px] text-ink-3">
            installability = {item.installability ? `"${item.installability}"` : "not published"}
          </code>
        </div>
        <p className="mt-2 text-[13px] leading-relaxed text-ink-2">
          {installability ? installability.plain : "This entry does not publish a provenance level."}
        </p>
      </div>

      <div className="border-t border-rule pt-3">
        <h3 className="text-[14px] font-semibold tracking-tight text-ink">Who published it</h3>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {item.publisher?.verified ? <VerifiedMark glyph /> : <span className="chip pointer-events-none">third-party list entry</span>}
        </div>
        <p className="mt-2 text-[13px] leading-relaxed text-ink-2">
          &ldquo;vendor-listed&rdquo; means the row came from that publisher&rsquo;s own marketplace
          manifest. No registry verified the publisher, and LiteSPM has not audited it — it is a
          provenance flag, not a security review.
        </p>
      </div>

      <div className="border-t border-rule pt-3">
        <h3 className="text-[14px] font-semibold tracking-tight text-ink">What it can touch</h3>
        <p className="mt-2 text-[13px] leading-relaxed text-ink-2">
          A per-entry access declaration is designed for the catalog (
          <code className="t-mono text-ink">effects</code>) but not published yet, so this entry
          states no file, network or credential scope. Adding it writes one entry into the
          agent&rsquo;s config file <em>after</em> backing that file up —{" "}
          <code className="t-mono text-ink">litespm restore</code> puts the original back.
        </p>
      </div>

      <div className="border-t border-rule pt-3">
        <h3 className="text-[14px] font-semibold tracking-tight text-ink">Secrets stay out of configs</h3>
        <p className="mt-2 text-[13px] leading-relaxed text-ink-2">
          Credentials are forwarded by name with{" "}
          <code className="t-mono text-ink">litespm install --env NAME</code>: the host config records
          a <code className="t-mono text-ink">{"${NAME}"}</code> reference, never the value, so the
          secret stays in the environment your agent was started in.
        </p>
      </div>
    </div>
  );
}

function VersionsPanel({
  item,
  total,
  versioned,
}: {
  item: Listing;
  total: number;
  versioned: number;
}) {
  const release = bundledRelease as Record<string, unknown>;
  const createdAt = typeof release.createdAt === "string" ? release.createdAt : null;

  return (
    <div className="grid gap-6 md:grid-cols-2">
      <div>
        <div className="section-head">
          <h3 className="text-[13px] font-semibold tracking-tight text-ink">This entry</h3>
        </div>
        {item.version ? (
          <p className="mt-3 flex items-baseline gap-2 text-[13px] text-ink-2">
            <span className="t-mono text-[15px] text-ink">{item.version}</span>
            <span className="text-ink-3">published by the source this row was read from</span>
          </p>
        ) : (
          <>
            <p className="mt-3">
              <Absent text="not published" />
            </p>
            <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-2">
              The source this row was read from carries no version string for {item.name}, and this
              catalog does not guess one. Version strings exist on{" "}
              <span className="t-mono t-tabular">{formatCount(versioned)}</span> of{" "}
              <span className="t-mono t-tabular">{formatCount(total)}</span> entries in this snapshot.
            </p>
          </>
        )}
      </div>

      <div>
        <div className="section-head">
          <h3 className="text-[13px] font-semibold tracking-tight text-ink">This snapshot</h3>
        </div>
        <dl className="spec-list mt-3">
          <SpecRow label="Release">
            <span className="t-mono text-[12px]!">
              {typeof release.releaseId === "string" ? release.releaseId : <Absent text="unspecified" />}
            </span>
          </SpecRow>
          <SpecRow label="Sequence">
            {typeof release.sequence === "number" ? (
              <span className="t-mono t-tabular text-[12px]!">{release.sequence}</span>
            ) : (
              <Absent text="unspecified" />
            )}
          </SpecRow>
          <SpecRow label="Built">
            <span className="t-mono text-[12px]!">
              {createdAt ? createdAt.replace("T", " ").replace("Z", " UTC") : <Absent text="unspecified" />}
            </span>
          </SpecRow>
          <SpecRow label="Dataset digest">
            <span className="t-mono text-[12px]!">
              {shortDigest(typeof release.datasetDigest === "string" ? release.datasetDigest : undefined) ?? (
                <Absent text="unspecified" />
              )}
            </span>
          </SpecRow>
          <SpecRow label="Manifest digest">
            <span className="t-mono text-[12px]!">
              {shortDigest(typeof release.manifestDigest === "string" ? release.manifestDigest : undefined) ?? (
                <Absent text="unspecified" />
              )}
            </span>
          </SpecRow>
        </dl>
        <p className="mt-2.5 text-[12px] leading-relaxed text-ink-3">
          Rows are requested with <code className="t-mono text-ink-2">?v=&lt;dataset digest&gt;</code>,
          so a cached copy cannot disagree with the HTML beside it.
        </p>
      </div>
    </div>
  );
}

function SourcePanel({ item }: { item: Listing }) {
  const canonical = `${SITE_URL}${listingHref(item)}`;

  return (
    <div className="grid gap-6 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div>
        <p className="max-w-prose text-[13.5px] leading-relaxed text-ink-2">
          Where this record came from, and where to go upstream. The source class is stated at the
          strength the catalog actually carries it.
        </p>
        <p className="mt-3 max-w-prose text-[13px] leading-relaxed text-ink-3">
          Available from a source {sourceLabel(item)}.
        </p>
        {item.publisher?.verified && (
          <p className="mt-2 max-w-prose text-[12px] leading-relaxed text-ink-3">
            A verified listing flag means the row was parsed from that publisher&rsquo;s manifest —
            not that any registry or this project reviewed the code.
          </p>
        )}
      </div>

      <dl className="spec-list">
        <SpecRow label="Publisher URL">
          {item.publisher?.url && /^https?:\/\//i.test(item.publisher.url) ? (
            externalLink(item.publisher.url, item.publisher.url)
          ) : (
            <Absent text="unspecified" />
          )}
        </SpecRow>
        <SpecRow label="Skill source">
          {item.skillSource ? (
            externalLink(item.skillSource, item.skillSource)
          ) : (
            <Absent text={item.kind === "skill" ? "unspecified" : "not applicable"} />
          )}
        </SpecRow>
        <SpecRow label="Source class">
          <span className="t-mono text-[12px]!">
            {item.publisher?.provenance === "vendor-manifest" ? "vendor-manifest" : "awesome-list-claim"}
          </span>
        </SpecRow>
        <SpecRow label="Slug">
          <span className="t-mono text-[12px]!">{item.slug}</span>
        </SpecRow>
        <SpecRow label="This page">
          <span className="t-mono break-all text-[12px]!">{canonical}</span>
        </SpecRow>
      </dl>
    </div>
  );
}

/* ------------------------------ shell ------------------------------ */

export interface PackageTabsProps {
  item: Listing;
  /** The agent the reader picked; drives the highlighted compatibility chips. */
  agent: AgentChoice | null;
  /** `hostUniverse(rows).length` — the coverage denominator. */
  hostTotal: number;
  /** Version coverage over the loaded row set (rows are loaded on this route). */
  snapshot: { total: number; versioned: number };
  /** Controlled so a question block above can jump to a tab — local state only. */
  active: PackageTabId;
  onChange: (id: PackageTabId) => void;
}

/**
 * Six tabs, one record. ARIA tabs pattern with roving tabindex and
 * Left/Right/Home/End keys; the active tab carries the accent underline, the
 * inactive ones stay neutral. The state lives in the view so a link from the
 * questions above can open a specific tab — switching never fetches anything
 * (the row is already in memory).
 */
export function PackageTabs({ item, agent, hostTotal, snapshot, active, onChange }: PackageTabsProps) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);

  const renderPanel = (tab: PackageTabId): React.ReactNode => {
    switch (tab) {
      case "overview":
        return <OverviewPanel item={item} />;
      case "capabilities":
        return <CapabilitiesPanel item={item} />;
      case "compatibility":
        return <CompatibilityPanel item={item} agent={agent} hostTotal={hostTotal} />;
      case "security":
        return <SecurityPanel item={item} />;
      case "versions":
        return <VersionsPanel item={item} total={snapshot.total} versioned={snapshot.versioned} />;
      case "source":
        return <SourcePanel item={item} />;
    }
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const i = TABS.findIndex((t) => t.id === active);
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % TABS.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = TABS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onChange(TABS[next].id);
    refs.current[next]?.focus();
  };

  return (
    <section aria-labelledby="pkg-details-title" className="mt-9">
      <div className="section-head">
        <h2 id="pkg-details-title">Details</h2>
        <span className="section-note">the full record, one level deeper</span>
      </div>

      <div
        role="tablist"
        aria-label="Package details"
        onKeyDown={onKeyDown}
        className="no-scrollbar mt-1 flex gap-1 overflow-x-auto border-b border-rule"
      >
        {TABS.map((tab, i) => (
          <button
            key={tab.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={`pkg-tab-${tab.id}`}
            aria-controls={`pkg-panel-${tab.id}`}
            aria-selected={active === tab.id}
            tabIndex={active === tab.id ? 0 : -1}
            onClick={() => onChange(tab.id)}
            className={`relative min-h-[44px] shrink-0 px-3 text-[13px] font-medium transition-colors duration-state md:min-h-[38px] ${
              active === tab.id
                ? "text-ink after:absolute after:inset-x-0 after:-bottom-px after:h-0.5 after:bg-accent after:content-['']"
                : "text-ink-3 hover:text-ink"
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {TABS.map((tab) => (
        <div
          key={tab.id}
          role="tabpanel"
          id={`pkg-panel-${tab.id}`}
          aria-labelledby={`pkg-tab-${tab.id}`}
          tabIndex={0}
          hidden={active !== tab.id}
          className="mt-5"
        >
          {renderPanel(tab.id)}
        </div>
      ))}
    </section>
  );
}
