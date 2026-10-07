import Link from "next/link";
import { Header } from "../components/navigation/Header";
import { SiteFooter } from "../components/layout/SiteFooter";

/**
 * Custom not-found page. The framework's built-in 404 renders inside the
 * root layout but carries none of the page furniture, which left the
 * layout's "Skip to content" link pointing at `#main` with no such anchor
 * on the page (an accessibility dead end on the one page that 404s).
 * Every other route renders `<main id="main">` itself; this one does too.
 */
export default function NotFound() {
  return (
    <div className="flex min-h-screen flex-col">
      <Header />

      <main id="main" className="shell flex-1 py-20">
        <p className="t-mono text-[11px] text-ink-3">404</p>
        <h1 className="mt-2 text-[24px] font-semibold tracking-tight text-ink">
          Page not found.
        </h1>
        <p className="mt-3 max-w-prose text-[13px] leading-relaxed text-ink-2">
          That page is not on this site — the link may be wrong, or the page may
          have moved. Everything the catalog knows is reachable from Explore.
        </p>
        <div className="mt-6 flex flex-wrap gap-3">
          <Link href="/" className="btn btn-solid btn-lg">
            Back to the home page
          </Link>
          <Link href="/explore/" className="btn btn-lg">
            Explore capabilities
          </Link>
        </div>
      </main>

      <SiteFooter />
    </div>
  );
}
