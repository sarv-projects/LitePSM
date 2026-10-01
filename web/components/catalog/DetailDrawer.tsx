"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import {
  X,
  Copy,
  ShieldCheck,
  Star,
  FileText,
  Code2,
  Shield,
  Terminal,
  Info,
} from "lucide-react";
import { Listing } from "../../lib/telemetry";
import { HOSTS, bridgeSnippet, nativeSnippet, PlatformOS } from "../../lib/hosts";
import { copyText } from "../../lib/clipboard";
import { formatStars } from "../../lib/format";

interface DetailDrawerProps {
  item: Listing | null;
  onClose: () => void;
}

type TabId = "overview" | "schema" | "security" | "connect";

const TABS: Array<{ id: TabId; label: string; icon: React.ElementType }> = [
  { id: "overview", label: "Overview", icon: FileText },
  { id: "schema", label: "Tools & Schema", icon: Code2 },
  { id: "security", label: "Security & Effects", icon: Shield },
  { id: "connect", label: "Host Connect", icon: Terminal },
];

export function DetailDrawer({ item, onClose }: DetailDrawerProps) {
  const [activeTab, setActiveTab] = useState<TabId>("overview");
  const [hostId, setHostId] = useState<string>("claude-code");
  const [os, setOs] = useState<PlatformOS>("linux");
  const [mode, setMode] = useState<"bridge" | "native">("bridge");
  const panelRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!item) return;
    const previous = document.activeElement as HTMLElement | null;
    closeRef.current?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key !== "Tab") return;
      // Focus trap within the drawer panel.
      const root = panelRef.current;
      if (!root) return;
      const focusables = root.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"]), select, input, textarea'
      );
      if (focusables.length === 0) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };

    const ua = typeof navigator !== "undefined" ? navigator.userAgent.toLowerCase() : "";
    setOs(ua.includes("mac") ? "mac" : ua.includes("linux") ? "linux" : "win");

    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      previous?.focus();
    };
  }, [item, onClose]);

  const host = HOSTS.find((h) => h.id === hostId) ?? HOSTS[0];

  const snippet = useCallback(
    () =>
      item
        ? mode === "bridge"
          ? bridgeSnippet(host)
          : nativeSnippet(host, item.slug, item.command, item.args)
        : "",
    [item, mode, host]
  );

  const onCopySnippet = () => copyText(snippet(), "Configuration copied");
  const onCopyInstall = () => item && copyText(`litepsm install ${item.id}`, "Install command copied");

  if (!item) return null;

  const hasTools = Array.isArray(item.tools) && item.tools.length > 0;
  const hasEffects = Array.isArray(item.effects) && item.effects.length > 0;

  return (
    <div
      className="fixed inset-0 z-50 flex justify-end bg-slate-900/40 backdrop-blur-sm drawer-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="drawer-title"
        className="drawer-panel flex h-full w-full max-w-2xl flex-col overflow-hidden border-l border-slate-200 bg-white shadow-2xl"
      >
        {/* Header */}
        <div className="flex items-start justify-between gap-4 border-b border-slate-200 p-6">
          <div className="min-w-0">
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <span className="rounded border border-emerald-200 bg-emerald-50 px-2 py-0.5 font-mono text-[11px] uppercase text-emerald-700">
                {item.kind}
              </span>
              <span className="font-mono text-xs text-slate-500">v{item.version}</span>
              <span className="flex items-center gap-1 rounded border border-amber-200 bg-amber-50 px-2 py-0.5 font-mono text-xs text-amber-700">
                <Star className="h-3 w-3 fill-amber-500 text-amber-500" aria-hidden="true" />
                {formatStars(item.stars)}
              </span>
            </div>
            <h2 id="drawer-title" className="truncate text-xl font-bold text-slate-900">
              {item.name}
            </h2>
            <p className="mt-1 text-xs text-slate-500">
              Publisher: <span className="font-medium text-slate-700">{item.publisher?.name || "unknown"}</span>
              {item.publisher?.verified && (
                <span className="ml-1 inline-flex items-center text-xs text-emerald-600">
                  <ShieldCheck className="mr-0.5 inline h-3.5 w-3.5" aria-hidden="true" /> Verified
                </span>
              )}
            </p>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="Close details"
            className="rounded-lg p-2 text-slate-400 transition-all hover:bg-slate-100 hover:text-slate-900 focus:outline-none focus:ring-2 focus:ring-emerald-500/50"
          >
            <X className="h-5 w-5" aria-hidden="true" />
          </button>
        </div>

        {/* Tabs */}
        <div role="tablist" aria-label="Capability details" className="flex items-center gap-1 border-b border-slate-200 bg-slate-50 px-4">
          {TABS.map((t) => {
            const Icon = t.icon;
            const selected = activeTab === t.id;
            return (
              <button
                key={t.id}
                role="tab"
                id={`tab-${t.id}`}
                aria-selected={selected}
                aria-controls={`panel-${t.id}`}
                tabIndex={selected ? 0 : -1}
                onClick={() => setActiveTab(t.id)}
                className={`flex items-center gap-2 border-b-2 px-4 py-3 text-xs font-medium transition-all ${
                  selected
                    ? "border-emerald-500 font-semibold text-emerald-700"
                    : "border-transparent text-slate-500 hover:text-slate-800"
                }`}
              >
                <Icon className="h-3.5 w-3.5" aria-hidden="true" />
                {t.label}
              </button>
            );
          })}
        </div>

        {/* Body */}
        <div className="flex-1 space-y-4 overflow-y-auto p-6 text-sm text-slate-700">
          {activeTab === "overview" && (
            <div
              role="tabpanel"
              id="panel-overview"
              aria-labelledby="tab-overview"
              className="space-y-4"
            >
              <div className="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">Description</h4>
                <p className="text-slate-700">{item.summary}</p>
              </div>
              {item.readme ? (
                <div className="rounded-xl border border-slate-200 bg-slate-50 p-4">
                  <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">Documentation</h4>
                  <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-slate-200 bg-white p-3 font-mono text-[11px] text-slate-700">
                    {item.readme}
                  </pre>
                </div>
              ) : (
                <p className="flex items-start gap-2 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
                  <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                  No extended README is published for this listing. Install it with the command below to read the
                  upstream documentation.
                </p>
              )}
            </div>
          )}

          {activeTab === "schema" && (
            <div
              role="tabpanel"
              id="panel-schema"
              aria-labelledby="tab-schema"
              className="space-y-4"
            >
              <div className="rounded-xl border border-slate-200 bg-slate-50 p-3.5">
                <span className="mb-1 block font-mono text-[10px] uppercase tracking-wider text-slate-500">
                  Canonical Schema Fingerprint (SHA-256)
                </span>
                <code className="break-all font-mono text-xs text-emerald-700">
                  {item.schemaFingerprint || "Not published in this catalog release"}
                </code>
              </div>
              {hasTools ? (
                <div className="space-y-3">
                  <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500">
                    Exposed Tool Functions ({item.tools!.length})
                  </h4>
                  {item.tools!.map((t) => (
                    <div key={t.name} className="space-y-2 rounded-xl border border-slate-200 bg-slate-50 p-4">
                      <span className="font-mono text-sm font-semibold text-slate-900">{t.name}</span>
                      <p className="text-xs text-slate-500">{t.description}</p>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-slate-500">
                  Tool schemas are not included in this release. The daemon computes a schema fingerprint on first
                  connection and binds approvals to it.
                </p>
              )}
            </div>
          )}

          {activeTab === "security" && (
            <div
              role="tabpanel"
              id="panel-security"
              aria-labelledby="tab-security"
              className="space-y-4"
            >
              <div className="flex items-start gap-3 rounded-xl border border-emerald-200 bg-emerald-50 p-4">
                <ShieldCheck className="mt-0.5 h-5 w-5 shrink-0 text-emerald-600" aria-hidden="true" />
                <div className="text-xs">
                  <span className="mb-0.5 block font-semibold text-slate-900">Fail-Closed Policy Enforcement</span>
                  <p className="text-slate-600">
                    Declared effects pass through 5-tier evaluation before execution. Unapproved actions prompt the
                    user; model output can never self-authorize.
                  </p>
                </div>
              </div>
              {hasEffects ? (
                <div className="space-y-2">
                  <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500">
                    Declared Effects
                  </h4>
                  {item.effects!.map((eff, idx) => (
                    <div
                      key={`${eff.effect}-${idx}`}
                      className="flex items-center justify-between rounded-xl border border-slate-200 bg-slate-50 p-3"
                    >
                      <code className="font-mono text-xs text-cyan-700">{eff.effect}</code>
                      <span className="rounded border border-slate-200 bg-white px-2 py-0.5 font-mono text-[10px] text-slate-500">
                        {eff.declaredBy}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-slate-500">
                  No effect declarations are published for this listing. Unknown-effect tools fail closed and require
                  explicit per-call authorization.
                </p>
              )}
            </div>
          )}

          {activeTab === "connect" && (
            <div
              role="tabpanel"
              id="panel-connect"
              aria-labelledby="tab-connect"
              className="space-y-4"
            >
              <div className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-2 text-xs font-semibold text-slate-600">
                  Host
                  <select
                    value={hostId}
                    onChange={(e) => setHostId(e.target.value)}
                    className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-800 focus:border-emerald-500 focus:outline-none"
                  >
                    {HOSTS.map((h) => (
                      <option key={h.id} value={h.id}>
                        {h.name}
                      </option>
                    ))}
                  </select>
                </label>

                <label className="flex items-center gap-2 text-xs font-semibold text-slate-600">
                  OS
                  <select
                    value={os}
                    onChange={(e) => setOs(e.target.value as PlatformOS)}
                    className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-800 focus:border-emerald-500 focus:outline-none"
                  >
                    <option value="win">Windows</option>
                    <option value="mac">macOS</option>
                    <option value="linux">Linux</option>
                  </select>
                </label>

                <div className="flex items-center gap-1 rounded-xl border border-slate-200 bg-slate-100 p-1 text-xs">
                  <button
                    type="button"
                    onClick={() => setMode("bridge")}
                    aria-pressed={mode === "bridge"}
                    className={`rounded-lg px-3 py-1 transition-all ${
                      mode === "bridge" ? "bg-emerald-600 font-semibold text-white" : "text-slate-600 hover:text-slate-900"
                    }`}
                  >
                    LitePSM Bridge
                  </button>
                  <button
                    type="button"
                    onClick={() => setMode("native")}
                    aria-pressed={mode === "native"}
                    className={`rounded-lg px-3 py-1 transition-all ${
                      mode === "native" ? "bg-slate-800 font-semibold text-white" : "text-slate-600 hover:text-slate-900"
                    }`}
                  >
                    Direct Native
                  </button>
                </div>
              </div>

              <div className="rounded-xl border border-slate-200 bg-slate-50 p-3">
                <span className="mb-1 block font-mono text-[10px] uppercase tracking-wider text-slate-500">
                  Target Config File
                </span>
                <code className="break-all font-mono text-xs text-slate-700">{host.paths[os]}</code>
              </div>

              <div className="relative">
                <pre className="overflow-x-auto whitespace-pre-wrap rounded-xl border border-slate-800 bg-[#0d1117] p-4 font-mono text-xs text-emerald-300">
                  {snippet()}
                </pre>
                <button
                  type="button"
                  onClick={onCopySnippet}
                  className="absolute right-3 top-3 flex items-center gap-1 rounded-lg border border-slate-700 bg-[#21262d] px-2.5 py-1.5 text-xs text-slate-200 transition-all hover:bg-emerald-500 hover:text-slate-950"
                >
                  <Copy className="h-3.5 w-3.5" aria-hidden="true" />
                  <span>Copy Config</span>
                </button>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between gap-3 border-t border-slate-200 bg-slate-50 p-4">
          <Link
            href={`/item/?id=${encodeURIComponent(item.id)}`}
            className="truncate font-mono text-xs text-slate-500 underline underline-offset-4 hover:text-emerald-600"
          >
            Open full page
          </Link>
          <button
            type="button"
            onClick={onCopyInstall}
            className="flex items-center gap-2 rounded-xl bg-emerald-500 px-4 py-2 text-xs font-semibold text-black shadow-lg shadow-emerald-500/20 transition-all hover:bg-emerald-400"
          >
            <Copy className="h-3.5 w-3.5" aria-hidden="true" />
            Copy Install Command
          </button>
        </div>
      </div>
    </div>
  );
}
