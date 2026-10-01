"use client";

import React from "react";
import { Check, Copy } from "lucide-react";
import { copyText } from "../../lib/clipboard";
import { TelemetryState } from "../../lib/telemetry";
import { formatCount, shortDigest } from "../../lib/format";
import { ProportionBar } from "./ProportionBar";
import { kindBreakdown } from "../../lib/catalog";
import { Listing } from "../../lib/telemetry";

interface HeroSectionProps {
  telemetry: TelemetryState;
  items: Listing[];
  hostCount: number;
}

function relativeAge(iso: string | undefined): string | null {
  if (!iso) return null;
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return null;
  const days = Math.floor((Date.now() - then) / 86_400_000);
  if (days < 1) return "today";
  if (days === 1) return "yesterday";
  if (days < 30) return `${days} days ago`;
  return new Date(then).toISOString().slice(0, 10);
}

export function HeroSection({ telemetry, items, hostCount }: HeroSectionProps) {
  const { data, status } = telemetry;
  const [copied, setCopied] = React.useState(false);

  const parts = React.useMemo(() => kindBreakdown(items), [items]);
  const age = relativeAge(data.createdAt);
  const digest = shortDigest(data.manifestDigest);

  const onCopy = async () => {
    const ok = await copyText("npm install -g litepsm && litepsm", "Quickstart command copied");
    if (!ok) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="shell pt-10 pb-8 md:pt-14">
      <div className="border-l-2 border-ink pl-5 md:pl-7">
        <h1 className="t-cond max-w-2xl text-[30px] font-semibold leading-[1.12] tracking-tight text-ink sm:text-[38px]">
          {formatCount(items.length)} capabilities, one bridge per agent
        </h1>
        <p className="mt-3 max-w-prose text-[14px] leading-relaxed text-ink-2 sm:text-[15px]">
          An index of MCP servers, portable agent skills, and plugins. LitePSM injects a single
          version-pinned bridge entry into each agent host and resolves the rest locally at runtime.
        </p>

        <div className="mt-7 max-w-2xl">
          <ProportionBar parts={parts} />
        </div>

        <div className="mt-6 flex max-w-2xl flex-wrap items-center gap-x-5 gap-y-2 text-[12px] text-ink-3">
          <span>
            <span className="t-mono t-tabular text-ink">{formatCount(hostCount)}</span> agent hosts named in the
            catalog
          </span>
          {/*
            The manifest count is a second number about the same thing, and the
            release stamp below already names the release. It only earns its
            place when the two disagree, because that gap is a real fact about
            this build. Never rendered while loading: "0 capabilities" would be
            a false number, which is the one thing this site must not show.
          */}
          {status === "ready" && data.counts.all > 0 && data.counts.all !== items.length && (
            <span>
              <span className="t-mono t-tabular text-ink">{formatCount(data.counts.all)}</span> in the release
              manifest, {formatCount(items.length - data.counts.all)} not yet in this build
            </span>
          )}
        </div>
      </div>

      <div className="mt-8 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="on-dark flex h-10 min-w-0 flex-1 items-center gap-2 rounded-panel bg-dark px-3 sm:max-w-lg">
          <span className="t-mono shrink-0 text-[12px] text-dark-ink-2" aria-hidden="true">
            $
          </span>
          <code className="t-mono min-w-0 flex-1 truncate text-[12px] text-dark-ink select-all">
            npm install -g litepsm &amp;&amp; litepsm
          </code>
          <button type="button" onClick={onCopy} aria-label="Copy quickstart command" className="btn !h-6 shrink-0 !px-2">
            {copied ? (
              <>
                <Check className="h-3 w-3 text-ink" aria-hidden="true" />
                <span>Copied</span>
              </>
            ) : (
              <>
                <Copy className="h-3 w-3" aria-hidden="true" />
                <span>Copy</span>
              </>
            )}
          </button>
        </div>

        <ReleaseStamp status={status} age={age} digest={digest} releaseId={data.releaseId} sequence={data.sequence} />
      </div>
    </section>
  );
}

/**
 * Telemetry honesty: the badge states what was actually fetched. When
 * /v1/current.json is unreachable the UI says so rather than showing a
 * confident green "live" dot.
 */
function ReleaseStamp({
  status,
  age,
  digest,
  releaseId,
  sequence,
}: {
  status: TelemetryState["status"];
  age: string | null;
  digest: string | null;
  releaseId?: string;
  sequence?: number;
}) {
  const tone =
    status === "ready" ? "text-ink" : status === "offline" ? "text-ink-3" : "text-ink-3";

  return (
    <p className={`t-mono flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] ${tone}`}>
      {status === "loading" && <span>checking release manifest…</span>}

      {status === "ready" && (
        <>
          <span className="inline-flex items-center gap-1.5">
            <span aria-hidden="true" className="inline-block h-1.5 w-1.5 rotate-45 bg-ink" />
            live manifest
          </span>
          {releaseId && <span className="text-ink-2">{releaseId}</span>}
          <span className="hidden sm:inline">
            {typeof sequence === "number" ? `seq ${sequence}` : null}
            {typeof sequence === "number" && digest ? " " : null}
            {digest}
          </span>
          {age && <span>published {age}</span>}
        </>
      )}

      {status === "offline" && <span>release manifest unreachable — showing the catalog bundled in this build</span>}
    </p>
  );
}
