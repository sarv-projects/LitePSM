"use client";

import React, { useState } from "react";
import { HOSTS } from "../../lib/hosts";

/** Derived from the adapter table, so the copy cannot drift from the data. */
const HOST_NAMES = `${HOSTS.slice(0, -1).map((h) => h.name).join(", ")}, and ${HOSTS[HOSTS.length - 1].name}`;

const FAQS: Array<{ q: string; a: React.ReactNode }> = [
  {
    q: "What is LitePSM?",
    a: (
      <>
        A local package manager and federated catalog for agent capabilities. It connects to each agent host
        once, writes a single version-pinned bridge entry into that host's config, and resolves individual
        capabilities at runtime.
      </>
    ),
  },
  {
    q: "Which agents are supported?",
    a: (
      <>
        {HOST_NAMES} have compiled-in config adapters that LitePSM can write directly. The catalog names more
        hosts than that, because publishers declare compatibility themselves; those entries are indexed but
        LitePSM will not edit that host's config for you.
      </>
    ),
  },
  {
    q: "Do my API keys leave my machine?",
    a: (
      <>
        No. Credentials are held in your operating system's native vault. This site is a static export — it
        fetches exactly one file, <code className="t-mono">/v1/current.json</code>, for release metadata, and
        runs search entirely in your browser.
      </>
    ),
  },
  {
    q: "Is this page's data live?",
    a: (
      <>
        The listings are a fixed catalog snapshot baked into the build, so every listing is pre-rendered and
        reachable without JavaScript. Only the release manifest is fetched at runtime, and the header states
        plainly whether that fetch succeeded.
      </>
    ),
  },
];

export function FaqSection() {
  const [open, setOpen] = useState<string | null>(FAQS[0].q);

  return (
    <section className="shell pb-14">
      <div className="section-head">
        <h2>Questions this index has to answer honestly</h2>
      </div>

      <dl className="grid gap-x-10 md:grid-cols-2">
        {FAQS.map((item) => {
          const isOpen = open === item.q;
          return (
            <div key={item.q} className="border-b border-rule py-4">
              <dt>
                <button
                  type="button"
                  aria-expanded={isOpen}
                  onClick={() => setOpen(isOpen ? null : item.q)}
                  className="flex w-full items-start justify-between gap-4 text-left"
                >
                  <span className="text-[13px] font-semibold leading-snug text-ink">{item.q}</span>
                  <span
                    aria-hidden="true"
                    className="t-mono mt-px shrink-0 text-[11px] text-ink-3 tabular-nums"
                  >
                    {isOpen ? "−" : "+"}
                  </span>
                </button>
              </dt>
              {isOpen && <dd className="mt-2 text-[13px] leading-relaxed text-ink-2">{item.a}</dd>}
            </div>
          );
        })}
      </dl>
    </section>
  );
}
