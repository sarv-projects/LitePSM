"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { ArrowRight, Terminal, ShieldCheck } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { Listing } from "../../lib/telemetry";
import { agentFacets } from "../../lib/catalog";
import { HOSTS } from "../../lib/hosts";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

export default function AgentsPage() {
  const agents = useMemo(() => agentFacets(items), []);
  const managed = new Set(HOSTS.map((h) => h.name));

  return (
    <div className="flex min-h-screen flex-col bg-[#f0f2f6]">
      <Header />
      <main className="mx-auto w-full max-w-7xl px-4 py-10 lg:px-8">
        <h1 className="text-2xl font-black tracking-tight text-slate-900">Works with your agents</h1>
        <p className="mt-1 max-w-2xl text-sm text-slate-500">
          LitePSM registers a single bridge entry per agent and resolves capabilities centrally at runtime. Counts show
          how many catalog capabilities advertise compatibility with each agent.
        </p>

        <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {agents.map((agent) => (
            <Link
              key={agent.slug}
              href={`/explore/?host=${encodeURIComponent(agent.name)}`}
              className="group flex flex-col justify-between rounded-2xl border border-slate-200/80 bg-white p-5 shadow-sm transition-all hover:border-slate-300 hover:shadow-md"
            >
              <div className="flex items-start justify-between gap-3">
                <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-900">
                  <Terminal className="h-4 w-4 text-emerald-400" aria-hidden="true" />
                </span>
                {managed.has(agent.name) && (
                  <span className="inline-flex items-center gap-1 rounded-full border border-emerald-200 bg-emerald-50 px-2 py-0.5 text-[10px] font-semibold text-emerald-700">
                    <ShieldCheck className="h-3 w-3" aria-hidden="true" /> Managed
                  </span>
                )}
              </div>
              <div className="mt-4">
                <h2 className="text-base font-bold text-slate-900 group-hover:text-emerald-600">{agent.name}</h2>
                <p className="mt-0.5 font-mono text-xs text-slate-500">{agent.count.toLocaleString()} compatible</p>
              </div>
              <span className="mt-3 inline-flex items-center gap-1 text-xs font-semibold text-emerald-700">
                Browse <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
              </span>
            </Link>
          ))}
        </div>

        <p className="mt-8 text-xs text-slate-500">
          Managed adapters with compiled-in config support: {HOSTS.map((h) => h.name).join(", ")}. Other agents appear
          when catalog publishers declare compatibility.
        </p>
      </main>
    </div>
  );
}
