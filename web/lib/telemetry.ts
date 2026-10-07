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

function deriveCounts(listings: Listing[]) {
  let mcp = 0;
  let skill = 0;
  let plugin = 0;
  for (const l of listings) {
    if (l.kind === "mcp") mcp += 1;
    else if (l.kind === "skill") skill += 1;
    else if (l.kind === "plugin") plugin += 1;
  }
  return { all: listings.length, mcp, skill, plugin };
}

/**
 * Fetch /v1/current.json for live counts. Falls back to data-derived counts and
 * reports status so the UI never shows a fake "live" indicator when offline.
 */
export function useTelemetry(listings: Listing[]): TelemetryState {
  const derived = useMemo(() => deriveCounts(listings), [listings]);
  // Seed from the bundled manifest so server-rendered html carries real facts.
  const [state, setState] = useState<TelemetryState>(() => ({
    status: "ready",
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
        setState({ status: "offline", data: { itemCount: derived.all, counts: derived } });
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

/** Distinct categories with counts, ordered by frequency (then name). */
export function categoryFacets(listings: Listing[], limit = 16): Array<{ name: string; count: number }> {
  const counts = new Map<string, number>();
  for (const l of listings) {
    const key = l.category?.trim();
    if (!key) continue;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return Array.from(counts, ([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, limit);
}
