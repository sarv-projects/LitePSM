// Landing-page structures: the "what do you want your AI to do?" taxonomy, the
// "what do you use?" agent strip, the honest selection rule behind "good places
// to start", and the provenance counts the trust section prints.
//
// Everything here is a pure function over a row set handed in from above, or a
// table of constants. No JSON import, no `"use client"`, no fetch: the Server
// Component in `app/page.tsx` calls the derivations at build time so the counts
// are in the prerendered HTML, and the client calls `rowWorksWithAgent` over
// rows it already has. Nothing in this module can ask for the dataset.
//
// The topic table maps a hand-written friendly label onto a REAL Explore
// parameter: either an exact `?category=` value from `data/catalog.json` (63
// categories, matched case-sensitively by `useCatalogSearch`) or a `?q=` term
// matched by the same `matchesQuery` the Explore results use. A label that
// cannot name an honest category says so by using `q` — it never invents a slug.

import type { Listing } from "./telemetry";
import { matchesQuery } from "./query";
import { HOSTS, SKILL_TARGETS, hostsFor } from "./hosts";

export interface Topic {
  id: string;
  label: string;
  blurb: string;
  /** Exact `?category=` value Explore accepts, when the dataset has one. */
  category?: string;
  /** `?q=` term, for topics the category vocabulary does not cover. */
  query?: string;
  /**
   * Preferred example names, in display order. Verified against the row set at
   * build time: a name that is not actually in the topic's result set is
   * dropped and the gap is backfilled from real rows, so a regenerated dataset
   * cannot put a stranger's name on a tile that no longer matches it.
   */
  examples: string[];
}

export const TOPICS: Topic[] = [
  {
    id: "code",
    label: "Code & Git",
    blurb: "Repositories, reviews, CI and the languages your agent writes.",
    category: "Developer Tools",
    examples: ["github", "circleci", "context7"],
  },
  {
    id: "browser",
    label: "Browser",
    blurb: "Drive a real browser: navigate, scrape, screenshot, test.",
    category: "Browser Automation",
    examples: ["playwright-mcp", "chrome-mcp-secure", "browser-use-mcp-server"],
  },
  {
    id: "files",
    label: "Files & Documents",
    blurb: "Read and write docs, PDFs, spreadsheets and notes.",
    query: "document",
    examples: ["notion", "google-docs", "docx-mcp"],
  },
  {
    id: "databases",
    label: "Databases",
    blurb: "Query, migrate and reason about the data your app keeps.",
    category: "Databases",
    examples: ["mongodb", "supabase", "clickhouse"],
  },
  {
    id: "cloud",
    label: "Cloud & DevOps",
    blurb: "Deployments, infrastructure and the pipelines around them.",
    category: "Cloud Infrastructure",
    examples: ["cloudflare", "vercel", "deploy-on-aws"],
  },
  {
    id: "design",
    label: "Design",
    blurb: "Design files, image work and the creative tools in between.",
    category: "Multimedia",
    examples: ["figma", "canva", "Adobe"],
  },
  {
    id: "research",
    label: "Research & Search",
    blurb: "Web search, papers, citations and evidence gathering.",
    category: "Search & Retrieval",
    examples: ["zotero", "brave-search-mcp", "arxiv-mcp-server"],
  },
  {
    id: "communication",
    label: "Communication",
    blurb: "Mail, chat, meetings and the inboxes you already live in.",
    category: "Communication",
    examples: ["slack", "gmail", "zoom"],
  },
  {
    id: "productivity",
    label: "Productivity",
    blurb: "Tasks, calendars, docs and the workflow around your team.",
    category: "Productivity & Workflow",
    examples: ["asana", "clickup", "linear"],
  },
  {
    id: "knowledge",
    label: "Knowledge & Memory",
    blurb: "Give an agent durable notes it can recall later.",
    category: "Knowledge & Memory",
    examples: ["apple-notes-mcp", "basic-memory", "obsidian-mcp"],
  },
  {
    id: "security",
    label: "Security & Testing",
    blurb: "Scans, test runners and the checks that catch regressions.",
    category: "Security & Testing",
    examples: ["auth0", "semgrep", "sonarqube"],
  },
];

