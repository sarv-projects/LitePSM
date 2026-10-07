"use client";

import Link from "next/link";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { kindLabel } from "../../lib/catalog";
import { formatCount } from "../../lib/format";

/** One board's worth of precomputed rows — see `app/trending/page.tsx`. */
export interface TrendingBoards {
  categories: Array<{ label: string; count: number }>;
  hosts: Array<{ label: string; count: number }>;
  publishers: Array<{ label: string; count: number }>;
  kinds: Array<{ label: string; count: number; kind: "mcp" | "skill" | "plugin" }>;
}

/**
 * Coverage boards are counts, not rows: `app/trending/page.tsx` computes them
 * from the full row set at build time, so this route hydrates from props and
 * never requests the dataset.
 */
export interface TrendingViewProps {
  total: number;
  boards: TrendingBoards;
}

/**
 * A horizontal proportion bar. Used for every board on this page so the reader
 * compares one thing: the shape of the distribution.
 */
function Bar({ value, max, tone }: { value: number; max: number; tone?: string }) {
  return (
    <span className="mt-1 block h-[3px] w-full bg-sunken-2">
      <span
        className="block h-full"
        style={{
          width: `${Math.max(2, (value / Math.max(1, max)) * 100)}%`,
          backgroundColor: tone ?? "var(--ink-3)",
        }}
      />
    </span>
  );
}

/**
 * A counted board. Every number here is a count this build actually computed
 * from the catalog: entries per publisher, per category, per host, per kind.
 * Nothing is ranked by popularity, because no popularity signal exists.
 */
function CountBoard({
  title,
  note,
  rows,
  href,
  tone,
}: {
  title: string;
  note?: string;
  rows: Array<{ label: string; count: number; href?: string }>;
  href?: (label: string) => string;
  tone?: string;
}) {
  if (!rows.length) return null;
  const max = rows[0].count;

  return (
    <section>
      <div className="section-head">
        <div className="min-w-0">
          <h2>{title}</h2>
          {note && <p className="section-note mt-0.5">{note}</p>}
        </div>
      </div>

      <ol>
        {rows.map((row, i) => {
          const to = row.href ?? href?.(row.label);
          const body = (
            <>
              <span className="t-mono t-tabular text-[11px] text-ink-3">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="min-w-0">
                <span className="t-cond block truncate text-[14px] font-medium text-ink">
                  {row.label}
                </span>
                <Bar value={row.count} max={max} tone={tone} />
              </span>
              <span className="t-mono t-tabular shrink-0 text-[12px] text-ink-2">
                {formatCount(row.count)}
              </span>
            </>
          );
          const cls =
            "grid grid-cols-[26px_minmax(0,1fr)_auto] items-center gap-3 border-b border-rule py-2.5 transition-colors";
          return (
            <li key={row.label}>
              {to ? (
                <Link href={to} className={`${cls} hover:bg-hover`}>
                  {body}
                </Link>
              ) : (
                <div className={cls}>{body}</div>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

export default function CoveragePage({ total, boards }: TrendingViewProps) {
  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="shell flex-1 pt-8">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="text-[24px] font-semibold tracking-tight text-ink">Coverage</h1>
          <p className="t-mono text-[11px] text-ink-3">
            {formatCount(total)} entries · counted, not estimated
          </p>
        </div>

        <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-2">
          These boards describe the shape of the snapshot: how many entries each source, publisher,
          category and agent host accounts for. Nothing here is ranked by popularity. LiteSPM
          publishes no star, download or install figures, because the upstream sources do not expose
          them — an earlier build of this site estimated them, and that was wrong.
        </p>

        <div className="mt-7 grid gap-x-10 gap-y-9 lg:grid-cols-2">
          <CountBoard
            title="By capability kind"
            note="Three kinds are in the v1 taxonomy."
            rows={boards.kinds}
          />
          <CountBoard
            title="Agent hosts by declared coverage"
            note="How many entries list each host as compatible. Publisher-declared, not a test result."
            rows={boards.hosts}
          />
          <CountBoard
            title="Categories by entry count"
            note="Normalised from the category string each source publishes."
            rows={boards.categories}
            href={(label) => `/explore/?category=${encodeURIComponent(label)}`}
          />
          <CountBoard
            title="Publishers by entries shipped"
            note="Count of entries, not stars."
            rows={boards.publishers}
            href={(label) => `/explore/?q=${encodeURIComponent(label)}`}
          />
        </div>

        <p className="mt-9 max-w-prose border-t border-rule pt-5 text-[12px] leading-relaxed text-ink-3">
          Sources for this snapshot are listed in the repository under{" "}
          <span className="t-mono">internal/source</span>. Each capability links back to the upstream
          page it was read from.
        </p>
      </main>

      <SiteFooter />
    </div>
  );
}
