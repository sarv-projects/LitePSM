"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Check, ChevronRight, SearchX } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { Listing } from "../../lib/telemetry";
import { formatCount } from "../../lib/format";
import { kindLabel, listingHref, sortListings } from "../../lib/catalog";
import { hostsFor } from "../../lib/hosts";
import { VerifiedMark } from "../../components/catalog/PublisherMark";
import { useCatalog } from "../../lib/catalogData";
import { SITE_URL } from "../../lib/site";
import { AGENT_CHOICES, findAgentChoice, rowWorksWithAgent } from "../../lib/landing";
import type { AgentChoice } from "../../lib/landing";
import { AddSteps, scrollToSection } from "../../components/package/AddSteps";
import { PackageTabs } from "../../components/package/PackageTabs";
import type { PackageTabId } from "../../components/package/PackageTabs";

/** Same key the home strip writes; one preference, one storage slot. */
const AGENT_STORAGE_KEY = "litespm-agent";

/**
 * What `/package/` needs from the catalog, and all it needs before a reader
 * actually asks for an entry. The index shell below is what the route
 * prerenders; the detail view resolves from `?slug=`, which can only be
 * answered from the full row set — so this route fetches `data/catalog.json`
 * when (and only when) a slug is present. A bare `/package/` visit ships no
 * catalog rows at all.
 */
export interface PackageViewProps {
  /** Row count of this build's snapshot, for the index copy. */
  total: number;
  /** `hostUniverse(rows).length` at build time — the coverage denominator. */
  hostTotal: number;
  /** First twelve rows in index order: the list the static shell renders. */
  indexRows: Listing[];
}

/**
 * Lookup is built once over the fetched row set, and only unambiguous slugs
 * are registered as keys - see `listingHref` in lib/catalog for why 121 slugs
 * cannot be used as keys. Ids are always unique and always resolve.
 */
function buildLookup(rows: Listing[]): Map<string, Listing> {
  const byKey = new Map<string, Listing>();
  for (const row of rows) byKey.set(row.id, row);
  for (const row of rows) {
    if (!byKey.has(row.slug)) byKey.set(row.slug, row);
  }
  return byKey;
}

/**
 * The prerendered body of `/package/`. Detail content is keyed off `?slug=`,
 * so the static shell explains the route and links onward instead of shipping
 * an empty frame.
 *
 * `note` is rendered only on the client (a deep link resolved after mount), so
 * it never appears in — and never mismatches — the prerendered HTML.
 */
function PackageIndexShell({
  total,
  indexRows,
  note,
}: {
  total: number;
  indexRows: Listing[];
  note?: React.ReactNode;
}) {
  return (
    <main id="main" className="shell flex-1 py-16">
      <div className="max-w-prose">
        <p className="t-mono text-[11px] text-ink-3">Entry detail</p>
        <h1 className="t-cond mt-2 text-[24px] font-semibold tracking-tight text-ink">
          Package pages resolve one entry at a time
        </h1>
        <p className="mt-2.5 text-[13px] leading-relaxed text-ink-2">
          A detail page is requested as <code className="t-mono">/package/?slug=&lt;key&gt;</code> against a
          single catalog snapshot of {formatCount(total)} entries. Emitting one static file per entry
          would mean {formatCount(total)} files, so instead the index ships as one page and the entry
          resolves in the browser from the rows it fetches on demand. The key is the entry&rsquo;s slug,
          or its catalog id where the slug is shared by more than one entry.
        </p>

        {note}

        <div className="section-head mt-9">
          <h2>Start of the catalog snapshot</h2>
          <Link href="/explore/" className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline">
            search all
          </Link>
        </div>
        <ul>
          {indexRows.map((item) => (
              <li key={item.id}>
                <Link
                  href={listingHref(item)}
                  className="grid grid-cols-[3px_minmax(0,1fr)] items-center gap-3 border-b border-rule py-2.5 transition-colors hover:bg-hover"
                >
                  <span
                    aria-hidden="true"
                    className="h-7 w-[3px] rounded-[1px]"
                    style={{ backgroundColor: `var(--${item.kind})` }}
                  />
                  <span className="min-w-0">
                    <span className="t-cond block truncate text-[14px] font-medium text-ink">{item.name}</span>
                    <span className="row-meta">
                      <span>{item.publisher?.name || "unspecified"}</span>
                      <span className="t-mono">{item.kind}</span>
                      <span className="truncate">{item.category}</span>
                    </span>
                  </span>
                </Link>
              </li>
            ))}
        </ul>
      </div>
    </main>
  );
}