export interface TopicStat extends Topic {
  count: number;
  examples: string[];
  href: string;
}

function topicPredicate(topic: Topic): (item: Listing) => boolean {
  if (topic.category) return (item) => item.category === topic.category;
  const q = topic.query ?? "";
  return (item) => matchesQuery(item, q);
}

function topicHref(topic: Topic): string {
  if (topic.category) return `/explore/?category=${encodeURIComponent(topic.category)}`;
  return `/explore/?q=${encodeURIComponent(topic.query ?? "")}`;
}

/**
 * Count and fill each topic against the full row set. Runs at build time in
 * `app/page.tsx`, so the numbers beside the tiles are the same numbers Explore
 * will report after it fetches — both come from this predicate over these rows.
 */
export function deriveTopics(items: Listing[]): TopicStat[] {
  return TOPICS.map((topic) => {
    const matches = items.filter(topicPredicate(topic));
    const present = new Set(matches.map((m) => m.name));
    const examples: string[] = [];
    for (const name of topic.examples) {
      if (present.has(name) && examples.length < 3) examples.push(name);
    }
    for (const item of matches) {
      if (examples.length >= 3) break;
      if (!examples.includes(item.name)) examples.push(item.name);
    }
    return { ...topic, count: matches.length, examples, href: topicHref(topic) };
  });
}

/* ------------------------------------------------------------------ *
 * "What do you use?"
 * ------------------------------------------------------------------ */

export interface AgentChoice {
  /** Value persisted in localStorage. */
  id: string;
  label: string;
  /**
   * The names this agent answers to inside `hostsFor(item)`. Derived from the
   * two generated registries rather than typed out, so an adapter rename
   * cannot leave the strip pointing at a host that no longer exists.
   *
   * An MCP row names the bridge adapter ("Claude Code CLI"), a skill row names
   * the skill target ("Claude Code"), and a plugin row carries whatever its
   * publisher declared. Matching both spellings is the whole point: one agent,
   * three sources of truth.
   */
  hosts: string[];
}

const AGENT_HOST_IDS: Array<{ id: string; label: string }> = [
  { id: "claude-code", label: "Claude Code" },
  { id: "codex", label: "Codex" },
  { id: "opencode", label: "OpenCode" },
  { id: "cursor", label: "Cursor" },
  { id: "cline", label: "Cline" },
  { id: "roo", label: "Roo Code" },
];

export const AGENT_CHOICES: AgentChoice[] = [
  ...AGENT_HOST_IDS.map(({ id, label }) => {
    const names = [
      HOSTS.find((h) => h.id === id)?.name,
      SKILL_TARGETS.find((t) => t.id === id)?.displayName,
    ].filter((n): n is string => Boolean(n));
    return { id, label, hosts: names };
  }),
  // No compatibility claim attaches to these two: the reader told us nothing
  // the catalog can check, so they carry an empty host set and no badges.
  { id: "other", label: "Other", hosts: [] },
  { id: "unsure", label: "I'm not sure", hosts: [] },
];

export function findAgentChoice(id: string | null): AgentChoice | null {
  if (!id) return null;
  return AGENT_CHOICES.find((c) => c.id === id) ?? null;
}

/**
 * Does this row declare support for the chosen agent?
 *
 * This is the catalog's own declared semantics, no stronger:
 * `hostsFor` returns every bridge adapter for an MCP server, every skill
 * directory for a skill, and the publisher's own list for a plugin. It is a
 * declaration, never a test result — the UI says so next to the badges.
 *
 * Pure over rows the caller already holds. It cannot fetch anything.
 */
export function rowWorksWithAgent(item: Listing, choice: AgentChoice | null): boolean {
  if (!choice || choice.hosts.length === 0) return false;
  const wanted = choice.hosts.map((n) => n.toLowerCase());
  return hostsFor(item).some((h) => wanted.includes(h.toLowerCase()));
}

/* ------------------------------------------------------------------ *
 * Good places to start — one honest selection rule, stated out loud
 * ------------------------------------------------------------------ */

