"use client";

import React, { useEffect, useMemo, useState, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  ArrowLeft,
  Copy,
  Star,
  ShieldCheck,
  ExternalLink,
  FolderOpen,
  ChevronRight,
  Info,
  PackageSearch,
  Terminal,
} from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { Listing } from "../../lib/telemetry";
import { HOSTS, bridgeSnippet, nativeSnippet, PlatformOS } from "../../lib/hosts";
import { copyText } from "../../lib/clipboard";
import { formatStars } from "../../lib/format";
import { kindLabel } from "../../lib/catalog";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];

function findItem(slug: string | null): Listing | null {
  if (!slug) return null;
  return items.find((i) => i.slug === slug) ?? items.find((i) => i.id === slug) ?? null;
}

function NotFound() {
  return (
    <div className="mx-auto max-w-lg px-4 py-24 text-center">
      <PackageSearch className="mx-auto mb-4 h-12 w-12 text-slate-400" aria-hidden="true" />
      <h1 className="mb-2 text-xl font-bold text-slate-900">Package not found</h1>
      <p className="mb-6 text-sm text-slate-500">
        This package is not part of the current catalog release. It may have been renamed or withdrawn.
      </p>
      <Link
        href="/explore/"
        className="inline-flex items-center gap-2 rounded-xl bg-slate-900 px-4 py-2.5 text-xs font-semibold text-white transition-all hover:bg-slate-700"
      >
        <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" /> Browse the catalog
      </Link>
    </div>
  );
}

