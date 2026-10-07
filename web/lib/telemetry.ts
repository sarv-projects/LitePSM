"use client";

// Catalog telemetry + client-side search model shared across the Market UI.

import { useEffect, useMemo, useState } from "react";
import bundledRelease from "../data/release.json";

export interface Listing {
  id: string;
  name: string;
  slug: string;
  kind: "mcp" | "skill" | "plugin";
  summary: string;
  category: string;
  /**
   * `verified` is true only for rows read from the publisher's own manifest
   * (`provenance: "vendor-manifest"`); list-sourced rows are "awesome-list-claim".
   */
  publisher: { name: string; verified: boolean; provenance?: string; url?: string; avatarUrl?: string };
  /** discovery_only rows have no proven version, command or transport. */
  installability?: "discovery_only" | "metadata_verified" | "runtime_verified" | "litespm_tested";
  transport?: string;
  runtime?: string;
  /**
   * Always null in this dataset. The upstream sources expose no
   * machine-readable star, download or install counts, so the builder publishes
   * none. Kept in the type because the field exists in the wire format and a
   * future source may legitimately populate it.
   */
  stars: number | null;
  version: string | null;
  /**
   * Publisher-declared host list. Only plugins carry one. MCP servers and
   * skills derive their compatibility from the kind (see lib/hosts.ts
   * `hostsFor`), because an MCP server is installable into every bridge
   * adapter and a skill into every host with a skills directory.
   */
  compatibleHosts?: string[];
  readme?: string;
  command?: string | null;
  args?: string[] | null;
  skillSource?: string;
  installHint?: string;
  schemaFingerprint?: string;
  tools?: Array<{ name: string; description: string; inputSchema?: unknown }>;
  effects?: Array<{ effect: string; declaredBy: string }>;
  /**
   * Derived resolution key for `/package/?slug=<key>` — never present in the
   * wire format. `slug` is not unique, so the key is the slug only where it is
   * unambiguous and the id otherwise (`withLinkKeys` in lib/catalog). Rows are
   * keyed here rather than re-deriving global slug counts in every component,
   * so a row carries its own link target across the server → client boundary.
   */
  linkKey?: string;
}

export interface Telemetry {
  itemCount: number;
  createdAt?: string;
  releaseId?: string;
  sequence?: number;
  manifestDigest?: string;
  counts: { all: number; mcp: number; skill: number; plugin: number };
}

export type TelemetryState =
  | { status: "loading"; data: Telemetry }
  | { status: "ready"; data: Telemetry }
  | { status: "offline"; data: Telemetry };

/**
 * The release manifest is bundled at build time, so the STATIC html can state
 * real release facts instead of a "checking release manifest…" placeholder that
 * only resolves once JavaScript runs. A crawler, a no-JS reader, or anyone
 * reading view-source previously saw the placeholder and nothing else.
 *
 * The runtime fetch below still runs, so a manifest published after this build
 * is picked up without a redeploy.
 */
function bundledTelemetry(): Telemetry {
  const raw = bundledRelease as Record<string, unknown>;
  const itemCount = Number(raw.itemCount ?? raw.totalCapabilities ?? 0) || 0;
  return {
    itemCount,
    createdAt: typeof raw.createdAt === "string" ? raw.createdAt : undefined,
    releaseId: typeof raw.releaseId === "string" ? raw.releaseId : undefined,
    sequence: typeof raw.sequence === "number" ? raw.sequence : undefined,
    manifestDigest: typeof raw.manifestDigest === "string" ? raw.manifestDigest : undefined,
    counts: {
      all: itemCount,
      mcp: Number(raw.mcpServersCount ?? 0) || 0,
      skill: Number(raw.agentSkillsCount ?? 0) || 0,
      plugin: Number(raw.pluginsCount ?? 0) || 0,
    },
  };
}

/**
 * Read `/v1/current.json` and report where the numbers on screen came from.
 *
 * The initial state is **`loading`**, not `ready`: the bundled manifest proves
 * what this build shipped, it does not prove the origin still serves it. Until
 * the fetch answers, the UI says "checking" rather than showing a "Live" badge
 * that has checked nothing — and it falls back to `offline`, with the bundled
 * snapshot, when the request fails. The `data` payload is the bundled manifest
 * from the first render either way, so the counts are never a zero placeholder.
 *
 * `counts` are the per-kind totals the page already holds — they arrive as
 * props from the prerender rather than as a full row set, so this hook does not
 * need (and must not be given) the 5,825-row catalog just to compare two
 * numbers. Deps are the primitives, so a fresh object identity per render
 * cannot re-trigger the fetch.
 */
export function useTelemetry(counts: Telemetry["counts"]): TelemetryState {
  const { all: allCount, mcp: mcpCount, skill: skillCount, plugin: pluginCount } = counts;
  const derived = useMemo(
    () => ({ all: allCount, mcp: mcpCount, skill: skillCount, plugin: pluginCount }),
    [allCount, mcpCount, skillCount, pluginCount]
  );

  // Seed from the bundled manifest so server-rendered html carries real facts,
  // but with an unverified status: the numbers are known, the liveness is not.
  const [state, setState] = useState<TelemetryState>(() => ({
    status: "loading",
    data: bundledTelemetry(),
  }));

  useEffect(() => {
    let cancelled = false;
    fetch("/v1/current.json", { cache: "no-store" })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((raw: Record<string, unknown>) => {
        if (cancelled) return;
        const itemCount = Number(raw.itemCount ?? raw.totalCapabilities ?? derived.all) || derived.all;
        setState({
          status: "ready",
          data: {
            itemCount,
            createdAt: typeof raw.createdAt === "string" ? raw.createdAt : undefined,
            releaseId: typeof raw.releaseId === "string" ? raw.releaseId : undefined,
            sequence: typeof raw.sequence === "number" ? raw.sequence : undefined,
            manifestDigest:
              typeof raw.manifestDigest === "string" ? raw.manifestDigest : undefined,
            counts: {
              all: itemCount,
              mcp: Number(raw.mcpServersCount ?? derived.mcp) || derived.mcp,
              skill: Number(raw.agentSkillsCount ?? derived.skill) || derived.skill,
              plugin: Number(raw.pluginsCount ?? derived.plugin) || derived.plugin,
            },
          },
        });
      })
      .catch(() => {
        if (cancelled) return;
        // Offline: the bundled snapshot is what is on screen. Keep its release
        // stamp (the build really shipped it) and its own item count, and say
        // `offline` instead of pretending the origin answered.
        setState({
          status: "offline",
          data: { ...bundledTelemetry(), itemCount: derived.all, counts: derived },
        });
      });
    return () => {
      cancelled = true;
    };
  }, [derived]);

  return state;
}

/** Client-side lexical search across the fields the catalog actually exposes. */
export function matchesQuery(item: Listing, q: string): boolean {
  const query = q.toLowerCase().trim();
  if (!query) return true;
  const haystack = [
    item.name,
    item.summary,
    item.category,
    item.publisher?.name,
    item.id,
    item.slug,
    item.runtime,
    item.transport,
    ...(item.tools || []).map((t) => `${t.name} ${t.description}`),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return query.split(/\s+/).every((token) => haystack.includes(token));
}
