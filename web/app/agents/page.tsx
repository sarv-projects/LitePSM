"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { SquareTerminal, Check } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { Listing } from "../../lib/telemetry";
import { agentFacets, hostUniverse } from "../../lib/catalog";
import { HOSTS, resolveHost } from "../../lib/hosts";
import { formatCount } from "../../lib/format";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

export default function AgentsPage() {
  const agents = useMemo(() => agentFacets(items), []);
  const hostCount = useMemo(() => hostUniverse(items).length, []);

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="shell flex-1 pt-8">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="t-cond text-[24px] font-semibold tracking-tight text-ink">Agent hosts</h1>
          <p className="t-mono text-[11px] text-ink-3">
            {formatCount(hostCount)} hosts named · {HOSTS.length} with compiled-in adapters
          </p>
        </div>

        <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-2">
          An entry&rsquo;s compatibility list is what its publisher declared. LiteSPM adds a bridge entry to
          the hosts it has compiled-in adapters for; the remaining hosts are indexed because publishers list
          them, not because LiteSPM can edit their configuration.
        </p>

        <div className="mt-7 border-b border-ink pb-2">
          <h2 className="sr-only">Agent hosts, compatible entry counts, and config adapters</h2>
          {/* Column order must match the rendered cell order, which the rows
              set with md:order-2 (adapter) and md:order-3 (compatible count). */}
          <div className="t-mono hidden grid-cols-[minmax(0,1fr)_92px_84px_minmax(0,1fr)] items-center gap-3 text-[10px] text-ink-3 md:grid">
            <span>Host</span>
            <span>Adapter</span>
            <span className="text-right">Compatible</span>
            <span>Config file this host uses</span>
          </div>
        </div>

        <ul>
          {agents.map((agent) => {
            const host = resolveHost(agent.name);
            return (
              <li key={agent.slug}>
                <Link
                  href={`/explore/?host=${encodeURIComponent(agent.name)}`}
                  className="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-rule py-3 transition-colors hover:bg-hover md:grid-cols-[minmax(0,1fr)_92px_84px_minmax(0,1fr)]"
                >
                  <span className="flex min-w-0 items-center gap-2.5">
                    <span
                      aria-hidden="true"
                      className="flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-chip bg-sunken-2"
                    >
                      <SquareTerminal className="h-3 w-3 text-ink-2" />
                    </span>
                    <span className="t-cond truncate text-[14px] font-medium text-ink">{agent.name}</span>
                  </span>

                  <span className="t-mono t-tabular text-right text-[12px] text-ink-2 md:order-3">
                    {formatCount(agent.count)}
                  </span>

                  <span className="hidden md:order-2 md:block">
                    {host ? (
                      <span
                        className="inline-flex items-center gap-1 text-[11px] text-ink"
                        title="LiteSPM has a compiled-in adapter that can write this host's config file"
                      >
                        <Check className="h-3 w-3" aria-hidden="true" />
                        managed
                      </span>
                    ) : (
                      <span className="text-[11px] text-ink-3" title="No compiled-in adapter; publishers declare compatibility themselves">
                        declared
                      </span>
                    )}
                  </span>

                  <code
                    className="t-mono hidden truncate text-[11px] text-ink-3 md:order-4 md:block"
                    title={host ? host.userPath : undefined}
                  >
                    {host ? host.userPath : "not managed by LiteSPM"}
                  </code>
                </Link>
              </li>
            );
          })}
        </ul>

        <p className="mt-6 max-w-prose text-[12px] leading-relaxed text-ink-3">
          Compatibility is publisher-declared. It is not a test result, and this site does not run these
          capabilities.
        </p>
      </main>

      <SiteFooter />
    </div>
  );
}
