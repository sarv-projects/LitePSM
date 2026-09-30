"use client";

import React, { useEffect, useState } from "react";
import { formatDistanceToNow } from "date-fns";
import { Sparkles, Terminal, Shield, Zap } from "lucide-react";

interface CurrentTelemetry {
  itemCount: number;
  createdAt: string;
  releaseId: string;
  sequence: number;
}

export function HeroSection() {
  const [telemetry, setTelemetry] = useState<CurrentTelemetry>({
    itemCount: 4208,
    createdAt: new Date().toISOString(),
    releaseId: "rel-2026-09-30-01",
    sequence: 142,
  });
  const [relativeTime, setRelativeTime] = useState<string>("just now");

  useEffect(() => {
    // Dynamic runtime fetch from /v1/current.json
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
        // Fallback gracefully
        setRelativeTime("recently");
      });
  }, []);

  return (
    <section className="relative pt-12 pb-8 px-4 text-center max-w-4xl mx-auto">
      {/* Background glow orb */}
      <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-96 h-96 bg-emerald-500/10 rounded-full blur-3xl pointer-events-none -z-10" />

      {/* Dynamic Telemetry Eyebrow Pill */}
      <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-emerald-950/60 border border-emerald-500/30 text-emerald-400 text-xs font-mono mb-6 shadow-sm shadow-emerald-900/20 backdrop-blur-md">
        <span className="relative flex h-2 w-2">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
          <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
        </span>
        <span>
          {telemetry.itemCount.toLocaleString()} Capabilities · Updated {relativeTime}
        </span>
      </div>

      {/* Main Headline */}
      <h1 className="text-3xl sm:text-5xl font-extrabold tracking-tight text-white mb-4">
        The Capability Layer for{" "}
        <span className="bg-gradient-to-r from-emerald-400 via-teal-300 to-cyan-400 bg-clip-text text-transparent">
          AI Coding Agents
        </span>
      </h1>

      {/* Subtitle */}
      <p className="text-base sm:text-lg text-gray-400 max-w-2xl mx-auto mb-6 leading-relaxed">
        Discover, verify, and seamlessly install MCP servers, portable skills, and plugins across{" "}
        <span className="text-gray-200 font-medium">Cline</span>,{" "}
        <span className="text-gray-200 font-medium">Pi Agent</span>,{" "}
        <span className="text-gray-200 font-medium">Grok Build</span>,{" "}
        <span className="text-gray-200 font-medium">Codex</span>, and{" "}
        <span className="text-gray-200 font-medium">Claude Code</span>.
      </p>

      {/* Quick Terminal Quickstart Banner */}
      <div className="inline-flex items-center gap-3 bg-[#11131a] border border-[#232734] px-4 py-2 rounded-xl text-xs font-mono text-gray-300 shadow-lg">
        <span className="text-emerald-400 font-bold">$</span>
        <span>npm install -g litepsm && litepsm</span>
        <button
          onClick={() => navigator.clipboard.writeText("npm install -g litepsm && litepsm")}
          className="ml-2 px-2 py-0.5 rounded bg-[#1f2330] hover:bg-emerald-500 hover:text-black text-gray-400 transition-all font-sans text-[11px]"
        >
          Copy
        </button>
      </div>
    </section>
  );
}
