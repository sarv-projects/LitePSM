"use client";

import React, { useDeferredValue, useMemo, useState, useEffect } from "react";
import { Header } from "../components/navigation/Header";
import { SiteFooter } from "../components/layout/SiteFooter";
import { LandingHero } from "../components/home/LandingHero";
import { AgentStrip } from "../components/home/AgentStrip";
import { TopicTiles } from "../components/home/TopicTiles";
import { EcosystemSection } from "../components/home/EcosystemSection";
import { StartSection } from "../components/home/StartSection";
import { HowItWorks } from "../components/home/HowItWorks";
import { AtlasTeaser } from "../components/home/AtlasTeaser";
import { TrustStrip } from "../components/home/TrustStrip";
import { DeveloperZone } from "../components/home/DeveloperZone";
import { SearchResults } from "../components/home/SearchResults";
import { Listing, useTelemetry } from "../lib/telemetry";
import { useCatalog } from "../lib/catalogData";
import { useCatalogSearch } from "../lib/useCatalogSearch";
import { deriveKindCounts, hostUniverse } from "../lib/catalog";
import { AGENT_CHOICES, findAgentChoice, rowWorksWithAgent } from "../lib/landing";
import type { ProvenanceStats, TopicStat } from "../lib/landing";

/**
 * The landing page. Every field here is computed by `app/page.tsx` — a Server
 * Component — from `data/catalog.json` at build time, and serialized into the
 * prerendered HTML. That is what keeps the dataset out of this module: nothing
 * here imports the rows, so hydration runs off the props alone and the reader
 * sees the whole page before any dataset request happens.
 *
 * Exactly one interaction asks for the full row set: typing in the hero
 * search. Everything else — the agent preference, the topic tiles, the kind
 * cards, the curated grid — runs off the server slice or off `hostsFor`, which
 * is a property of the row's kind. A passive visit therefore fetches
 * `/v1/current.json` (release liveness, bundled-snapshot fallback) and nothing
 * else; `/data/catalog.json` waits for a keystroke.
 */
export interface HomeViewProps {
  total: number;
  kindCounts: { all: number; mcp: number; skill: number; plugin: number };
  hostCount: number;
  /** Compiled-in bridge adapters (data/hosts.json), for the agents card. */
  adapterCount: number;
  provenance: ProvenanceStats;
  topics: TopicStat[];
  /** The curated "Good places to start" rows, resolved at build time. */
  start: Listing[];
  officialCount: number;
  vendorCount: number;
}

const AGENT_STORAGE_KEY = "litespm-agent";

export default function Home(props: HomeViewProps) {
  const { status, items: full, request } = useCatalog();
  const [searchQuery, setSearchQuery] = useState("");
  const deferredQuery = useDeferredValue(searchQuery);

  const [agentId, setAgentId] = useState<string | null>(null);
  useEffect(() => {
    try {
      const stored = window.localStorage.getItem(AGENT_STORAGE_KEY);
      if (stored && AGENT_CHOICES.some((c) => c.id === stored)) setAgentId(stored);
    } catch {
      // Storage denied: the strip still works, it just forgets on reload.
    }
  }, []);

  const changeAgent = (id: string) => {
    setAgentId(id);
    try {
      window.localStorage.setItem(AGENT_STORAGE_KEY, id);
    } catch {
      // Best-effort persistence.
    }
  };

  const live = useMemo(() => {
    if (!full) return null;
    return {
      total: full.length,
      kindCounts: deriveKindCounts(full),
      hostCount: hostUniverse(full).length,
    };
  }, [full]);

  const total = live ? live.total : props.total;
  const kindCounts = live ? live.kindCounts : props.kindCounts;
  const hostCount = live ? live.hostCount : props.hostCount;
  const telemetry = useTelemetry(kindCounts);

  const setQuery = (q: string) => {
    // The one fetch on this route: a reader asking for rows.
    if (q.trim() !== "") request();
    setSearchQuery(q);
  };

  const { results, searching } = useCatalogSearch(full ?? [], deferredQuery, {
    kind: null,
    category: null,
  });

  const agent = findAgentChoice(agentId);

  // Counts are only honest over rows that are actually loaded. Until the
  // snapshot lands the strip speaks qualitatively rather than printing a
  // number derived from twelve curated cards.
  const agentCounts = useMemo(() => {
    if (!full) return null;
    return AGENT_CHOICES.map((choice) => ({
      id: choice.id,
      count: full.filter((item) => rowWorksWithAgent(item, choice)).length,
    }));
  }, [full]);

  const hasQuery = searchQuery.trim() !== "";
  const awaiting = status === "loading";
  const error = status === "error";

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="flex-1">
        <LandingHero
          total={total}
          kindCounts={kindCounts}
          hostCount={hostCount}
          telemetry={telemetry}
          query={searchQuery}
          setQuery={setQuery}
          totalMatches={deferredQuery && full ? results.length : undefined}
        />

        {hasQuery ? (
          <SearchResults
            items={full ? results : []}
            query={searchQuery}
            count={full ? results.length : undefined}
            loading={searching}
            awaiting={awaiting}
            error={error}
            onRetry={request}
            onClear={() => setSearchQuery("")}
            agent={agent}
            total={total}
          />
        ) : (
          <>
            <AgentStrip
              choice={agent}
              onChange={changeAgent}
              counts={agentCounts}
              total={total}
            />
            <TopicTiles topics={props.topics} />
            <EcosystemSection
              kindCounts={kindCounts}
              hostCount={hostCount}
              adapterCount={props.adapterCount}
            />
            <StartSection
              items={props.start}
              officialCount={props.officialCount}
              vendorCount={props.vendorCount}
              agent={agent}
            />
          </>
        )}

        <HowItWorks />
        <AtlasTeaser />
        <TrustStrip provenance={props.provenance} />
        <DeveloperZone />
      </main>

      <SiteFooter />
    </div>
  );
}
