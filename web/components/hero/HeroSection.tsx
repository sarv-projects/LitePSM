"use client";

import React, { useEffect, useState } from "react";
import { formatDistanceToNow } from "date-fns";
import { Check, Copy } from "lucide-react";

interface CurrentTelemetry {
  totalCapabilities?: number;
  itemCount?: number;
  createdAt?: string;
  releaseId?: string;
  sequence?: number;
}

export function HeroSection() {
  const [telemetry, setTelemetry] = useState<CurrentTelemetry>({
    totalCapabilities: 5185,
    createdAt: new Date().toISOString(),
    releaseId: "rel-2026-10-01-01",
    sequence: 142,
  });
  const [relativeTime, setRelativeTime] = useState<string>("just now");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    fetch("/v1/current.json")
      .then((res) => {
        if (res.ok) return res.json();
        throw new Error("Failed to fetch /v1/current.json");
      })
      .then((data: CurrentTelemetry) => {
        setTelemetry(data);
        if (data.createdAt) {
          try {
            setRelativeTime(formatDistanceToNow(new Date(data.createdAt)) + " ago");
          } catch {
            setRelativeTime("recently");
          }
        }
      })
      .catch(() => {
        setRelativeTime("recently");
      });
  }, []);

  const count = telemetry.totalCapabilities || telemetry.itemCount || 5185;

  const copyQuickstart = () => {
    navigator.clipboard.writeText("npm install -g litepsm && litepsm");
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="relative pt-12 sm:pt-16 pb-8 px-4 text-center max-w-4xl mx-auto">
      {/* Dynamic Telemetry Eyebrow Pill */}
      <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-white border border-slate-200/90 text-slate-700 text-xs font-mono mb-6 shadow-sm">
        <span className="relative flex h-2 w-2">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
          <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
        </span>
        <span className="font-semibold text-slate-900">
          {count.toLocaleString()} Capabilities
        </span>
        <span className="text-slate-400">·</span>
        <span className="text-slate-500">Updated {relativeTime}</span>
      </div>

      {/* Main Headline */}
      <h1 className="text-3xl sm:text-5xl lg:text-6xl font-black tracking-tight text-slate-900 mb-4 font-sans">
        The Capability Catalog for{" "}
        <span className="bg-gradient-to-r from-emerald-600 via-teal-600 to-cyan-600 bg-clip-text text-transparent">
          AI Coding Agents
        </span>
      </h1>

      {/* Subtitle */}
      <p className="text-base sm:text-lg text-slate-600 max-w-2xl mx-auto mb-8 leading-relaxed">
        Comprehensive registry of every verified MCP server, portable Agent Skill, and plugin for{" "}
        <strong className="text-slate-800 font-semibold">Claude Code</strong>,{" "}
        <strong className="text-slate-800 font-semibold">Codex</strong>,{" "}
        <strong className="text-slate-800 font-semibold">OpenCode</strong>, and many more.
      </p>

      {/* Quick Terminal Quickstart Banner */}
      <div className="inline-flex items-center gap-3 bg-[#0d1117] border border-slate-800 px-4 py-2.5 rounded-2xl text-xs font-mono text-slate-300 shadow-xl max-w-full overflow-x-auto">
        <span className="text-emerald-400 font-bold select-none">$</span>
        <span className="text-slate-100 select-all font-semibold">npm install -g litepsm && litepsm</span>
        <button
          onClick={copyQuickstart}
          className="ml-2 flex items-center gap-1 px-2.5 py-1 rounded-lg bg-[#21262d] hover:bg-emerald-500 hover:text-slate-950 text-slate-300 transition-all font-sans text-xs border border-slate-700"
        >
          {copied ? (
            <>
              <Check className="w-3.5 h-3.5 text-emerald-400" />
              <span>Copied</span>
            </>
          ) : (
            <>
              <Copy className="w-3.5 h-3.5 text-slate-400" />
              <span>Copy</span>
            </>
          )}
        </button>
      </div>
    </section>
  );
}
