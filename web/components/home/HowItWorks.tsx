"use client";

import React from "react";

/**
 * Find → Check → Add → Manage: four steps, one plain sentence each, with the
 * technical explanation behind a disclosure rather than underneath it.
 *
 * The expanders are `<details>` — no route, no modal, no client state — and
 * every command they name exists in `cmd/litespm/main.go` today: `search`,
 * `doctor`, `install`, `install remove`, `restore`, `grant`, `approve`,
 * `host setup`. Nothing here advertises a command that is still a backlog row.
 */
interface Step {
  n: string;
  title: string;
  plain: string;
  technical: React.ReactNode;
}

const STEPS: Step[] = [
  {
    n: "01",
    title: "Find",
    plain: "Search the catalog for what you need — by name, or by what you want done.",
    technical: (
      <>
        <p>
          <code className="t-mono">litespm search &lt;term&gt;</code> queries the locally synced
          release index. The sync walks pointer → manifest → listings and verifies a{" "}
          <strong className="font-medium text-ink">sha256</strong> digest at every step before a row
          is trusted; a row that fails verification is not searchable.
        </p>
        <p className="mt-2">
          The <code className="t-mono">/explore</code> page runs the equivalent match in your browser
          over the snapshot the page shipped with, using a MiniSearch index inside a Web Worker — no
          server is involved either way.
        </p>
      </>
    ),
  },
  {
    n: "02",
    title: "Check",
    plain: "See what it is, who published it, and what it would be able to reach.",
    technical: (
      <>
        <p>
          Each entry carries <code className="t-mono">publisher.provenance</code> — either{" "}
          <code className="t-mono">vendor-manifest</code> (read out of the publisher&rsquo;s own
          repository manifest) or <code className="t-mono">awesome-list-claim</code> — plus{" "}
          <code className="t-mono">installability</code>, the declared hosts, and the launch command
          when one was proven.
        </p>
        <p className="mt-2">
          Locally, <code className="t-mono">litespm doctor</code> reports the environment the install
          would land in: daemon, bridge, host adapters, and the state of the encrypted vault.
        </p>
      </>
    ),
  },
  {
    n: "03",
    title: "Add",
    plain: "Install it into the agent you actually use, with one command.",
    technical: (
      <>
        <p>
          <code className="t-mono">litespm install &lt;id&gt;</code> builds a plan, waits for an
          approval, then writes through the ledger. Skills land in the host&rsquo;s own skills
          directory; MCP servers are registered as one named entry in that host&rsquo;s config file,
          spliced with a pre-edit backup.
        </p>
        <p className="mt-2">
          Plugin artifacts are not published in the catalog yet, so a plugin install fails closed with{" "}
          <code className="t-mono">LPSM-ARTIFACT-UNAVAILABLE</code> instead of half-installing.
        </p>
      </>
    ),
  },
  {
    n: "04",
    title: "Manage",
    plain: "Update, take things away, and put your original config back whenever you want.",
    technical: (
      <>
        <p>
          <code className="t-mono">litespm install remove</code> takes an entry LiteSPM wrote back out,{" "}
          <code className="t-mono">litespm restore</code> rolls a config file back to its pre-edit
          backup, and <code className="t-mono">litespm uninstall</code> removes the bridge entry from a
          host.
        </p>
        <p className="mt-2">
          <code className="t-mono">litespm grant</code> and{" "}
          <code className="t-mono">litespm approve</code> decide what a capability is allowed to do;
          every decision is recorded against the plan that asked for it.
        </p>
      </>
    ),
  },
];

export function HowItWorks() {
  return (
    <section className="shell band" aria-labelledby="how-title">
      <div className="border-t border-rule pt-8">
        <p className="kicker">How LiteSPM works</p>
        <h2 id="how-title" className="band-title mt-2">
          Four steps, in that order
        </h2>
      </div>

      <ol className="mt-7 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {STEPS.map((step) => (
          <li
            key={step.n}
            className="flex flex-col rounded-card border border-rule bg-surface p-5"
          >
            <span className="t-mono text-[12px] font-medium tracking-wider text-accent-text">
              {step.n}
            </span>
            <h3 className="mt-2 text-[18px] font-semibold tracking-tight text-ink">{step.title}</h3>
            <p className="mt-1.5 flex-1 text-[13.5px] leading-relaxed text-ink-2">{step.plain}</p>

            <details className="group mt-4 border-t border-rule pt-2">
              <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[12.5px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[36px]">
                How this works technically
                <span aria-hidden="true" className="text-ink-3 transition-transform duration-panel ease-out group-open:rotate-90">
                  →
                </span>
              </summary>
              <div className="pb-1 text-[12.5px] leading-relaxed text-ink-3">{step.technical}</div>
            </details>
          </li>
        ))}
      </ol>
    </section>
  );
}
