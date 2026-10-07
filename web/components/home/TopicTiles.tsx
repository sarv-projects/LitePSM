"use client";

import React from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import type { TopicStat } from "../../lib/landing";
import { formatCount } from "../../lib/format";

/**
 * The normalized "what do you want your AI to do?" taxonomy.
 *
 * Every tile is a real link into Explore: either an exact `?category=` value
 * the dataset publishes (63 of them, matched case-sensitively) or a `?q=` term
 * the same matcher Explore runs. The friendly label is hand-written; the count
 * and the three example names are computed at build time from the rows the tile
 * actually selects, so a tile can never advertise a capability that its own
 * link would not return.
 */
export function TopicTiles({ topics }: { topics: TopicStat[] }) {
  return (
    <section className="shell band" aria-labelledby="topics-title">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="kicker">Browse by intent</p>
          <h2 id="topics-title" className="band-title mt-2">
            What do you want your AI to do?
          </h2>
          <p className="band-sub">
            Each area lists what the catalog actually holds for it, with three entries you can open
            right away.
          </p>
        </div>
        <Link href="/categories/" className="touch-link text-[13px] text-ink-2 hover:text-accent-text hover:underline">
          All categories →
        </Link>
      </div>

      <ul className="mt-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {topics.map((topic) => (
          <li key={topic.id}>
            <Link
              href={topic.href}
              className="group flex h-full flex-col rounded-card border border-rule bg-surface p-4 transition-[transform,border-color,box-shadow] duration-hover ease-out hover:-translate-y-0.5 hover:border-ink-3 hover:shadow-card"
            >
              <div className="flex items-baseline justify-between gap-3">
                <h3 className="text-[15px] font-semibold tracking-tight text-ink">{topic.label}</h3>
                <span className="t-mono t-tabular shrink-0 text-[12px] text-ink-3">
                  {formatCount(topic.count)}
                </span>
              </div>

              <p className="mt-1.5 text-[13px] leading-relaxed text-ink-2">{topic.blurb}</p>

              <ul className="mt-3 flex flex-wrap gap-1.5">
                {topic.examples.map((name) => (
                  <li
                    key={name}
                    className="t-mono max-w-full truncate rounded-chip border border-rule bg-sunken px-1.5 py-0.5 text-[10.5px] text-ink-2"
                  >
                    {name}
                  </li>
                ))}
              </ul>

              <span className="mt-4 flex items-center gap-1.5 text-[12.5px] font-medium text-ink-3 transition-colors duration-state ease-out group-hover:text-accent-text">
                Explore
                <ArrowRight
                  className="h-3.5 w-3.5 transition-transform duration-hover ease-out group-hover:translate-x-0.5"
                  aria-hidden="true"
                />
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
