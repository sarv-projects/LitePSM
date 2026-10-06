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

# Every judgement about the finished bundle lives in one script, which CI also
# runs, so the deploy path and the checks it faces cannot drift apart.
./scripts/audit-pages-dist.sh "${PAGES_DIR}"
echo "✓ Cloudflare Pages bundle prepared successfully in ${PAGES_DIR}/"
ls -la "${PAGES_DIR}"