function NotFound({ total }: { total: number }) {
  return (
    <main id="main" className="shell flex-1 py-20">
      <div className="flex max-w-prose flex-col items-start gap-3">
        <SearchX className="h-6 w-6 text-ink-3" aria-hidden="true" />
        <h1 className="t-cond text-[22px] font-semibold tracking-tight text-ink">
          That entry is not in this catalog snapshot
        </h1>
        <p className="text-[13px] leading-relaxed text-ink-2">
          Package pages resolve from a single catalog snapshot of {formatCount(total)} entries. This
          slug is not in it — it may have been renamed, withdrawn, or published after this build.
        </p>
        <Link href="/explore/" className="btn btn-solid mt-2">
          Search the catalog
        </Link>
      </div>
    </main>
  );
}

/**
 * The slug is read from `window.location` after mount rather than through
 * `useSearchParams`, so this route still prerenders as static HTML instead of
 * bailing into a Suspense boundary. With 5,814 entries the detail pages stay on
 * a single `?slug=` route - `generateStaticParams` would emit thousands of
 * files, which the static host cannot serve.
 *
 * A slug is also the signal that the full row set is needed: the request fires
 * in the same effect that reads the parameter, so the fetch starts on the first
 * client frame rather than one render later. A bare `/package/` never asks.
 */
function useSlugParam(request: () => void): { slug: string | null; resolved: boolean } {
  const [state, setState] = useState<{ slug: string | null; resolved: boolean }>({
    slug: null,
    resolved: false,
  });

  useEffect(() => {
    // `?slug=` is a query, but the route is also reachable bare. Treat "no
    // parameter" as resolved-with-nothing so a bare /package/ shows the route
    // index rather than an unresolved placeholder.
    const param = new URLSearchParams(window.location.search).get("slug");
    if (param) request();
    setState({ slug: param, resolved: true });
  }, [request]);

  return state;
}

