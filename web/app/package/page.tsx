"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Copy, ExternalLink, ChevronRight, Info, SearchX, Check } from "lucide-react";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { Listing } from "../../lib/telemetry";
import { HOSTS, bridgeSnippet, nativeSnippet, hostPath, PlatformOS } from "../../lib/hosts";
import { copyText } from "../../lib/clipboard";
import { formatCount } from "../../lib/format";
import { hostUniverse, kindLabel, listingHref, sortListings } from "../../lib/catalog";
import { hostsFor } from "../../lib/hosts";
import { VerifiedMark } from "../../components/catalog/PublisherMark";
import catalogData from "../../data/catalog.json";

const items = catalogData as unknown as Listing[];
const SITE_URL = "https://litepsm.market";

const HOST_TOTAL = hostUniverse(items).length;
const INSTALL_COMMAND_PREFIX = "litepsm install ";

/**
 * Lookup is built once at module scope, and only unambiguous slugs are
 * registered - see `listingHref` in lib/catalog for why 121 slugs cannot be
 * used as keys. Ids are always unique and always resolve.
 */
const BY_KEY = new Map<string, Listing>();
for (const item of items) BY_KEY.set(item.id, item);
for (const item of items) {
  if (!BY_KEY.has(item.slug)) BY_KEY.set(item.slug, item);
}

function findItem(key: string | null | undefined): Listing | null {
  if (!key) return null;
  return BY_KEY.get(key) ?? null;
}

/** A data field that may legitimately be missing in the snapshot. */
function Value({ children, absent }: { children?: React.ReactNode; absent?: string }) {
  if (children === undefined || children === null || children === "") {
    return <span className="absent">{absent || "not published"}</span>;
  }
  return <>{children}</>;
}

function CopyButton({ text, label, message }: { text: string; label: string; message: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        if (await copyText(text, message)) {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 2000);
        }
      }}
      aria-label={label}
      className="btn shrink-0"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-ink" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

/**
 * The prerendered body of `/package/`. Detail content is keyed off `?slug=`,
 * so the static shell explains the route and links onward instead of shipping
 * an empty frame.
 */
function PackageIndexShell() {
  return (
    <main id="main" className="shell flex-1 py-16">
      <div className="max-w-prose">
        <p className="t-mono text-[11px] text-ink-3">Entry detail</p>
        <h1 className="t-cond mt-2 text-[24px] font-semibold tracking-tight text-ink">
          Package pages resolve one entry at a time
        </h1>
        <p className="mt-2.5 text-[13px] leading-relaxed text-ink-2">
          A detail page is requested as <code className="t-mono">/package/?slug=&lt;key&gt;</code> against a
          single bundled catalog of {formatCount(items.length)} entries. Emitting one static file per entry
          would mean {formatCount(items.length)} files, so instead the index ships as one page and the entry
          resolves in the browser. The key is the entry&rsquo;s slug, or its catalog id where the slug is shared
          by more than one entry.
        </p>

        <div className="section-head mt-9">
          <h2>Most popular entries</h2>
          <Link href="/explore/" className="t-mono shrink-0 text-[12px] text-ink-2 hover:text-ink hover:underline">
            search all
          </Link>
        </div>
        <ul>
          {sortListings(items, "index")
            .slice(0, 12)
            .map((item) => (
              <li key={item.id}>
                <Link
                  href={listingHref(item)}
                  className="grid grid-cols-[3px_minmax(0,1fr)_auto] items-center gap-3 border-b border-rule py-2.5 transition-colors hover:bg-hover"
                >
                  <span
                    aria-hidden="true"
                    className="h-7 w-[3px] rounded-[1px]"
                    style={{ backgroundColor: `var(--${item.kind})` }}
                  />
                  <span className="min-w-0">
                    <span className="t-cond block truncate text-[14px] font-medium text-ink">{item.name}</span>
                    <span className="row-meta">
                      <span>{item.publisher?.name || "not published"}</span>
                      <span className="t-mono">{item.kind}</span>
                      <span className="truncate">{item.category}</span>
                    </span>
                  </span>
                  <span className="t-mono t-tabular shrink-0 text-[11px] text-pop">
                    not published
                  </span>
                </Link>
              </li>
            ))}
        </ul>
      </div>
    </main>
  );
}

