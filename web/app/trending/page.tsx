"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { Listing } from "../../lib/telemetry";
import { KIND_ORDER, kindLabel, listingHref, publisherFacets, sortListings, topStarred } from "../../lib/catalog";
import { formatCount } from "../../lib/format";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

/** Ranked list. The rank is tabular so columns align; rows are whole links. */
function Board({
  title,
  note,
  rows,
  hostCount,
  showSummary = false,
}: {
  title: string;
  note?: string;
  rows: Listing[];
  hostCount: number;
  showSummary?: boolean;
}) {
  if (!rows.length) return null;

  return (
    <section>
      <div className="section-head">
        <h2>{title}</h2>
        {note && <p className="section-note mt-0.5">{note}</p>}
      </div>

      <ol>
        {rows.map((item, i) => (
          <li key={item.id}>
            <Link
              href={listingHref(item)}
              className="grid grid-cols-[26px_minmax(0,1fr)_auto] items-baseline gap-3 border-b border-rule py-2.5 transition-colors hover:bg-hover"
            >
              <span className="t-mono t-tabular text-[11px] text-ink-3">{String(i + 1).padStart(2, "0")}</span>

              <span className="min-w-0">
                <span className="flex items-center gap-1.5">
                  <span
                    aria-hidden="true"
                    className="h-2.5 w-[3px] shrink-0 rounded-[1px]"
                    style={{ backgroundColor: `var(--${item.kind})` }}
                  />
                  <span className="t-cond truncate text-[14px] font-medium text-ink">{item.name}</span>
                </span>
                <span className="row-meta">
                  <span>{item.publisher?.name || "not published"}</span>
                  {item.transport && <span className="t-mono">{item.transport}</span>}
                  <span className="truncate">{item.category}</span>
                  {showSummary && <span className="hidden truncate sm:inline">{item.summary}</span>}
                </span>
              </span>

              <span className="shrink-0 text-right">
                {item.stars > 0 ? (
                  <span
                    className="t-mono t-tabular block text-[12px] text-pop"
                    title={`${item.stars.toLocaleString("en-US")} publisher-repo stars. Illustrative popularity, not installs.`}
                  >
                    {formatCount(item.stars)}
                  </span>
                ) : (
                  <span className="absent">not published</span>
                )}
                <span className="t-mono t-tabular mt-0.5 block text-[10px] text-ink-3">
                  {(item.testedHosts || []).length} of {hostCount}
                </span>
              </span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}

/**
 * Publishers by entry count. This is a different axis from the star boards:
 * it answers "who ships the most capabilities" rather than "what is popular".
 */
function PublisherBoard({ rows }: { rows: Array<{ name: string; count: number }> }) {
  if (!rows.length) return null;
  const max = rows[0].count;

  return (
    <section>
      <div className="section-head">
        <h2>Publishers by entries shipped</h2>
        <p className="section-note mt-0.5">Count, not stars. 3,692 publishers appear in this snapshot.</p>
      </div>

      <ol>
        {rows.map((pub, i) => (
          <li key={pub.name}>
            <Link
              href={`/explore/?q=${encodeURIComponent(pub.name)}`}
              className="grid grid-cols-[26px_minmax(0,1fr)_auto] items-center gap-3 border-b border-rule py-2.5 transition-colors hover:bg-hover"
            >
              <span className="t-mono t-tabular text-[11px] text-ink-3">{String(i + 1).padStart(2, "0")}</span>
              <span className="min-w-0">
                <span className="t-cond block truncate text-[14px] font-medium text-ink">{pub.name}</span>
                <span className="mt-1 block h-[3px] w-full bg-sunken-2">
                  <span
                    className="block h-full bg-ink-3"
                    style={{ width: `${Math.max(2, (pub.count / max) * 100)}%` }}
                  />
                </span>
              </span>
              <span className="t-mono t-tabular shrink-0 text-[12px] text-ink-2">
                {formatCount(pub.count)}
              </span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}

export default function TrendingPage() {
  const hostCount = useMemo(() => hostUniverseCount(), []);

  const boards = useMemo(
    () => ({
      overall: topStarred(items, 25),
      publishers: publisherFacets(items, 15),
      byKind: KIND_ORDER.map((k) => ({
        kind: k,
        rows: topStarred(
          items.filter((i) => i.kind === k),
          15
        ),
      })),
      tail: sortListings(items, "newest").slice(0, 15),
    }),
    []
  );

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="shell flex-1 pt-8">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="t-cond text-[24px] font-semibold tracking-tight text-ink">Leaderboards</h1>
          <p className="t-mono text-[11px] text-ink-3">
            {formatCount(items.length)} entries · no usage telemetry
          </p>
        </div>

        <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-2">
          Every board below is ordered by publisher-repo stars, an upstream popularity signal. LitePSM
          collects no install or download data, so nothing here measures usage. Entries whose source
          publishes no star count read{" "}
          <span className="absent">not published</span> and are never counted as zero.
        </p>

        <div className="mt-7 grid gap-x-10 gap-y-9 lg:grid-cols-2">
          <Board
            title="Most popular, all kinds"
            note="Stars only. Popularity is not endorsement."
            rows={boards.overall}
            hostCount={hostCount}
          />
          <PublisherBoard rows={boards.publishers} />
          {boards.byKind.map((b) => (
            <Board
              key={b.kind}
              title={`Top ${kindLabel(b.kind)}s`}
              rows={b.rows}
              hostCount={hostCount}
              showSummary
            />
          ))}
          <Board
            title="End of the release manifest"
            note="Last entries in this snapshot. The dataset has no publish timestamps, so this is not a recency ranking."
            rows={boards.tail}
            hostCount={hostCount}
          />
        </div>
      </main>

      <SiteFooter />
    </div>
  );
}

function hostUniverseCount(): number {
  const hosts = new Set<string>();
  for (const item of items) {
    for (const host of item.testedHosts || []) hosts.add(host.trim());
  }
  return hosts.size;
}
