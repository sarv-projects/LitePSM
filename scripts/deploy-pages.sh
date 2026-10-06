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

echo "● Verifying /v1/current.json endpoint..."
if [ ! -f "${PAGES_DIR}/v1/current.json" ] || [ ! -d "${PAGES_DIR}/v1/releases" ]; then
    echo "❌ /v1 tree missing after materialization"
    exit 1
fi

# The CDN caches /v1/releases/* immutably and /v1/current.json is the only
# mutable part, so a tree that disagrees with its own pointer is unrecoverable
# once uploaded: the digest chain is checked here, before upload, using the same
# rules `catalog sync` applies on the client.
echo "● Verifying /v1 tree against its pointer (digest chain)..."
go run ./cmd/litespm catalog build --verify "${PAGES_DIR}"

echo "● Writing Cloudflare Pages _headers..."
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
EOF

echo "● Running Strict Dist Allowlist & Leak Prevention Audit..."
# Check for forbidden extensions or secret leaks (.go, .git, .env, .pem, .key, etc.)
# Source maps are the leak this audit originally missed: a .js.map embeds the
# original module source, so shipping one publishes the code behind the bundle.
# The .ts rule used to carry a chain of -not exceptions that could never apply
# (a .ts file never matches *.js and friends), which read like an allowlist while
# forbidding every .ts file; it is now the single rule it meant to be.
FORBIDDEN_FILES=$(find "${PAGES_DIR}" -type f \( \
    -name "*.go" -o \
    -name "*.map" -o \
    -name "*.env*" -o \
    -name "*.pem" -o \
    -name "*.key" -o \
    -name "*.db" -o \
    -name "*.sqlite*" -o \
    -name "*.ts" \
\))

if [ -n "${FORBIDDEN_FILES}" ]; then
    echo "❌ SECURITY AUDIT FAILED: Forbidden files detected in ${PAGES_DIR}:"
    echo "${FORBIDDEN_FILES}"
    exit 1
fi

echo "✓ Zero source leaks detected. Dist allowlist verified."
echo "✓ Cloudflare Pages bundle prepared successfully in ${PAGES_DIR}/"
ls -la "${PAGES_DIR}"
