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
  process.env.NEXT_PUBLIC_SITE_URL || "https://litepsm.sarveshbh-2022.workers.dev"
).replace(/\/+$/, "");