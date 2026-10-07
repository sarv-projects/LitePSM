"use client";

import React from "react";
import { AGENT_CHOICES } from "../../lib/landing";
import type { AgentChoice } from "../../lib/landing";

/**
 * "What do you use?" — a preference, not a filter engine.
 *
 * The choice is written to `localStorage["litespm-agent"]` and read back on
 * mount. Selecting an agent only changes what the cards already on the page
 * SAY: it derives a "Works with …" badge from rows the build handed over. It
 * never calls `useCatalog().request()` — a preference must not be able to pull
 * 3.8 MB of rows down, and the E1 contract puts every dataset request behind an
 * explicit interaction with the data itself.
 *
 * Counts appear only over rows that are actually loaded. Until then the copy is
 * qualitative, because a count over the twelve rows in "Good places to start"
 * would read as a catalog-wide claim it is not.
 */
export interface AgentStripProps {
  choice: AgentChoice | null;
  onChange: (id: string) => void;
  /** Catalog-wide counts per choice, or null while the rows are unloaded. */
  counts: Array<{ id: string; count: number }> | null;
  total: number;
}

export function AgentStrip({ choice, onChange, counts, total }: AgentStripProps) {
  const countFor = counts?.find((c) => c.id === choice?.id)?.count ?? null;

  let status: React.ReactNode;
  if (!choice) {
    status = (
      <>
        Pick one and the capabilities on this page will say whether they work with it. Nothing is
        downloaded to answer the question — the badges come from rows already on the page.
      </>
    );
  } else if (choice.hosts.length === 0) {
    status = (
      <>
        Fair enough. Nothing is measured against <strong className="font-medium text-ink-2">&ldquo;{choice.label}&rdquo;</strong>,
        so no capability here will claim to work with it.
      </>
    );
  } else if (countFor !== null) {
    status = (
      <>
        <span className="t-mono t-tabular text-ink">{countFor.toLocaleString("en-US")}</span> of{" "}
        <span className="t-mono t-tabular text-ink">{total.toLocaleString("en-US")}</span> entries
        list <strong className="font-medium text-ink-2">{choice.label}</strong> among their compatible
        hosts.
      </>
    );
  } else {
    status = (
      <>
        Cards below are marked against{" "}
        <strong className="font-medium text-ink-2">{choice.label}</strong> using the rows already on
        this page. Open a capability to see its full host list.
      </>
    );
  }

  return (
    <section className="shell band" aria-labelledby="agent-strip-title">
      <div className="rounded-card border border-rule bg-surface p-5 sm:p-6">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h2 id="agent-strip-title" className="text-[17px] font-semibold tracking-tight text-ink">
            What do you use?
          </h2>
          <p className="text-[12px] text-ink-3">Saved in this browser only</p>
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          {AGENT_CHOICES.map((option) => (
            <button
              key={option.id}
              type="button"
              aria-pressed={choice?.id === option.id}
              onClick={() => onChange(option.id)}
              className="chip"
            >
              {option.label}
            </button>
          ))}
        </div>

        <p className="mt-4 max-w-prose text-[13px] leading-relaxed text-ink-2">{status}</p>

        <p className="mt-3 max-w-prose text-[12px] leading-relaxed text-ink-3">
          Compatibility here is either publisher-declared or derived from the capability&rsquo;s kind —
          every MCP server installs through the bridge into a host that has an adapter, every skill
          into a host with a skills directory, and a plugin lists its own hosts. It is a declaration,
          not a test result.
        </p>
      </div>
    </section>
  );
}
