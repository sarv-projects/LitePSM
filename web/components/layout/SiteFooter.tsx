"use client";

import React from "react";
import Link from "next/link";
import { HOSTS } from "../../lib/hosts";

const REPO = "https://github.com/sarv-projects/LitePSM";

/**
 * The footer carries the site's data-provenance contract. Most indexes bury
 * this; on a catalog with no usage telemetry it is the most useful thing on
 * the page, so it gets real space.
 */
export function SiteFooter() {
  return (
    <footer className="mt-auto border-t border-ink bg-surface">
      <div className="shell grid gap-8 py-10 md:grid-cols-[1.4fr_1fr_1fr]">
        <div>
          <p className="t-cond text-[15px] font-semibold text-ink">LitePSM Market</p>
          <p className="mt-1.5 max-w-prose text-[13px] leading-relaxed text-ink-2">
            A static index built from a single catalog snapshot. Pages are pre-rendered; search runs in
            your browser against the bundled data, so nothing you type leaves the page.
          </p>
        </div>

        <div>
          <h2 className="t-mono text-[11px] font-medium text-ink">Indexed</h2>
          <ul className="mt-2 space-y-1.5 text-[13px] text-ink-2">
            <li>
              <Link href="/explore/" className="link">
                Explore
              </Link>
            </li>
            <li>
              <Link href="/categories/" className="link">
                Categories
              </Link>
            </li>
            <li>
              <Link href="/agents/" className="link">
                Agent hosts
              </Link>
            </li>
            <li>
              <Link href="/trending/" className="link">
                Leaderboards
              </Link>
            </li>
            <li>
              <a href="/v1/current.json" className="link t-mono !text-[12px]">
                /v1/current.json
              </a>
            </li>
          </ul>
        </div>

        <div>
          <h2 className="t-mono text-[11px] font-medium text-ink">What the numbers mean</h2>
          <ul className="mt-2 space-y-1.5 text-[12px] leading-relaxed text-ink-3">
            <li>
              Star counts are an upstream publisher-repo popularity signal. They are not installs, and
              they are never zero-filled when a source publishes nothing.
            </li>
            <li>
              {HOSTS.length} agent hosts have compiled-in config adapters. The catalog names{" "}
              {HOSTS.length + 3} distinct hosts in total.
            </li>
          </ul>
        </div>
      </div>

      <div className="border-t border-rule">
        <div className="shell flex flex-col gap-2 py-4 sm:flex-row sm:items-center sm:justify-between">
          <p className="t-mono text-[11px] text-ink-3">
            LitePSM is open source. No analytics, no cookies, no install telemetry.
          </p>
          <a
            href={REPO}
            target="_blank"
            rel="noopener noreferrer"
            className="t-mono text-[11px] text-ink-2 hover:text-ink hover:underline"
          >
            sarv-projects/LitePSM
          </a>
        </div>
      </div>
    </footer>
  );
}
