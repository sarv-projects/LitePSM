"use client";

import Link from "next/link";
import { Header } from "../../components/navigation/Header";
import { SiteFooter } from "../../components/layout/SiteFooter";
import { CategoryFacet, kindLabel } from "../../lib/catalog";
import { formatCount } from "../../lib/format";

const KIND_ORDER = ["mcp", "skill", "plugin"] as const;

/**
 * The category index is pure aggregation, so `app/categories/page.tsx` derives
 * it from the full row set at build time and passes it in. Nothing here needs
 * the rows themselves: this route never fetches the dataset at all.
 */
export interface CategoriesViewProps {
  total: number;
  categories: CategoryFacet[];
}

export default function CategoriesPage({ total, categories }: CategoriesViewProps) {

  return (
    <div className="flex min-h-screen flex-col" style={{ ["--stack-top" as string]: "48px" }}>
      <Header />

      <main id="main" className="shell flex-1 pt-8">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="text-[24px] font-semibold tracking-tight text-ink">Categories</h1>
          <p className="t-mono text-[11px] text-ink-3">
            {formatCount(categories.length)} categories across {formatCount(total)} entries
          </p>
        </div>

        <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-2">
          Categories are the upstream registry&rsquo;s own labels, not a curated taxonomy, so a few of them
          describe a language or a collection rather than a domain. The bar shows each category&rsquo;s share
          of the largest one.
        </p>

        {/* Column headers are a real table header, not an ALL-CAPS eyebrow. */}
        <div className="mt-7 border-b border-ink pb-2">
          <h2 className="sr-only">All categories with entry counts</h2>
          <div className="t-mono grid grid-cols-[minmax(0,1fr)_56px_130px] items-center gap-4 text-[10px] text-ink-3 md:grid-cols-[260px_64px_170px_minmax(120px,1fr)]">
            <span>Category</span>
            <span className="text-right">Entries</span>
            <span className="hidden md:block">Kind mix</span>
            <span className="hidden md:block">Share of the largest category</span>
          </div>
        </div>

        <ul>
          {categories.map((cat) => (
            <li key={cat.name}>
              <Link
                href={`/explore/?category=${encodeURIComponent(cat.name)}`}
                className="grid grid-cols-[minmax(0,1fr)_56px_130px] items-center gap-4 border-b border-rule py-2.5 transition-colors hover:bg-hover md:grid-cols-[260px_64px_170px_minmax(120px,1fr)]"
              >
                <span className="t-cond truncate text-[14px] font-medium text-ink">{cat.name}</span>

                <span className="t-mono t-tabular text-right text-[12px] text-ink-2">
                  {formatCount(cat.count)}
                </span>

                <span className="hidden items-center gap-2 md:flex">
                  {KIND_ORDER.map((kind) =>
                    cat.byKind[kind] > 0 ? (
                      <span
                        key={kind}
                        className="flex items-center gap-1"
                        title={`${formatCount(cat.byKind[kind])} ${kindLabel(kind).toLowerCase()}${cat.byKind[kind] === 1 ? "" : "s"}`}
                      >
                        <span
                          aria-hidden="true"
                          className="h-2.5 w-[3px] rounded-[1px]"
                          style={{ backgroundColor: `var(--${kind})` }}
                        />
                        <span className="t-mono t-tabular text-[11px] text-ink-3">
                          {formatCount(cat.byKind[kind])}
                        </span>
                      </span>
                    ) : null
                  )}
                </span>

                {/* The bar is a magnitude read, not decoration: it is scaled to
                    the largest category, so 100% means "Developer Tools". */}
                <span className="hidden items-center gap-2 md:flex">
                  <span className="block h-1.5 min-w-0 flex-1 bg-sunken-2">
                    <span
                      className="block h-full bg-ink-3"
                      style={{ width: `${Math.max(1.5, cat.share * 100)}%` }}
                    />
                  </span>
                  <span className="t-mono t-tabular w-9 shrink-0 text-right text-[10px] text-ink-3">
                    {Math.round(cat.share * 100)}%
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </main>

      <SiteFooter />
    </div>
  );
}
