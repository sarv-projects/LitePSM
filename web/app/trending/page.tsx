"use client";

import React, { useMemo, useState } from "react";
import { Star } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { DetailDrawer } from "../../components/catalog/DetailDrawer";
import { Listing } from "../../lib/telemetry";
import { KIND_ORDER, kindLabel, topStarred, sortListings } from "../../lib/catalog";
import { formatStars } from "../../lib/format";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

function Board({
  title,
  subtitle,
  rows,
  onSelect,
}: {
  title: string;
  subtitle?: string;
  rows: Listing[];
  onSelect: (item: Listing) => void;
}) {
  if (!rows.length) return null;
  return (
    <section className="rounded-2xl border border-slate-200/80 bg-white p-5 shadow-sm">
      <h2 className="text-lg font-bold tracking-tight text-slate-900">{title}</h2>
      {subtitle && <p className="mt-0.5 text-xs text-slate-500">{subtitle}</p>}
      <ol className="mt-4 divide-y divide-slate-100">
        {rows.map((item, i) => (
          <li key={item.id}>
            <button
              type="button"
              onClick={() => onSelect(item)}
              className="flex w-full items-center gap-3 py-2.5 text-left transition-colors hover:bg-slate-50"
            >
              <span className="w-6 shrink-0 text-center font-mono text-sm font-bold text-slate-300">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold text-slate-900">{item.name}</span>
                <span className="block truncate text-xs text-slate-500">{item.category}</span>
              </span>
              <span className="flex shrink-0 items-center gap-1 font-mono text-xs text-amber-600">
                <Star className="h-3 w-3 fill-amber-500 text-amber-500" aria-hidden="true" />
                {formatStars(item.stars)}
              </span>
            </button>
          </li>
        ))}
      </ol>
    </section>
  );
}

export default function TrendingPage() {
  const [selected, setSelected] = useState<Listing | null>(null);

  const boards = useMemo(
    () => ({
      overall: topStarred(items, 20),
      newest: sortListings(items, "newest").slice(0, 20),
      byKind: KIND_ORDER.map((k) => ({
        kind: k,
        rows: topStarred(
          items.filter((i) => i.kind === k),
          10
        ),
      })),
    }),
    []
  );

  return (
    <div className="flex min-h-screen flex-col bg-[#f0f2f6]">
      <Header />
      <main className="mx-auto w-full max-w-7xl px-4 py-10 lg:px-8">
        <h1 className="text-2xl font-black tracking-tight text-slate-900">Trending</h1>
        <p className="mt-1 text-sm text-slate-500">
          Rankings derive from publisher star counts in this catalog release. Install/usage telemetry is not collected.
        </p>

        <div className="mt-8 grid grid-cols-1 gap-6 lg:grid-cols-3">
          <Board title="Top 20 overall" subtitle="By GitHub stars" rows={boards.overall} onSelect={setSelected} />
          <Board title="New & noteworthy" subtitle="Most recently added" rows={boards.newest} onSelect={setSelected} />
          {boards.byKind.map((b) => (
            <Board key={b.kind} title={`Top ${kindLabel(b.kind)}`} rows={b.rows} onSelect={setSelected} />
          ))}
        </div>
      </main>

      <DetailDrawer item={selected} onClose={() => setSelected(null)} />
    </div>
  );
}