function NotFound() {
  return (
    <main id="main" className="shell flex-1 py-20">
      <div className="flex max-w-prose flex-col items-start gap-3">
        <SearchX className="h-6 w-6 text-ink-3" aria-hidden="true" />
        <h1 className="t-cond text-[22px] font-semibold tracking-tight text-ink">
          That entry is not in this catalog snapshot
        </h1>
        <p className="text-[13px] leading-relaxed text-ink-2">
          Package pages resolve from a single bundled catalog of {formatCount(items.length)} entries. This
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
 * bailing into a Suspense boundary. With 5,816 entries the detail pages stay on
 * a single `?slug=` route - `generateStaticParams` would emit thousands of
 * files, which the static host cannot serve.
 */
function useSlugParam(): { slug: string | null; resolved: boolean } {
  const [state, setState] = useState<{ slug: string | null; resolved: boolean }>({
    slug: null,
    resolved: false,
  });

  useEffect(() => {
    // `?slug=` is a query, but the route is also reachable bare. Treat "no
    // parameter" as resolved-with-nothing so a bare /package/ shows the route
    // index rather than an unresolved placeholder.
    const param = new URLSearchParams(window.location.search).get("slug");
    setState({ slug: param, resolved: true });
  }, []);

  return state;
}

function PackageContent() {
  const { slug, resolved } = useSlugParam();
  const item = useMemo(() => findItem(slug), [slug]);

  const [activeHost, setActiveHost] = useState("claude-code");
  const [platformOs, setPlatformOs] = useState<PlatformOS>("linux");
  const [snippetMode, setSnippetMode] = useState<"bridge" | "native">("bridge");

  useEffect(() => {
    const ua = typeof navigator !== "undefined" ? navigator.userAgent.toLowerCase() : "";
    setPlatformOs(ua.includes("mac") ? "mac" : ua.includes("linux") ? "linux" : "win");
  }, []);

  const host = HOSTS.find((h) => h.id === activeHost) ?? HOSTS[0];
  const hostTotal = HOST_TOTAL;

  const snippet = item
    ? snippetMode === "bridge"
      ? bridgeSnippet(host)
      : nativeSnippet(host, item.slug, item.command, item.args)
    : "";

  const related = useMemo(() => {
    if (!item) return [];
    return sortListings(
      items.filter((i) => i.id !== item.id && i.category === item.category),
      "index"
    ).slice(0, 6);
  }, [item]);

  // Pre-hydration, and when the route is visited bare, we cannot know which
  // entry is wanted. Both cases get the route index: a described frame with
  // real links, not a spinner and not a false "not found".
  if (!resolved || !slug) {
    return (
      <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
        <Header />
        <PackageIndexShell />
        <SiteFooter />
      </div>
    );
  }

  if (!item) {
    return (
      <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
        <Header />
        <NotFound />
        <SiteFooter />
      </div>
    );
  }

  const hosts = hostsFor(item);
  const installCommand = `${INSTALL_COMMAND_PREFIX}${item.id}`;

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

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify([jsonLd, breadcrumb]) }}
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
        <div className="grid gap-x-10 gap-y-9 lg:grid-cols-[minmax(0,1fr)_320px]">
          {/* ---------- main column ---------- */}
          <div className="min-w-0">
            <header className="border-l-2 border-ink pl-5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="chip pointer-events-none">
                  <span
                    aria-hidden="true"
                    className="h-2.5 w-[3px] rounded-[1px]"
                    style={{ backgroundColor: `var(--${item.kind})` }}
                  />
                  {kindLabel(item.kind)}
                </span>
                {item.transport && <span className="chip pointer-events-none t-mono !text-[11px]">{item.transport}</span>}
                {item.runtime && <span className="chip pointer-events-none t-mono !text-[11px]">{item.runtime}</span>}
              </div>

              <h1 className="t-cond mt-3 text-[28px] font-semibold leading-tight tracking-tight text-ink sm:text-[32px]">
                {item.name}
              </h1>
              <p className="mt-2 max-w-prose text-[14px] leading-relaxed text-ink-2">{item.summary}</p>
            </header>

            {/* ---- install: the one dark surface, 8px ---- */}
            <section className="on-dark panel-dark mt-7">
              <h2 className="t-mono text-[11px] font-medium text-dark-ink-2">Install with LitePSM</h2>

              <div className="mt-2.5 flex items-center gap-2 border border-dark-rule bg-dark-2 px-3 py-2.5">
                <span className="t-mono shrink-0 text-[12px] text-dark-ink-2" aria-hidden="true">
                  $
                </span>
                <code className="t-mono min-w-0 flex-1 break-all text-[12px] text-dark-ink select-all">
                  {installCommand}
                </code>
                <CopyButton text={installCommand} label="Copy install command" message="Install command copied" />
              </div>

              <p className="mt-3 text-[12px] leading-relaxed text-dark-ink-2">
                Or run <code className="t-mono text-dark-ink">/marketplace</code> inside your agent and
                search for “{item.name}”.
              </p>
            </section>

            {/*
              Only 441 of 5,816 entries publish a host-native command, and all
              of them are plugins. Rendering an empty "Install with your agent"
              heading on the other 92% would be a section that costs vertical
              space to say nothing, so it appears only when there is a command
              to show.
            */}
            {item.installHint && (
              <section className="mt-8">
                <div className="section-head">
                  <h2>Install with your agent</h2>
                  <span className="section-note">
                    {item.kind === "skill"
                      ? "Upstream source repository"
                      : "The host's own plugin command"}
                  </span>
                </div>

                <div className="mt-3 flex items-center gap-2 border border-rule-2 bg-sunken px-3 py-2.5">
                  <code className="t-mono min-w-0 flex-1 break-all text-[12px] text-ink select-all">
                    {item.installHint}
                  </code>
                  <CopyButton
                    text={item.installHint}
                    label="Copy host install command"
                    message="Install command copied"
                  />
                </div>
              </section>
            )}

            {/* ---- setup: host x OS ---- */}
            <section className="mt-8">
              <div className="section-head">
                <div>
                  <h2>Setup</h2>
                  <p className="section-note mt-0.5">
                    One bridge entry per host. Pick an agent and a platform for the exact config file.
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-0.5 border border-ink-3 bg-sunken p-0.5">
                  {(["win", "mac", "linux"] as PlatformOS[]).map((o) => (
                    <button
                      key={o}
                      type="button"
                      aria-pressed={platformOs === o}
                      onClick={() => setPlatformOs(o)}
                      className={`h-6 rounded-[3px] px-2.5 text-[11px] font-medium transition-colors ${
                        platformOs === o ? "bg-ink text-surface" : "text-ink-2 hover:text-ink"
                      }`}
                    >
                      {o === "win" ? "Windows" : o === "mac" ? "macOS" : "Linux"}
                    </button>
                  ))}
                </div>
              </div>

              <div className="no-scrollbar mt-4 flex gap-1.5 overflow-x-auto pb-1" role="group" aria-label="Agent host">
                {HOSTS.map((h) => (
                  <button
                    key={h.id}
                    type="button"
                    aria-pressed={activeHost === h.id}
                    onClick={() => setActiveHost(h.id)}
                    className="chip shrink-0"
                  >
                    {h.name}
                  </button>
                ))}
              </div>

              <div className="mt-3 grid gap-3 sm:grid-cols-2">
                <div>
                  <p className="t-mono text-[10px] text-ink-3">Config file</p>
                  <div className="mt-1 flex items-start gap-2">
                    <code className="t-mono min-w-0 flex-1 break-all border border-rule-2 bg-sunken px-2.5 py-2 text-[11px] text-ink-2">
                      {hostPath(host, platformOs)}
                    </code>
                    <CopyButton
                      text={hostPath(host, platformOs)}
                      label="Copy configuration file path"
                      message="Config path copied"
                    />
                  </div>
                </div>

                <div>
                  <p className="t-mono text-[10px] text-ink-3">Config format</p>
                  <p className="t-mono mt-1 border border-rule-2 bg-sunken px-2.5 py-2 text-[11px] uppercase text-ink-2">
                    {host.kind}
                    {host.nested && <span className="ml-2 normal-case text-ink-3">nested v2 layout</span>}
                  </p>
                </div>
              </div>

              <div className="mt-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="text-[13px] font-semibold text-ink">
                    Snippet
                    <span className="ml-2 font-normal text-ink-3">
                      {snippetMode === "bridge" ? "LitePSM bridge" : "direct native, unmanaged"}
                    </span>
                  </h3>
                  <div className="flex items-center gap-0.5 border border-ink-3 bg-sunken p-0.5">
                    <button
                      type="button"
                      aria-pressed={snippetMode === "bridge"}
                      onClick={() => setSnippetMode("bridge")}
                      className={`h-6 rounded-[3px] px-2.5 text-[11px] font-medium transition-colors ${
                        snippetMode === "bridge" ? "bg-ink text-surface" : "text-ink-2 hover:text-ink"
                      }`}
                    >
                      LitePSM bridge
                    </button>
                    <button
                      type="button"
                      aria-pressed={snippetMode === "native"}
                      onClick={() => setSnippetMode("native")}
                      className={`h-6 rounded-[3px] px-2.5 text-[11px] font-medium transition-colors ${
                        snippetMode === "native" ? "bg-ink text-surface" : "text-ink-2 hover:text-ink"
                      }`}
                    >
                      Direct native
                    </button>
                  </div>
                </div>

                <div className="on-dark mt-2 overflow-hidden rounded-panel bg-dark">
                  <div className="flex items-center justify-between gap-2 border-b border-dark-rule px-3 py-2">
                    <span className="t-mono truncate text-[11px] text-dark-ink-2">
                      {host.name} · {host.kind === "toml" ? "config.toml" : "config.json"}
                    </span>
                    <CopyButton text={snippet} label="Copy configuration snippet" message="Snippet copied" />
                  </div>
                  <pre className="max-h-80 overflow-auto whitespace-pre-wrap p-3.5 font-mono text-[12px] leading-relaxed text-dark-ink">
                    {snippet}
                  </pre>
                </div>

                <p className="mt-2.5 flex items-start gap-2 text-[12px] leading-relaxed text-ink-3">
                  <Info className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                  <span>
                    Running <code className="t-mono text-ink-2">litepsm</code> backs up the target file and
                    injects this single entry, preserving existing entries. The{" "}
                    <em>direct native</em> view is unmanaged: LitePSM will not keep it updated.
                  </span>
                </p>
              </div>
            </section>

            {/* ---- related ---- */}
            {related.length > 0 && (
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
                          <span className="t-cond block truncate text-[14px] font-medium text-ink">
                            {r.name}
                          </span>
                          <span className="row-meta">
                            <span className="meta-publisher">{r.publisher?.name || "unspecified"}</span>
                            <span className="truncate text-ink-3">{r.summary}</span>
                          </span>
                        </span>
                        <span className="t-mono t-tabular shrink-0 text-[11px] text-ink-3">
                          {r.kind}
                        </span>
                      </Link>
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </div>

          {/* ---------- spec rail: a spec sheet, not a card ---------- */}
          <aside className="min-w-0 lg:sticky lg:top-[68px] lg:self-start">
            <div className="section-head">
              <h2>Specification</h2>
            </div>

            <dl className="spec-list mt-3">
              <div>
                <dt>Catalog id</dt>
                <dd className="t-mono !text-[12px]">{item.id}</dd>
              </div>
              <div>
                <dt>Kind</dt>
                <dd>{kindLabel(item.kind)}</dd>
              </div>
              <div>
                <dt>Publisher</dt>
                <dd>
                  {item.publisher?.name ? (
                    item.publisher.url ? (
                      <a
                        href={item.publisher.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="link inline-flex items-center gap-1"
                      >
                        {item.publisher.name}
                        <ExternalLink className="h-3 w-3 text-ink-3" aria-hidden="true" />
                      </a>
                    ) : (
                      item.publisher.name
                    )
                  ) : (
                    <span className="absent">not published</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Verified</dt>
                <dd>
                  {item.publisher?.verified ? (
                    <VerifiedMark glyph />
                  ) : (
                    <span className="absent">not marked verified</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Version</dt>
                <dd className="t-mono !text-[12px]">
                  <Value absent="not published">{item.version}</Value>
                </dd>
              </div>
              <div>
                <dt>Transport</dt>
                <dd className="t-mono !text-[12px]">
                  <Value absent={item.kind === "mcp" ? "not published" : "not applicable"}>
                    {item.transport}
                  </Value>
                </dd>
              </div>
              <div>
                <dt>Runtime</dt>
                <dd className="t-mono !text-[12px]">
                  <Value absent={item.kind === "mcp" ? "not published" : "not applicable"}>{item.runtime}</Value>
                </dd>
              </div>
              <div>
                <dt>Launch command</dt>
                <dd className="t-mono !text-[12px]">
                  {item.command ? (
                    <span className="break-all">
                      {item.command}
                      {item.args?.length ? ` ${item.args.join(" ")}` : ""}
                    </span>
                  ) : (
                    <span className="absent">not published</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Skill source</dt>
                <dd className="t-mono !text-[12px]">
                  {item.skillSource ? (
                    <a
                      href={item.skillSource}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="link break-all"
                    >
                      {new URL(item.skillSource).host}
                    </a>
                  ) : (
                    <span className="absent">{item.kind === "skill" ? "not published" : "not applicable"}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Popularity</dt>
                <dd>
                  <span
                    className="absent"
                    title="The upstream sources expose no machine-readable star, download or install counts, so this catalog publishes none rather than an estimate."
                  >
                    not published
                  </span>
                </dd>
              </div>
            </dl>

            <div className="section-head mt-7">
              <h2>Agent compatibility</h2>
              <span className="section-note t-mono t-tabular">
                {hosts.length} of {hostTotal}
              </span>
            </div>

            {hosts.length ? (
              <ul className="mt-2">
                {hosts.map((h) => (
                  <li
                    key={h}
                    className="flex items-center justify-between gap-3 border-b border-rule py-1.5 text-[12px]"
                  >
                    <span className="text-ink-2">{h}</span>
                    <span
                      className="text-[11px] text-ink-3"
                      title="Publisher-declared compatibility, not a LitePSM test result"
                    >
                      declared
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-2 text-[12px] text-ink-3">This entry declares no agent compatibility.</p>
            )}

            <p className="mt-3 text-[11px] leading-relaxed text-ink-3">
              Compatibility is declared by the publisher. This site does not execute capabilities, so it
              cannot confirm any of these claims.
            </p>
          </aside>
        </div>
      </main>

      <SiteFooter />
    </div>
  );
}

export default function PackagePage() {
  return <PackageContent />;
}
