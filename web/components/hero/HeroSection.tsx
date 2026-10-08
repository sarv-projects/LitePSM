"use client";

import React from "react";
import { Check, Copy } from "lucide-react";
import { copyText } from "../../lib/clipboard";
import { TelemetryState } from "../../lib/telemetry";
import { formatCount, shortDigest } from "../../lib/format";
import { ProportionBar } from "./ProportionBar";
import { Listing } from "../../lib/telemetry";

interface HeroSectionProps {
  telemetry: TelemetryState;
  /** Row count in this build, computed server-side and passed as a prop. */
  total: number;
  /** Kind shares for the proportion bar — `kindBreakdown(rows)` at build time. */
  breakdown: Array<{ kind: Listing["kind"]; count: number; share: number }>;
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

export function HeroSection({ telemetry, total, breakdown, hostCount }: HeroSectionProps) {
  const { data, status } = telemetry;
  const [copied, setCopied] = React.useState(false);

  const parts = breakdown;
  // `relativeAge` reads Date.now(), so computing it during render would give the
  // prerendered HTML and the client's first render two different answers
  // ("yesterday" vs "today") and trip a hydration mismatch. It is therefore
  // measured after mount; the server renders no age at all.
  const [age, setAge] = React.useState<string | null>(null);
  React.useEffect(() => {
    setAge(relativeAge(data.createdAt));
  }, [data.createdAt]);
  const digest = shortDigest(data.manifestDigest);

  const onCopy = async () => {
    const ok = await copyText("npm install -g litespm && litespm", "Quickstart command copied");
    if (!ok) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="shell pt-10 pb-8 md:pt-14">
      <div className="border-l-2 border-ink pl-5 md:pl-7">
        <h1 className="t-cond max-w-2xl text-[30px] font-semibold leading-[1.12] tracking-tight text-ink sm:text-[38px]">
          {formatCount(total)} capabilities, one bridge per agent
        </h1>
        <p className="mt-3 max-w-prose text-[14px] leading-relaxed text-ink-2 sm:text-[15px]">
          An index of MCP servers, portable agent skills, and plugins. LiteSPM injects a single
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
          {status === "ready" && data.counts.all > 0 && data.counts.all !== total && (
            <span>
              <span className="t-mono t-tabular text-ink">{formatCount(data.counts.all)}</span> in the release
              manifest, {formatCount(total - data.counts.all)} not yet in this build
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
            npm install -g litespm &amp;&amp; litespm
          </code>
          <button type="button" onClick={onCopy} aria-label="Copy quickstart command" className="btn h-6! shrink-0 px-2!">
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
 * Release stamp for the catalog pointer.
 *
 * It only says "Live catalog" once `/v1/current.json` has actually been fetched
 * and answered; until then — and on the prerendered HTML, where no fetch has
 * happened — it says so plainly. Nothing here is a heartbeat or a fabricated
 * liveness signal: the dot marks a completed request, and `offline` says the
 * bundled snapshot is what you are looking at.
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
  const technicalInfo = [
    releaseId,
    typeof sequence === "number" ? `seq ${sequence}` : null,
    digest,
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <div
      className="inline-flex items-center gap-2 rounded-full border border-rule bg-surface px-3 py-1 text-[12px] text-ink-2 shadow-xs"
      title={technicalInfo ? `Release verification: ${technicalInfo}` : undefined}
    >
      {status === "loading" && (
        <span className="flex items-center gap-1.5 text-ink-3 text-[11px]">
          <span className="h-2 w-2 animate-pulse rounded-full bg-ink-3/40" aria-hidden="true" />
          Checking release…
        </span>
      )}

      {status === "ready" && (
        <span className="flex items-center gap-1.5 text-[11.5px]">
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent/40 opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-accent" />
          </span>
          <span className="font-medium text-ink">Live catalog</span>
          {age && <span className="text-ink-3 font-normal">· Updated {age}</span>}
        </span>
      )}

      {status === "offline" && (
        <span className="flex items-center gap-1.5 text-ink-3 text-[11px]">
          <span className="h-2 w-2 rounded-full bg-ink-3/40" aria-hidden="true" />
          Bundled catalog
        </span>
      )}
    </div>
  );
}