/** The catalog's own category for skills published by the organization itself. */
export const OFFICIAL_SKILLS_CATEGORY = "Official Skills by";

/**
 * Two sources, both of which the catalog can actually filter on — and both of
 * which are a fact about where a row came from, not about how popular it is:
 *
 *   1. `category = "Official Skills by"` — skills the publisher itself ships.
 *   2. `publisher.verified` (`provenance = "vendor-manifest"`) — read out of the
 *      publisher's own repository manifest rather than a third-party list.
 *
 * No star, download or install figure exists in the dataset, so none of this
 * can be, or is presented as, a ranking.
 */
const START_PREFERRED: string[] = [
  // category 1: org-published skills
  "Docx",
  "Frontend Design",
  "Cloudflare",
  "Gemini Api Dev",
  "Building Secure Contracts",
  "Clickhouse Best Practices",
  // category 2: publisher's own marketplace manifest
  "figma",
  "mongodb",
  "notion",
  "sentry",
  "canva",
  "supabase",
];

export const START_SIZE = 12;

export function deriveStart(items: Listing[]): Listing[] {
  const inRule = items.filter(
    (item) => item.category === OFFICIAL_SKILLS_CATEGORY || item.publisher?.verified === true
  );
  const byName = new Map<string, Listing[]>();
  for (const item of inRule) {
    const bucket = byName.get(item.name);
    if (bucket) bucket.push(item);
    else byName.set(item.name, [item]);
  }

  const chosen: Listing[] = [];
  const taken = new Set<string>();
  for (const name of START_PREFERRED) {
    if (chosen.length >= START_SIZE) break;
    const bucket = byName.get(name);
    if (bucket && bucket.length > 0 && !taken.has(name)) {
      chosen.push(bucket[0]);
      taken.add(name);
    }
  }
  // Backfill in index order (verified publishers first, then kind, then name —
  // the builder's own deterministic order) if a regenerated dataset dropped a
  // preferred name.
  for (const item of inRule) {
    if (chosen.length >= START_SIZE) break;
    if (taken.has(item.name)) continue;
    chosen.push(item);
    taken.add(item.name);
  }

  // Interleave the two sources so the grid is not six of one kind in a row.
  const skills = chosen.filter((c) => c.kind === "skill");
  const rest = chosen.filter((c) => c.kind !== "skill");
  const out: Listing[] = [];
  for (let i = 0; i < Math.max(skills.length, rest.length); i += 1) {
    if (skills[i]) out.push(skills[i]);
    if (rest[i]) out.push(rest[i]);
  }
  return out;
}

/* ------------------------------------------------------------------ *
 * Provenance — what the trust section is allowed to claim
 * ------------------------------------------------------------------ */

export interface ProvenanceStats {
  total: number;
  /** Rows read from the publisher's own repository manifest. */
  vendorManifest: number;
  /** Rows claimed by a third-party awesome-list. */
  awesomeList: number;
  /** `installability` values, exactly as the wire format publishes them. */
  discoveryOnly: number;
  metadataVerified: number;
  runtimeVerified: number;
  litespmTested: number;
  /** Rows that carry a proven version string. */
  versioned: number;
}

export function deriveProvenance(items: Listing[]): ProvenanceStats {
  const stats: ProvenanceStats = {
    total: items.length,
    vendorManifest: 0,
    awesomeList: 0,
    discoveryOnly: 0,
    metadataVerified: 0,
    runtimeVerified: 0,
    litespmTested: 0,
    versioned: 0,
  };
  for (const item of items) {
    if (item.publisher?.verified) stats.vendorManifest += 1;
    else stats.awesomeList += 1;
    switch (item.installability) {
      case "metadata_verified":
        stats.metadataVerified += 1;
        break;
      case "runtime_verified":
        stats.runtimeVerified += 1;
        break;
      case "litespm_tested":
        stats.litespmTested += 1;
        break;
      default:
        stats.discoveryOnly += 1;
    }
    if (item.version) stats.versioned += 1;
  }
  return stats;
}
