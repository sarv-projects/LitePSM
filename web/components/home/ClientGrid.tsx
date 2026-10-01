"use client";

import React from "react";
import { Terminal } from "lucide-react";
import { HOSTS } from "../../lib/hosts";

/** "Works with" row listing the six host adapters LitePSM supports. */
export function ClientGrid() {
  return (
    <section className="mx-auto w-full max-w-7xl px-4 pb-10 lg:px-8">
      <div className="mb-4">
        <h2 className="text-lg font-bold tracking-tight text-slate-900">Works with your agents</h2>
        <p className="mt-0.5 text-xs text-slate-500">
          One bridge per host. Capabilities are resolved centrally at runtime.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
        {HOSTS.map((host) => (
          <div
            key={host.id}
            className="flex flex-col items-start gap-2 rounded-2xl border border-slate-200/80 bg-white p-4 shadow-sm transition-colors hover:border-slate-300"
          >
            <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-slate-900">
              <Terminal className="h-4 w-4 text-emerald-400" aria-hidden="true" />
            </span>
            <span className="text-sm font-semibold text-slate-900">{host.name}</span>
            <span className="font-mono text-[10px] uppercase text-slate-400">{host.kind}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