function PackageContent({ total, hostTotal, indexRows }: PackageViewProps) {
  const { status, items: full, request } = useCatalog();
  const { slug, resolved } = useSlugParam(request);
  const lookup = useMemo(() => (full ? buildLookup(full) : null), [full]);
  const item = useMemo(() => (slug && lookup ? lookup.get(slug) ?? null : null), [slug, lookup]);

  // The agent preference. Reading it is enough to personalize the page;
  // changing it writes the same `litespm-agent` key the home strip uses and
  // recomputes over rows already in memory — it never calls `request()` (the
  // E1 fetch contract: only a `?slug=` deep link asks for the dataset here).
  const [agentId, setAgentId] = useState<string | null>(null);
  useEffect(() => {
    try {
      setAgentId(window.localStorage.getItem(AGENT_STORAGE_KEY));
    } catch {
      // Storage can be unavailable (private mode, policy); the page still
      // works, it just cannot remember the choice.
    }
  }, []);
  const changeAgent = (id: string) => {
    setAgentId(id);
    try {
      window.localStorage.setItem(AGENT_STORAGE_KEY, id);
    } catch {
      /* preference is best-effort */
    }
  };
  const agent: AgentChoice | null = findAgentChoice(agentId);

  // Which tab the detail section is showing. Local state only: switching a tab
  // renders rows already in memory, nothing is fetched.
  const [tab, setTab] = useState<PackageTabId>("overview");

  // Version coverage over the loaded row set — a count only because this route
  // has the rows in memory once a slug resolved.
  const snapshot = useMemo(() => {
    if (!full) return { total: 0, versioned: 0 };
    let versioned = 0;
    for (const row of full) if (row.version) versioned += 1;
    return { total: full.length, versioned };
  }, [full]);

  // Pre-hydration, and when the route is visited bare, we cannot know which
  // entry is wanted. Both cases get the route index: a described frame with
  // real links, not a spinner and not a false "not found".
  if (!resolved || !slug) {
    return (
      <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
        <Header />
        <PackageIndexShell total={total} indexRows={indexRows} />
        <SiteFooter />
      </div>
    );
  }

  if (!item) {
    // Three honest states, never a premature "not found": the index shell
    // stays on screen while the snapshot is still being fetched (it is what
    // this route prerendered, so the DOM does not flash), a failed fetch says
    // so with a retry, and only a completed snapshot may declare a key absent.
    if (status === "ready") {
      return (
        <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
          <Header />
          <NotFound total={total} />
          <SiteFooter />
        </div>
      );
    }
    const pending = status === "loading" || status === "idle";
    return (
      <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
        <Header />
        <PackageIndexShell
          total={total}
          indexRows={indexRows}
          note={
            pending ? (
              <p className="t-mono mt-6 text-[12px] text-ink-3" role="status">
                Loading “{slug}”…
              </p>
            ) : (
              <div className="mt-6 border border-rule-2 bg-sunken px-4 py-3">
                <p className="text-[13px] text-ink-2">
                  The catalog rows could not be fetched, so this key cannot be resolved either way.
                </p>
                <button type="button" onClick={request} className="btn btn-solid mt-2">
                  Try again
                </button>
              </div>
            )
          }
        />
        <SiteFooter />
      </div>
    );
  }

  const hosts = hostsFor(item);
  const worksWith = rowWorksWithAgent(item, agent);

  // Rendered client-side because the entry is keyed off `?slug=`. Every field
  // here comes straight from the bundled record; nothing is inferred.
  const breadcrumb = {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: [
      { "@type": "ListItem", position: 1, name: "Index", item: `${SITE_URL}/` },
      {
        "@type": "ListItem",
        position: 2,
        name: kindLabel(item.kind) + "s",
        item: `${SITE_URL}/explore/?kind=${item.kind}`,
      },
      {
        "@type": "ListItem",
        position: 3,
        name: item.category,
        item: `${SITE_URL}/explore/?category=${encodeURIComponent(item.category)}`,
      },
      { "@type": "ListItem", position: 4, name: item.name },
    ],
  };

  const jsonLd = {
    "@context": "https://schema.org",
    "@type": "SoftwareSourceCode",
    name: item.name,
    description: item.summary,
    url: `${SITE_URL}${listingHref(item)}`,
    version: item.version || undefined,
    author: { "@type": "Organization", name: item.publisher?.name || "Unknown" },
    ...(item.publisher?.url ? { sameAs: item.publisher.url } : {}),
  };

  /** Question two — plain first, exact fields one disclosure deeper. */
  const canDo = {
    mcp: {
      plain:
        "An MCP server is a small service your agent talks to: it gives your agent new tools to call — search, read, write, run — without the agent itself changing. Once LiteSPM has registered it, your agent reaches it through the one LiteSPM bridge entry.",
      published:
        "The catalog publishes this server's description and its runtime. The individual tools it exposes are discovered by your agent after it connects — this page does not list them.",
      connects: "the single LiteSPM bridge entry, once per host",
    },
    skill: {
      plain:
        "An agent skill is a written workflow — a SKILL.md your agent loads as instructions when they are relevant. It changes what your agent knows how to do, not what software it runs.",
      published:
        "The catalog publishes the skill's name, description and source repository; the instructions themselves live in that repository.",
      connects: "the host's own skills directory, where LiteSPM writes the file",
    },
    plugin: {
      plain:
        "A plugin is a bundle the agent host installs as a unit — its commands, hooks and tools come from the host's own plugin system. What a bundle contains is defined by its publisher.",
      published:
        "The catalog publishes the publisher's description and the hosts the publisher declares it works with.",
      connects: "the host's own plugin command",
    },
  }[item.kind];

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify([jsonLd, breadcrumb]).replace(/</g, "\\u003c"),
        }}
      />

      <nav aria-label="Breadcrumb" className="border-b border-rule bg-sunken">
        <ol className="t-mono shell flex items-center gap-1.5 overflow-x-auto py-2 text-[11px] text-ink-3">
          <li>
            <Link href="/" className="hover:text-ink hover:underline">
              Index
            </Link>
          </li>
          <ChevronRight className="h-3 w-3 shrink-0 text-rule-2" aria-hidden="true" />
          <li>
            <Link href={`/explore/?kind=${item.kind}`} className="hover:text-ink hover:underline">
              {kindLabel(item.kind)}s
            </Link>
          </li>
          <ChevronRight className="h-3 w-3 shrink-0 text-rule-2" aria-hidden="true" />
          <li className="truncate">
            <Link
              href={`/explore/?category=${encodeURIComponent(item.category)}`}
              className="hover:text-ink hover:underline"
            >
              {item.category}
            </Link>
          </li>
          <ChevronRight className="h-3 w-3 shrink-0 text-rule-2" aria-hidden="true" />
          <li aria-current="page" className="truncate text-ink">
            {item.name}
          </li>
        </ol>
      </nav>

      <main id="main" className="shell flex-1 pb-16 pt-7">
        <div className="mx-auto w-full max-w-[1040px]">
          {/* ---- 1. What is this? -------------------------------------- */}
          <header aria-labelledby="pkg-title">
            <p className="kicker">What is this?</p>

            <div className="mt-2 flex flex-wrap items-center gap-2">
              <span className="chip pointer-events-none">
                <span
                  aria-hidden="true"
                  className="h-2.5 w-[3px] rounded-[1px]"
                  style={{ backgroundColor: `var(--${item.kind})` }}
                />
                {kindLabel(item.kind)}
              </span>
              <span className="chip pointer-events-none">{item.category}</span>
            </div>

            <h1
              id="pkg-title"
              className="mt-3 text-[28px] font-semibold leading-tight tracking-tight text-ink sm:text-[32px]"
            >
              {item.name}
            </h1>

            <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[13px] text-ink-2">
              {item.publisher?.url && /^https?:\/\//i.test(item.publisher.url) ? (
                <a
                  href={item.publisher.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="link"
                >
                  {item.publisher.name}
                </a>
              ) : (
                <span>{item.publisher?.name || "unspecified publisher"}</span>
              )}
              {item.publisher?.verified && <VerifiedMark glyph />}
            </p>

            <p className="mt-2.5 max-w-prose text-[14px] leading-relaxed text-ink-2">{item.summary}</p>
          </header>

          {/* ---- 2. What can it do? ------------------------------------ */}
          <section aria-labelledby="pkg-can-title" className="mt-9">
            <div className="section-head">
              <h2 id="pkg-can-title">What can it do?</h2>
              <span className="section-note">plain language first</span>
            </div>

            <p className="mt-3 max-w-prose text-[14px] leading-relaxed text-ink-2">{canDo.plain}</p>
            <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-3">{canDo.published}</p>

            <details className="group mt-2 border-t border-rule">
              <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[12.5px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[36px]">
                Technical details
                <span
                  aria-hidden="true"
                  className="text-ink-3 transition-transform duration-panel ease-out group-open:rotate-90"
                >
                  →
                </span>
              </summary>
              <dl className="spec-list pb-1">
                <div>
                  <dt>Connects through</dt>
                  <dd>{canDo.connects}</dd>
                </div>
                <div>
                  <dt>Runtime</dt>
                  <dd className="t-mono !text-[12px]">
                    {item.runtime || (
                      <span className="absent">{item.kind === "mcp" ? "unspecified" : "not applicable"}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt>Transport</dt>
                  <dd className="t-mono !text-[12px]">
                    {item.transport || (
                      <span className="absent">{item.kind === "mcp" ? "unspecified" : "not applicable"}</span>
                    )}
                  </dd>
                </div>
              </dl>
            </details>
          </section>

          {/* ---- 3. Will it work with what I use? ---------------------- */}
          <section
            id="will-it-work"
            aria-labelledby="pkg-fit-title"
            tabIndex={-1}
            className="mt-9 scroll-mt-16"
          >
            <div className="section-head">
              <h2 id="pkg-fit-title">Will it work with what I use?</h2>
              <span className="section-note t-mono t-tabular">
                {hosts.length} of {hostTotal} hosts
              </span>
            </div>

            <div className="mt-3 flex flex-wrap items-center gap-1.5">
              <span className="mr-1 text-[12px] text-ink-3">You use:</span>
              {AGENT_CHOICES.map((option) => (
                <button
                  key={option.id}
                  type="button"
                  aria-pressed={agent?.id === option.id}
                  onClick={() => changeAgent(option.id)}
                  className="chip"
                >
                  {option.label}
                </button>
              ))}
            </div>

            <div className="mt-3 flex flex-wrap items-center gap-2">
              {!agent ? (
                <p className="max-w-prose text-[13.5px] leading-relaxed text-ink-2">
                  Pick the agent you use above and this entry answers immediately, from rows already
                  on this page — no download is needed to say it.
                </p>
              ) : agent.hosts.length === 0 ? (
                <p className="max-w-prose text-[13.5px] leading-relaxed text-ink-2">
                  Nothing is measured against &ldquo;{agent.label}&rdquo;, so this entry makes no
                  compatibility claim about it either way.
                </p>
              ) : (
                <>
                  <span
                    className={`inline-flex items-center gap-1 rounded-chip border px-1.5 py-0.5 text-[11px] font-medium ${
                      worksWith
                        ? "border-accent/30 bg-accent-wash text-accent-text"
                        : "border-rule bg-sunken text-ink-3"
                    }`}
                    title={
                      worksWith
                        ? "Derived from the capability's kind: MCP servers install through the bridge into any host with an adapter, skills into any host with a skills directory, plugins list their own hosts. A declaration, not a test result."
                        : "This entry does not list that agent among its compatible hosts. An absence of a declaration, not a test result."
                    }
                  >
                    {worksWith && <Check className="h-3 w-3" aria-hidden="true" />}
                    {worksWith ? `Works with ${agent.label}` : `Not declared for ${agent.label}`}
                  </span>
                  <span className="max-w-prose text-[13px] leading-relaxed text-ink-3">
                    {worksWith
                      ? "from this entry's declared or kind-derived host list — a declaration, not a test run."
                      : "an absence of a claim, not evidence of failure."}
                  </span>
                </>
              )}
            </div>

            <p className="mt-3 max-w-prose text-[13px] leading-relaxed text-ink-3">
              Compatibility is either declared by the publisher or derived from the entry&rsquo;s kind
              (MCP servers run through the bridge, skills into any skills directory). This site does
              not execute capabilities, so none of it is a test result.
            </p>

            <button
              type="button"
              className="btn mt-3"
              onClick={() => {
                setTab("compatibility");
                scrollToSection("pkg-details");
              }}
            >
              See the full compatibility list
            </button>
          </section>

          {/* ---- 4. What access does it need? -------------------------- */}
          <section aria-labelledby="pkg-access-title" className="mt-9">
            <div className="section-head">
              <h2 id="pkg-access-title">What access does it need?</h2>
              <span className="section-note">what the catalog carries</span>
            </div>

            <p className="mt-3 max-w-prose text-[14px] leading-relaxed text-ink-2">
              A per-entry list of the files, sites or credentials this capability can reach is
              designed for the catalog but not published yet — so the honest answer today is{" "}
              <span className="absent">not published</span>. What you do get: the config file
              LiteSPM would touch (one bridge entry, backed up first), and — where a server needs a
              credential — an environment variable you name at install time, recorded as a name
              rather than a value.
            </p>

            <details className="group mt-2 border-t border-rule">
              <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[12.5px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[36px]">
                Where access would be declared
                <span
                  aria-hidden="true"
                  className="text-ink-3 transition-transform duration-panel ease-out group-open:rotate-90"
                >
                  →
                </span>
              </summary>
              <dl className="spec-list pb-1">
                <div>
                  <dt>effects</dt>
                  <dd className="!text-[12.5px]">
                    <span className="absent">designed, not yet published</span> — the per-entry access
                    declaration this page would quote
                  </dd>
                </div>
                <div>
                  <dt>command / args</dt>
                  <dd className="t-mono !text-[12px]">
                    {item.command ? (
                      <span className="break-all">
                        {item.command}
                        {item.args?.length ? ` ${item.args.join(" ")}` : ""}
                      </span>
                    ) : (
                      <span className="absent">unspecified</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt>installHint</dt>
                  <dd className="t-mono !text-[12px]">
                    {item.installHint ? (
                      <span className="break-all">{item.installHint}</span>
                    ) : (
                      <span className="absent">{item.kind === "plugin" ? "unspecified" : "not applicable"}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt>skillSource</dt>
                  <dd className="t-mono !text-[12px]">
                    {item.skillSource ? (
                      <span className="break-all">{item.skillSource}</span>
                    ) : (
                      <span className="absent">{item.kind === "skill" ? "unspecified" : "not applicable"}</span>
                    )}
                  </dd>
                </div>
              </dl>
            </details>
          </section>

          {/* ---- 5. How do I add it? ----------------------------------- */}
          <div className="mt-9">
            <AddSteps
              item={item}
              agent={agent}
              onChooseAgent={() => scrollToSection("will-it-work")}
            />
          </div>

          {/* ---- then: tabs -------------------------------------------- */}
          <div id="pkg-details" tabIndex={-1} className="scroll-mt-16">
            <PackageTabs
              item={item}
              agent={agent}
              hostTotal={hostTotal}
              snapshot={snapshot}
              active={tab}
              onChange={setTab}
            />
          </div>

          {/* ---- related ---- */}
          <RelatedRow item={item} full={full} />
        </div>
      </main>

      <SiteFooter />
    </div>
  );
}

/** Also-in-category: discovery after the record, never a popularity list. */
function RelatedRow({ item, full }: { item: Listing; full: Listing[] | null }) {
  const related = useMemo(() => {
    if (!item || !full) return [];
    return sortListings(
      full.filter((i) => i.id !== item.id && i.category === item.category),
      "index"
    ).slice(0, 6);
  }, [item, full]);

  if (related.length === 0) return null;

  return (
    <section className="mt-9">
      <div className="section-head">
        <h2>Also in {item.category}</h2>
        <Link
          href={`/explore/?category=${encodeURIComponent(item.category)}`}
          className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline"
        >
          see all
        </Link>
      </div>
      <ul>
        {related.map((r) => (
          <li key={r.id}>
            <Link
              href={listingHref(r)}
              className="grid grid-cols-[3px_minmax(0,1fr)_auto] items-center gap-3 border-b border-rule py-2.5 transition-colors hover:bg-hover"
            >
              <span
                aria-hidden="true"
                className="h-7 w-[3px] rounded-[1px]"
                style={{ backgroundColor: `var(--${r.kind})` }}
              />
              <span className="min-w-0">
                <span className="t-cond block truncate text-[14px] font-medium text-ink">{r.name}</span>
                <span className="row-meta">
                  <span className="meta-publisher">{r.publisher?.name || "unspecified"}</span>
                  <span className="truncate text-ink-3">{r.summary}</span>
                </span>
              </span>
              <span className="t-mono t-tabular shrink-0 text-[11px] text-ink-3">{r.kind}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export default function PackagePage(props: PackageViewProps) {
  return <PackageContent {...props} />;
}
