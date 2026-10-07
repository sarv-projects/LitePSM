"use client";

import React from "react";
import Link from "next/link";
import { ArrowRight, Check, Terminal } from "lucide-react";
import { HOSTS, SKILL_TARGETS } from "../../lib/hosts";
import type { HostAdapter } from "../../lib/hosts";

/**
 * Agent Atlas teaser: five cards built strictly from the two generated
 * registries (`data/hosts.json`, `data/skill-targets.json`), which are emitted
 * from the same Go tables the binary writes with. Every chip is a fact one of
 * those tables states — config format, a compiled-in adapter, a skills
 * directory, the universal-skills flag — and nothing is inferred: no OS
 * support, no "best for", no compatibility the data does not carry.
 *
 * No `/compare` link: that route does not exist yet (`LPSM-J007`).
 */
const TEASER_IDS = ["claude-code", "codex", "opencode", "cursor", "cline"];

function skillTarget(id: string) {
  return SKILL_TARGETS.find((t) => t.id === id);
}

function hostAdapter(id: string): HostAdapter | undefined {
  return HOSTS.find((h) => h.id === id);
}

export function AtlasTeaser() {
  return (
    <section className="shell band" aria-labelledby="atlas-title">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="kicker">Agent atlas</p>
          <h2 id="atlas-title" className="band-title mt-2">
            Works with the agent you already use
          </h2>
          <p className="band-sub">
            LiteSPM writes one bridge entry into a host&rsquo;s own config file. Which hosts it can
            write, and where, is a fact in the adapter registry — here are five of them.
          </p>
        </div>
        <Link href="/agents/" className="btn">
          Explore all agents
          <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
        </Link>
      </div>

      <ul className="mt-7 grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        {TEASER_IDS.map((id) => {
          const host = hostAdapter(id);
          const skill = skillTarget(id);
          if (!host && !skill) return null;
          const label = skill?.displayName ?? host?.name ?? id;

          return (
            <li
              key={id}
              className="flex flex-col rounded-card border border-rule bg-surface p-4 transition-[transform,border-color,box-shadow] duration-hover ease-out hover:-translate-y-0.5 hover:border-ink-3 hover:shadow-card"
            >
              <div className="flex items-center gap-2">
                <span className="flex h-7 w-7 items-center justify-center rounded-chip bg-sunken-2">
                  <Terminal className="h-3.5 w-3.5 text-ink-2" aria-hidden="true" />
                </span>
                <h3 className="text-[15px] font-semibold tracking-tight text-ink">{label}</h3>
              </div>

              <div className="mt-3 flex flex-wrap gap-1.5">
                {host && (
                  <span className="inline-flex items-center gap-1 rounded-chip border border-accent/30 bg-accent-wash px-1.5 py-0.5 text-[10.5px] font-medium text-accent-text">
                    <Check className="h-3 w-3" aria-hidden="true" />
                    MCP adapter
                  </span>
                )}
                {host && (
                  <span className="t-mono rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] uppercase text-ink-2">
                    {host.kind}
                  </span>
                )}
                {skill && (
                  <span className="t-mono rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] text-ink-2">
                    skills directory
                  </span>
                )}
                {skill?.universal && (
                  <span className="t-mono rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] text-ink-2">
                    universal skills
                  </span>
                )}
                {host?.nested && (
                  <span className="t-mono rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] text-ink-2">
                    nested key path
                  </span>
                )}
                {host?.projectOnly && (
                  <span className="t-mono rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] text-ink-2">
                    project scope
                  </span>
                )}
              </div>

              <p className="mt-auto pt-3">
                <code
                  className="block truncate text-[11px] text-ink-3"
                  title={host?.userPath || undefined}
                >
                  {host?.userPath || "path not published"}
                </code>
              </p>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
