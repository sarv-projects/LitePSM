"use client";

import React from "react";
import { formatDistanceToNow } from "date-fns";
import { Check, Copy } from "lucide-react";
import { copyText } from "../../lib/clipboard";
import { TelemetryState } from "../../lib/telemetry";

interface HeroSectionProps {
  telemetry: TelemetryState;
}

export function HeroSection({ telemetry }: HeroSectionProps) {
  const { data, status } = telemetry;
  const [copied, setCopied] = React.useState(false);

  const relativeTime = React.useMemo(() => {
    if (!data.createdAt) return null;
    try {
      return `${formatDistanceToNow(new Date(data.createdAt))} ago`;
    } catch {
      return null;
    }
  }, [data.createdAt]);

  const onCopy = async () => {
    const ok = await copyText("npm install -g litepsm && litepsm", "Quickstart copied");
    if (!ok) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="relative mx-auto max-w-4xl px-4 pb-8 pt-12 text-center sm:pt-16">
      <div className="mb-6 inline-flex items-center gap-2 rounded-full border border-slate-200/90 bg-white px-3.5 py-1.5 font-mono text-xs text-slate-700 shadow-sm">
        <span className="relative flex h-2 w-2" aria-hidden="true">
          {status === "ready" && (
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75 motion-reduce:animate-none" />
          )}
          <span
            className={`relative inline-flex h-2 w-2 rounded-full ${
              status === "ready" ? "bg-emerald-500" : status === "offline" ? "bg-amber-500" : "bg-slate-400"
            }`}
          />
        </span>
        <span className="font-semibold text-slate-900">
          {status === "loading" ? "Loading capabilities..." : `${data.itemCount.toLocaleString()} Capabilities`}
        </span>
        <span className="text-slate-400">·</span>
        <span className="text-slate-500">
          {status === "offline" ? "Offline (cached)" : relativeTime ? `Updated ${relativeTime}` : "Live"}
        </span>
      </div>

      <h1 className="mb-4 text-3xl font-black tracking-tight text-slate-900 sm:text-5xl lg:text-6xl">
        The Capability Catalog for{" "}
        <span className="bg-gradient-to-r from-emerald-600 via-teal-600 to-cyan-600 bg-clip-text text-transparent">
          AI Coding Agents
        </span>
      </h1>

      <p className="mx-auto mb-8 max-w-2xl text-base leading-relaxed text-slate-600 sm:text-lg">
        Comprehensive registry of verified MCP servers, portable Agent Skills, and plugins for{" "}
        <strong className="font-semibold text-slate-800">Claude Code</strong>,{" "}
        <strong className="font-semibold text-slate-800">Codex</strong>,{" "}
        <strong className="font-semibold text-slate-800">OpenCode</strong>, and many more.
      </p>

      <div className="inline-flex max-w-full items-center gap-3 overflow-x-auto rounded-2xl border border-slate-800 bg-[#0d1117] px-4 py-2.5 font-mono text-xs text-slate-300 shadow-xl">
        <span className="select-none font-bold text-emerald-400" aria-hidden="true">
          $
        </span>
        <span className="select-all font-semibold text-slate-100">npm install -g litepsm &amp;&amp; litepsm</span>
        <button
          type="button"
          onClick={onCopy}
          aria-label="Copy quickstart command"
          className="ml-2 flex items-center gap-1 rounded-lg border border-slate-700 bg-[#21262d] px-2.5 py-1 font-sans text-xs text-slate-300 transition-all hover:bg-emerald-500 hover:text-slate-950"
        >
          {copied ? (
            <>
              <Check className="h-3.5 w-3.5 text-emerald-400" aria-hidden="true" />
              <span>Copied</span>
            </>
          ) : (
            <>
              <Copy className="h-3.5 w-3.5 text-slate-400" aria-hidden="true" />
              <span>Copy</span>
            </>
          )}
        </button>
      </div>
    </section>
  );
}