function PackageContent() {
  const searchParams = useSearchParams();
  const slug = searchParams.get("slug");

  const item = useMemo(() => findItem(slug), [slug]);

  const [activeHost, setActiveHost] = useState<string>("claude-code");
  const [platformOs, setPlatformOs] = useState<PlatformOS>("linux");
  const [snippetMode, setSnippetMode] = useState<"bridge" | "native">("bridge");

  useEffect(() => {
    const ua = typeof navigator !== "undefined" ? navigator.userAgent.toLowerCase() : "";
    setPlatformOs(ua.includes("mac") ? "mac" : ua.includes("linux") ? "linux" : "win");
  }, []);

  const host = HOSTS.find((h) => h.id === activeHost) ?? HOSTS[0];

  const snippet = item
    ? snippetMode === "bridge"
      ? bridgeSnippet(host)
      : nativeSnippet(host, item.slug, item.command, item.args)
    : "";

  const related = useMemo(() => {
    if (!item) return [];
    return items.filter((i) => i.id !== item.id && i.category === item.category).slice(0, 3);
  }, [item]);

  if (!item) {
    return (
      <div className="flex min-h-screen flex-col bg-[#f0f2f6]">
        <Header />
        <NotFound />
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-[#f0f2f6] text-slate-800">
      <Header />

      <div className="w-full border-b border-slate-200/80 bg-white/70 px-4 py-3 backdrop-blur-md lg:px-8">
        <div className="mx-auto flex max-w-7xl items-center justify-between font-mono text-xs text-slate-500">
          <div className="flex min-w-0 items-center gap-2">
            <Link href="/explore/" className="flex items-center gap-1.5 font-sans font-medium text-slate-600 transition-colors hover:text-emerald-600">
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" /> Explore
            </Link>
            <ChevronRight className="h-3 w-3 text-slate-300" aria-hidden="true" />
            <span>{kindLabel(item.kind)}</span>
            <ChevronRight className="h-3 w-3 text-slate-300" aria-hidden="true" />
            <span className="truncate font-semibold text-slate-900">{item.name}</span>
          </div>
          <code className="hidden rounded border border-slate-200 bg-slate-100 px-2 py-0.5 text-slate-600 sm:inline">
            {item.id}
          </code>
        </div>
      </div>

      <main className="mx-auto w-full max-w-7xl flex-1 space-y-8 px-4 py-8 lg:px-8">
        {/* Hero */}
        <section className="relative overflow-hidden rounded-3xl border border-slate-200/80 bg-white p-6 shadow-[0_4px_20px_-4px_rgba(15,23,42,0.05)] sm:p-8">
          <div className="card-dither-strip absolute left-0 right-0 top-0 h-1.5" />
          <div className="flex flex-col justify-between gap-6 pt-2 lg:flex-row lg:items-start">
            <div className="max-w-3xl space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="rounded-full border border-emerald-200/80 bg-emerald-50 px-2.5 py-1 font-mono text-xs font-medium text-emerald-700">
                  {kindLabel(item.kind)}
                </span>
                <span className="rounded-full border border-slate-200 bg-slate-100 px-2.5 py-1 font-mono text-xs text-slate-500">
                  {item.category}
                </span>
                {item.transport && (
                  <span className="rounded-full border border-slate-200 bg-slate-50 px-2.5 py-1 font-mono text-xs text-slate-500">
                    {item.transport}
                  </span>
                )}
                <span className="rounded-full border border-slate-200 bg-slate-50 px-2 py-1 font-mono text-xs text-slate-400">
                  v{item.version || "1.0.0"}
                </span>
              </div>

              <h1 className="text-2xl font-extrabold tracking-tight text-slate-900 sm:text-4xl">{item.name}</h1>
              <p className="text-sm leading-relaxed text-slate-600 sm:text-base">{item.summary}</p>

              <div className="flex flex-wrap items-center gap-4 pt-1 text-xs text-slate-500">
                <div className="flex items-center gap-1.5">
                  <span>Published by</span>
                  <strong className="font-semibold text-slate-800">{item.publisher?.name || "unknown"}</strong>
                  {item.publisher?.verified && <ShieldCheck className="h-4 w-4 text-emerald-600" aria-label="Verified publisher" />}
                </div>
                <div className="flex items-center gap-1 rounded-full border border-amber-200/60 bg-amber-50 px-2.5 py-0.5 font-mono text-amber-600">
                  <Star className="h-3.5 w-3.5 fill-amber-500 text-amber-500" aria-hidden="true" />
                  <span className="font-semibold">{formatStars(item.stars)} Stars</span>
                </div>
                {item.publisher?.url && (
                  <a
                    href={item.publisher.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-emerald-600 underline underline-offset-4 hover:text-emerald-700"
                  >
                    Upstream Source <ExternalLink className="h-3 w-3" aria-hidden="true" />
                  </a>
                )}
              </div>
            </div>

            <div className="w-full shrink-0 space-y-3 rounded-2xl border border-slate-800 bg-slate-900 p-5 text-white shadow-xl lg:w-80">
              <span className="font-mono text-xs font-semibold uppercase tracking-wider text-emerald-400">
                Install with LitePSM
              </span>
              <div className="select-all break-all rounded-xl border border-slate-800 bg-black/60 p-3 font-mono text-xs text-emerald-300">
                litepsm install {item.id}
              </div>
              <button
                type="button"
                onClick={() => copyText(`litepsm install ${item.id}`, "Install command copied")}
                className="flex w-full items-center justify-center gap-2 rounded-xl bg-emerald-500 py-2.5 text-xs font-bold text-slate-950 shadow-md transition-all hover:bg-emerald-400"
              >
                <Copy className="h-4 w-4" aria-hidden="true" /> Copy command
              </button>
              <p className="text-[11px] leading-relaxed text-slate-400">
                Or run <code className="text-slate-200">/litepsm</code> in your agent and search &ldquo;{item.name}
                &rdquo;.
              </p>
            </div>
          </div>
        </section>

        {/* Compatibility */}
        <section className="rounded-3xl border border-slate-200/80 bg-white p-6 shadow-sm sm:p-8">
          <h2 className="text-lg font-bold text-slate-900">Compatibility</h2>
          <p className="mt-0.5 text-xs text-slate-500">
            Agents this package advertises support for. LitePSM manages a single bridge entry per agent.
          </p>
          <div className="mt-4 flex flex-wrap gap-2">
            {(item.testedHosts || []).length ? (
              item.testedHosts.map((h) => (
                <span
                  key={h}
                  className="inline-flex items-center gap-1.5 rounded-full border border-slate-200 bg-slate-50 px-3 py-1.5 text-xs font-medium text-slate-700"
                >
                  <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden="true" />
                  {h}
                </span>
              ))
            ) : (
              <span className="text-xs text-slate-500">No agent compatibility declared for this package.</span>
            )}
          </div>
        </section>

        {/* Setup */}
        <section className="space-y-6 rounded-3xl border border-slate-200/80 bg-white p-6 shadow-sm sm:p-8">
          <div className="flex flex-col justify-between gap-4 border-b border-slate-100 pb-5 sm:flex-row sm:items-center">
            <div>
              <h2 className="text-lg font-bold text-slate-900 sm:text-xl">Setup</h2>
              <p className="mt-0.5 text-xs text-slate-500 sm:text-sm">
                Pick your agent and OS for the exact config file and the single LitePSM bridge entry.
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-1 self-start rounded-xl border border-slate-200 bg-slate-100 p-1 text-xs font-medium text-slate-600 sm:self-auto">
              {(["win", "mac", "linux"] as PlatformOS[]).map((o) => (
                <button
                  key={o}
                  type="button"
                  aria-pressed={platformOs === o}
                  onClick={() => setPlatformOs(o)}
                  className={`rounded-lg px-3 py-1.5 transition-all ${
                    platformOs === o ? "bg-white font-semibold text-slate-900 shadow-sm" : "hover:text-slate-900"
                  }`}
                >
                  {o === "win" ? "Windows" : o === "mac" ? "macOS" : "Linux"}
                </button>
              ))}
            </div>
          </div>

          <div className="no-scrollbar flex items-center gap-2 overflow-x-auto pb-2">
            {HOSTS.map((h) => {
              const isActive = activeHost === h.id;
              return (
                <button
                  key={h.id}
                  type="button"
                  aria-pressed={isActive}
                  onClick={() => setActiveHost(h.id)}
                  className={`flex items-center gap-2 whitespace-nowrap rounded-xl border px-4 py-2.5 text-xs font-semibold transition-all ${
                    isActive
                      ? "border-slate-900 bg-slate-900 text-white shadow-md"
                      : "border-slate-200 bg-slate-50 text-slate-600 hover:border-slate-300 hover:bg-slate-100"
                  }`}
                >
                  <Terminal className="h-3.5 w-3.5" aria-hidden="true" />
                  {h.name}
                </button>
              );
            })}
          </div>

          <div className="space-y-2 rounded-2xl border border-slate-200/90 bg-slate-50 p-4">
            <div className="flex items-center justify-between font-mono text-xs">
              <span className="flex items-center gap-1.5 font-medium text-slate-700">
                <FolderOpen className="h-4 w-4 text-emerald-600" aria-hidden="true" /> Target configuration file
              </span>
              <span className="uppercase text-slate-400">{platformOs}</span>
            </div>
            <div className="flex items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white px-3.5 py-2.5 shadow-sm">
              <code className="select-all break-all font-mono text-xs text-slate-800">{host.paths[platformOs]}</code>
              <button
                type="button"
                onClick={() => copyText(host.paths[platformOs], "Path copied")}
                aria-label="Copy configuration file path"
                className="flex shrink-0 items-center gap-1 rounded-lg border border-slate-200 bg-slate-100 px-3 py-1.5 text-xs font-medium text-slate-700 transition-all hover:bg-emerald-600 hover:text-white"
              >
                <Copy className="h-3.5 w-3.5" aria-hidden="true" /> Copy path
              </button>
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
              <div className="flex items-center gap-2">
                <span className="text-xs font-bold uppercase tracking-wider text-slate-900">Configuration</span>
                <span className="text-[11px] text-slate-400">({host.kind === "toml" ? "TOML" : "JSON"})</span>
              </div>
              <div className="flex items-center gap-1 rounded-xl border border-slate-200 bg-slate-100 p-1 text-xs">
                <button
                  type="button"
                  aria-pressed={snippetMode === "bridge"}
                  onClick={() => setSnippetMode("bridge")}
                  className={`rounded-lg px-3 py-1 transition-all ${
                    snippetMode === "bridge" ? "bg-emerald-600 font-semibold text-white" : "text-slate-600 hover:text-slate-900"
                  }`}
                >
                  LitePSM Bridge
                </button>
                <button
                  type="button"
                  aria-pressed={snippetMode === "native"}
                  onClick={() => setSnippetMode("native")}
                  className={`rounded-lg px-3 py-1 transition-all ${
                    snippetMode === "native" ? "bg-slate-800 font-semibold text-white" : "text-slate-600 hover:text-slate-900"
                  }`}
                >
                  Direct Native
                </button>
              </div>
            </div>

            <div className="relative overflow-hidden rounded-2xl border border-slate-800 bg-[#0d1117] shadow-2xl">
              <div className="flex items-center justify-between border-b border-slate-800 bg-[#161b22] px-4 py-2.5 font-mono text-xs text-slate-400">
                <span className="font-semibold text-slate-300">
                  {host.name} · {host.kind === "toml" ? "config.toml" : "config.json"}
                </span>
                <button
                  type="button"
                  onClick={() => copyText(snippet, "Snippet copied")}
                  aria-label="Copy configuration snippet"
                  className="flex items-center gap-1.5 rounded-lg border border-slate-700 bg-[#21262d] px-3 py-1 font-sans text-xs text-slate-200 transition-all hover:bg-emerald-500 hover:text-slate-950"
                >
                  <Copy className="h-3.5 w-3.5" aria-hidden="true" /> Copy
                </button>
              </div>
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap p-5 font-mono text-xs leading-relaxed text-slate-200 sm:text-sm">
                {snippet}
              </pre>
            </div>

            <div className="flex items-start gap-2 rounded-xl border border-amber-200/80 bg-amber-50 p-3 text-xs text-amber-800">
              <Info className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" aria-hidden="true" />
              <span>
                Running <code>litepsm</code> discovers the host config, backs it up, and injects this single bridge entry safely.
              </span>
            </div>
          </div>
        </section>

        {related.length > 0 && (
          <section className="space-y-4">
            <h2 className="text-base font-bold text-slate-900">Related in {item.category}</h2>
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              {related.map((r) => (
                <Link
                  key={r.id}
                  href={`/package/?slug=${encodeURIComponent(r.slug)}`}
                  className="group block rounded-2xl border border-slate-200/80 bg-white p-4 shadow-sm transition-all hover:border-slate-300 hover:shadow-md"
                >
                  <div className="mb-2 flex items-center justify-between text-xs">
                    <span className="font-mono text-[10px] uppercase text-slate-500">{kindLabel(r.kind)}</span>
                    <span className="font-mono text-xs text-amber-600">★ {formatStars(r.stars)}</span>
                  </div>
                  <h3 className="line-clamp-1 text-sm font-bold text-slate-900 transition-colors group-hover:text-emerald-600">
                    {r.name}
                  </h3>
                  <p className="mt-1 line-clamp-2 text-xs text-slate-500">{r.summary}</p>
                </Link>
              ))}
            </div>
          </section>
        )}
      </main>
    </div>
  );
}

export default function PackagePage() {
  return (
    <Suspense fallback={<div className="p-12 text-center font-mono text-slate-500">Loading package...</div>}>
      <PackageContent />
    </Suspense>
  );
}
