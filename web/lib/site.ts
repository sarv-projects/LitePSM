// Canonical origin for absolute URLs: metadataBase, Open Graph, Twitter cards,
// and the JSON-LD emitted on package pages.
//
// Canonical and social URLs must name the origin that actually serves this
// site, so the default is the live deployment origin rather than a domain that
// does not resolve yet. Override it at build time — and flip the default once
// the custom domain is registered:
//
//     NEXT_PUBLIC_SITE_URL=https://litespm.market npm run build
//
// Trailing slashes are stripped so callers can append paths directly.
export const SITE_URL = (
  process.env.NEXT_PUBLIC_SITE_URL || "https://litespm.sarveshbh-2022.workers.dev"
).replace(/\/+$/, "");

/**
 * Repository links for the "read more" affordances on the landing page. There
 * is no docs site and no `/docs` route yet (`LPSM-J008`), so "Read the docs"
 * points at the repository README and "View architecture" at the ARCH index —
 * the same origin the footer's GitHub button already names. They are real,
 * resolvable URLs rather than a placeholder route that would 404.
 */
export const REPO_URL = "https://github.com/sarv-projects/LiteSPM";
export const DOCS_URL = `${REPO_URL}/blob/main/README.md`;
export const ARCHITECTURE_URL = `${REPO_URL}/blob/main/ARCH/00-INDEX.md`;