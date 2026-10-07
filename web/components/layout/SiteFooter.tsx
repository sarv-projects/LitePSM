"use client";

import React from "react";
import Link from "next/link";
import { HOSTS } from "../../lib/hosts";
import bundledRelease from "../../data/release.json";
import { CountUp } from "../ui/CountUp";

const REPO = "https://github.com/sarv-projects/LiteSPM";

const COUNTS = {
  total: Number(bundledRelease.totalCapabilities ?? bundledRelease.itemCount ?? 0),
  mcp: Number(bundledRelease.mcpServersCount ?? 0),
  skill: Number(bundledRelease.agentSkillsCount ?? 0),
  plugin: Number(bundledRelease.pluginsCount ?? 0),
};

/**
 * The footer carries the site's data-provenance contract. Most indexes bury
 * this; on a catalog with no usage telemetry it is the most useful thing on
 * the page, so it gets real space.
 */
export function SiteFooter() {
  return (
    <footer className="mt-auto border-t border-rule bg-surface">
      <div className="shell grid gap-8 py-10 md:grid-cols-[1.4fr_1fr_1fr]">
        <div>
          <p className="text-[15px] font-semibold text-ink">LiteSPM Market</p>
          <p className="mt-1.5 max-w-prose text-[13px] leading-relaxed text-ink-2">
            One bridge entry per agent, installed once. The catalog is a snapshot of named upstream
            sources, and everything here is resolved locally by the LiteSPM daemon.
          </p>
        </div>

        <div>
          <h2 className="t-mono text-[11px] font-medium text-ink">Indexed</h2>
          <ul className="mt-2 space-y-1.5 text-[13px] text-ink-2">
            <li>
              <Link href="/explore/" className="link touch-link">
                Explore
              </Link>
            </li>
            <li>
              <Link href="/categories/" className="link touch-link">
                Categories
              </Link>
            </li>
            <li>
              <Link href="/agents/" className="link touch-link">
                Agent hosts
              </Link>
            </li>
            <li>
              <Link href="/trending/" className="link touch-link">
                Coverage
              </Link>
            </li>
            <li>
              <a href="/v1/current.json" className="link touch-link t-mono !text-[12px]">
                /v1/current.json
              </a>
            </li>
          </ul>
        </div>

        <div>
          <h2 className="t-mono text-[11px] font-medium text-ink">What the numbers mean</h2>
          <ul className="mt-2 space-y-1.5 text-[12px] leading-relaxed text-ink-3">
            <li>
              <strong className="font-medium text-ink-2">No popularity figures are published.</strong>{" "}
              The upstream sources do not expose machine-readable star or install counts, so the
              catalog shows none rather than an estimate.
            </li>
            <li>
              <CountUp value={HOSTS.length} className="font-medium text-ink-2" /> agent hosts have
              compiled-in config adapters. Every one lists the documentation it was verified against.
            </li>
            <li>
              Publisher &ldquo;vendor-listed&rdquo; means the row came from that publisher&rsquo;s own
              marketplace manifest; no registry verified the publisher and this project did not audit it.
              Rows from third-party awesome-lists are discovery-only: no version or launch command was proven.
            </li>
          </ul>
        </div>
      </div>

      <div className="border-t border-rule bg-sunken">
        <div className="shell flex flex-col gap-4 py-5 sm:flex-row sm:items-center sm:justify-between">
          <dl className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
            <div className="flex items-baseline gap-1.5">
              <dd className="t-cond text-[18px] font-semibold leading-none text-ink">
                <CountUp value={COUNTS.total} />
              </dd>
              <dt className="t-mono text-[11px] text-ink-3">capabilities</dt>
            </div>
            <div className="flex items-baseline gap-1.5">
              <dd className="t-cond text-[18px] font-semibold leading-none text-mcp">
                <CountUp value={COUNTS.mcp} />
              </dd>
              <dt className="t-mono text-[11px] text-ink-3">MCP</dt>
            </div>
            <div className="flex items-baseline gap-1.5">
              <dd className="t-cond text-[18px] font-semibold leading-none text-skill">
                <CountUp value={COUNTS.skill} />
              </dd>
              <dt className="t-mono text-[11px] text-ink-3">skills</dt>
            </div>
            <div className="flex items-baseline gap-1.5">
              <dd className="t-cond text-[18px] font-semibold leading-none text-plugin">
                <CountUp value={COUNTS.plugin} />
              </dd>
              <dt className="t-mono text-[11px] text-ink-3">plugins</dt>
            </div>
          </dl>

          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <p className="t-mono text-[11px] text-ink-3">
              Open source. No analytics, no cookies, no install telemetry.
            </p>
            <a
              href={REPO}
              target="_blank"
              rel="noopener noreferrer"
              className="btn h-7 !text-[11px]"
            >
              GitHub repository
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}
