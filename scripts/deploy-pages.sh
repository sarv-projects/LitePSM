#!/usr/bin/env bash
set -euo pipefail

# LiteSPM Cloudflare Pages Static Packaging & Security Audit Script
# Conforms to ARCH/18 Section 4 (CDN Headers) & Section 5 (Strict Dist Allowlist)

PAGES_DIR="pages-dist"
rm -rf "${PAGES_DIR}"
mkdir -p "${PAGES_DIR}"

echo "● Building Next.js Static Export..."
(
    cd web
    echo "● Installing web dependencies..."
    # Strict lockfile install. A silent `|| npm install` fallback would let a
    # release ship a dependency tree that no lockfile describes.
    npm ci
    # NEXT_PUBLIC_SITE_URL sets the canonical origin used for metadata, Open
    # Graph and JSON-LD URLs; it must name the origin that serves the site.
    # Empty keeps the built-in default from web/lib/site.ts.
    NEXT_PUBLIC_SITE_URL="${NEXT_PUBLIC_SITE_URL:-}" npm run build
)

echo "● Copying Web Static Assets to ${PAGES_DIR}..."
cp -r web/out/* "${PAGES_DIR}/"

# The Next.js export copies web/public/v1 (whatever a previous local build
# left there) into the bundle. The /v1 API tree comes exclusively from the Go
# builder below, so drop the exported copy before materializing it: stale or
# untracked release directories must never reach the CDN.
rm -rf "${PAGES_DIR}/v1"

echo "● Materializing catalog release tree (Go builder)..."
# -materialize reproduces the RELEASED pointer's id, sequence, createdAt and
# manifest byte for byte instead of cutting a new release: the CDN caches
# /v1/releases/* immutably, so the deployed bytes must be exactly the bytes
# web/public/v1/current.json digests. It fails closed if the released pointer
# is missing or is not a Go-built pointer.
go run ./cmd/litespm catalog build \
    --materialize \
    --out "${PAGES_DIR}" \
    --prev web/public/v1/current.json

# Sign the trust anchor. /v1/current.json is what every client verifies its
# release chain against, so it is the one file whose AUTHENTICITY matters:
# the digest chain (manifest/listings/versions) proves integrity from the
# pointer down, and this signature proves who published the pointer.
#
# Signing runs only where a real signing identity exists — CI's OIDC token
# (ACTIONS_ID_TOKEN_REQUEST_URL) for cosign's keyless flow. A local run has
# none and says so rather than producing a signature nobody can verify.
# Clients check it when present (internal/catalog) and require it with
# LITESPM_REQUIRE_CATALOG_SIGNATURE=1.
if command -v cosign >/dev/null 2>&1 && [ -n "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]; then
    echo "● Signing /v1/current.json (cosign keyless)..."
    cosign sign-blob --yes \
        --bundle "${PAGES_DIR}/v1/current.json.sigstore.json" \
        "${PAGES_DIR}/v1/current.json"
else
    echo "● Skipping catalog pointer signature (cosign or OIDC identity unavailable in this environment)"
fi

echo "● Writing Cloudflare Pages _headers..."
# ARCH/18 §4: the CDN caches /v1/releases/* immutably and /v1/current.json is
# the only mutable part. CORS is open (the catalog is public data) and
# nosniff is set on both stanzas.
cat << 'EOF' > "${PAGES_DIR}/_headers"
# Immutable release directory (forever cached)
/v1/releases/*
  Cache-Control: public, max-age=31536000, immutable
  Access-Control-Allow-Origin: *
  X-Content-Type-Options: nosniff

# Pointer file (always revalidate)
/v1/current.json
  Cache-Control: public, no-cache, must-revalidate
  Access-Control-Allow-Origin: *
  X-Content-Type-Options: nosniff

# Signature bundle for the pointer (revalidate with it; never immutable)
/v1/current.json.sigstore.json
  Cache-Control: public, no-cache, must-revalidate
  Access-Control-Allow-Origin: *
  X-Content-Type-Options: nosniff

# Site-shaped catalog rows, fetched on the first interaction that needs them.
# _headers matches the path, not the query, and the site always requests
# ?v=<datasetDigest> — so the bare URL must not be pinned long either: a
# short max-age keeps a direct hit from serving last week's rows after a
# redeploy, while the digest query keeps concurrent fetches self-consistent.
/data/catalog.json
  Cache-Control: public, max-age=300, must-revalidate
  Access-Control-Allow-Origin: *
  X-Content-Type-Options: nosniff
EOF

# Every judgement about the finished bundle lives in one script, which CI also
# runs, so the deploy path and the checks it faces cannot drift apart.
./scripts/audit-pages-dist.sh "${PAGES_DIR}"
echo "✓ Cloudflare Pages bundle prepared successfully in ${PAGES_DIR}/"
ls -la "${PAGES_DIR}"
