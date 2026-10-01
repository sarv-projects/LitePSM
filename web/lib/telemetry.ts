"use client";

// Catalog telemetry + client-side search model shared across the Market UI.

import { useEffect, useMemo, useState } from "react";

export interface Listing {
  id: string;
  name: string;
  slug: string;
  kind: "mcp" | "skill" | "plugin";
  summary: string;
  category: string;
  publisher: { name: string; verified: boolean; url?: string; avatarUrl?: string };
  transport?: string;
  runtime?: string;
  stars: number;
  version: string;
  testedHosts: string[];
  readme?: string;
  command?: string;
  args?: string[];
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

const EMPTY: Telemetry = {
  itemCount: 0,
  counts: { all: 0, mcp: 0, skill: 0, plugin: 0 },
};

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
  const [state, setState] = useState<TelemetryState>({ status: "loading", data: EMPTY });

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
