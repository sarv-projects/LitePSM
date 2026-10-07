"use client";

import React from "react";
import type { ProvenanceStats } from "../../lib/landing";
import { formatCount } from "../../lib/format";

/**
 * "Know what you're adding." — the trust strip.
 *
 * Every sentence here is checked against a field the dataset actually carries.
 * Where the honest answer is "not published", the card says so in those words
 * instead of implying a capability the catalog does not have; where a number
 * exists, the number is computed from the rows at build time and printed with
 * its unit. The technical expander names the exact field, so a reader can go
 * verify the claim rather than take the paragraph on faith.
 */
export function TrustStrip({ provenance }: { provenance: ProvenanceStats }) {
  const cards: Array<{ title: string; plain: React.ReactNode; tech: React.ReactNode }> = [
    {
      title: "Where it came from",
      plain: (
        <>
          Whether the entry was read out of the publisher&rsquo;s own manifest, or claimed by a
          third-party list. Both are indexed; they are not the same thing.
        </>
      ),
      tech: (
        <>
          <code className="t-mono">publisher.provenance</code> ={" "}
          <code className="t-mono">vendor-manifest</code> ({formatCount(provenance.vendorManifest)}){" "}
          <span className="text-ink-3">or</span> <code className="t-mono">awesome-list-claim</code>{" "}
          ({formatCount(provenance.awesomeList)}).
        </>
      ),
    },
    {
      title: "Who publishes it",
      plain: (
        <>
          The name the upstream registry records. &ldquo;Vendor-listed&rdquo; means the row came from
          that publisher&rsquo;s own marketplace manifest — no registry audited them, and neither did
          this project.
        </>
      ),
      tech: (
        <>
          <code className="t-mono">publisher.name</code>,{" "}
          <code className="t-mono">publisher.url</code>, and the boolean{" "}
          <code className="t-mono">publisher.verified</code> — a provenance flag, explicitly not a
          security review.
        </>
      ),
    },
    {
      title: "What it can access",
      plain: (
        <>
          Not published per entry today. What you do get is the config file LiteSPM would write and,
          where one was proven, the exact command the server launches with.
        </>
      ),
      tech: (
        <>
          Published where known: <code className="t-mono">command</code>,{" "}
          <code className="t-mono">args</code>, <code className="t-mono">installHint</code>,{" "}
          <code className="t-mono">skillSource</code>. A per-entry effect declaration is designed
          (<code className="t-mono">effects</code>), not yet published in the catalog.
        </>
      ),
    },
    {
      title: "Whether LiteSPM tested it",
      plain: (
        <>
          Each entry states how far it got: discovery only, metadata checked, runtime proven, or
          tested by LiteSPM. Nothing is promoted to a higher level for looking popular.
        </>
      ),
      tech: (
        <>
          <code className="t-mono">installability</code>:{" "}
          <code className="t-mono">discovery_only</code> ({formatCount(provenance.discoveryOnly)}),{" "}
          <code className="t-mono">metadata_verified</code> (
          {formatCount(provenance.metadataVerified)}),{" "}
          <code className="t-mono">runtime_verified</code> ({formatCount(provenance.runtimeVerified)}
          ), <code className="t-mono">litespm_tested</code> ({formatCount(provenance.litespmTested)}
          ).
        </>
      ),
    },
    {
      title: "What version",
      plain: (
        <>
          A version string when the publisher published one. When they did not, the field is empty
          and the page says <span className="absent">not published</span> rather than guessing.
        </>
      ),
      tech: (
        <>
          <code className="t-mono">version</code> is present on{" "}
          <span className="t-mono">{formatCount(provenance.versioned)}</span> of{" "}
          <span className="t-mono">{formatCount(provenance.total)}</span> entries.
        </>
      ),
    },
  ];

  return (
    <section className="shell band" aria-labelledby="trust-title">
      <div className="rounded-card border border-rule bg-surface p-5 sm:p-7">
        <p className="kicker">Trust</p>
        <h2 id="trust-title" className="band-title mt-2">
          Know what you&rsquo;re adding.
        </h2>
        <p className="band-sub">
          Five questions, answered on every entry page — including the ones whose honest answer is
          &ldquo;not published yet&rdquo;.
        </p>

        <ul className="mt-6 grid gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-5">
          {cards.map((card) => (
            <li key={card.title} className="border-t border-rule pt-3">
              <h3 className="text-[14px] font-semibold tracking-tight text-ink">{card.title}</h3>
              <p className="mt-1.5 text-[13px] leading-relaxed text-ink-2">{card.plain}</p>
              <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{card.tech}</p>
            </li>
          ))}
        </ul>

        <details className="group mt-7 border-t border-rule pt-2">
          <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[13px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[40px]">
            Technical provenance
            <span
              aria-hidden="true"
              className="text-ink-3 transition-transform duration-panel ease-out group-open:rotate-90"
            >
              →
            </span>
          </summary>
          <dl className="spec-list pb-2">
            <div>
              <dt>sha256</dt>
              <dd>
                The release manifest digest is a sha256 over <code className="t-mono">manifest.json</code>;
                the client verifies pointer → manifest → listings before a row is trusted.
              </dd>
            </div>
            <div>
              <dt>manifestDigest</dt>
              <dd>
                <code className="t-mono">data/release.json → manifestDigest</code> — the digest this
                build shipped with, shown in the release chip at the top of the page.
              </dd>
            </div>
            <div>
              <dt>datasetDigest</dt>
              <dd>
                <code className="t-mono">data/release.json → datasetDigest</code> — stamped on the
                lazy row request as <code className="t-mono">?v=…</code>, so a CDN cannot answer a
                newer build with older rows than the HTML beside them.
              </dd>
            </div>
            <div>
              <dt>signature</dt>
              <dd>
                The published release pointer is signed with a keyless Sigstore bundle;
                <code className="t-mono"> litespm catalog sync</code> verifies it and refuses an
                unsigned origin when <code className="t-mono">LITESPM_REQUIRE_CATALOG_SIGNATURE=1</code>.
                This page does not verify signatures — it is static HTML.
              </dd>
            </div>
            <div>
              <dt>byte-parity</dt>
              <dd>
                <code className="t-mono">/data/catalog.json</code> is exported byte-for-byte from
                <code className="t-mono"> web/data/catalog.json</code>, so the rows you fetch on
                demand are the rows this page was built from.
              </dd>
            </div>
          </dl>
        </details>
      </div>
    </section>
  );
}
