"use client";

import React from "react";
import Link from "next/link";
import { SquareTerminal } from "lucide-react";
import { HOSTS } from "../../lib/hosts";

/**
 * Not a logo wall. This is the host-adapter table: which config file each
 * agent uses, which format, and the exact bridge command LitePSM writes. That
 * is the information a reader needs before choosing an agent.
 */
export function ClientGrid() {
  return (
    <section className="shell pb-10">
      <div className="section-head">
        <div>
          <h2>Host adapters</h2>
          <p className="section-note mt-0.5">
            One <code className="t-mono">litepsm</code> bridge entry per host. Capabilities resolve centrally.
          </p>
        </div>
        <Link href="/agents/" className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline">
          agent compatibility
        </Link>
      </div>

      <ul>
        {HOSTS.map((host) => (
          <li key={host.id} className="row">
            <span className="row-spine bg-rule" aria-hidden="true" />
            <span
              aria-hidden="true"
              className="flex h-[22px] w-[22px] items-center justify-center rounded-chip bg-sunken-2"
            >
              <SquareTerminal className="h-3 w-3 text-ink-2" />
            </span>

            <span className="min-w-0">
              <span className="row-name">{host.name}</span>
              <span className="row-meta">
                <span className="meta-spec uppercase">{host.kind}</span>
                {host.nested && <span className="meta-spec">nested v2 layout</span>}
                <span className="truncate text-ink-3">{host.userPath}</span>
              </span>
            </span>

            {/* The literal command is the thing a reader would otherwise have
                to go look up, so it belongs on the row. */}
            <span className="row-side">
              <code className="t-mono hidden text-[11px] text-ink-3 xl:inline">
                litepsm bridge stdio --host {host.id}
              </code>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
