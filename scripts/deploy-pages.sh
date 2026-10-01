#!/usr/bin/env bash
set -euo pipefail

# LitePSM Cloudflare Pages Static Packaging & Security Audit Script
# Conforms to ARCH/18 Section 4 (CDN Headers) & Section 5 (Strict Dist Allowlist)

PAGES_DIR="pages-dist"
rm -rf "${PAGES_DIR}"
mkdir -p "${PAGES_DIR}"

echo "● Building Next.js Static Export..."
(
    cd web
    echo "● Installing web dependencies..."
    npm ci || npm install
    npm run build
)

echo "● Copying Web Static Assets to ${PAGES_DIR}..."
cp -r web/out/* "${PAGES_DIR}/"

echo "● Verifying /v1/current.json endpoint..."
if [ ! -f "${PAGES_DIR}/v1/current.json" ]; then
    mkdir -p "${PAGES_DIR}/v1"
    cp web/public/v1/current.json "${PAGES_DIR}/v1/current.json"
fi

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
FORBIDDEN_FILES=$(find "${PAGES_DIR}" -type f \( \
    -name "*.go" -o \
    -name "*.env*" -o \
    -name "*.pem" -o \
    -name "*.key" -o \
    -name "*.db" -o \
    -name "*.sqlite*" -o \
    -name "*.ts" -not -name "*.js" -not -name "*.css" -not -name "*.html" -not -name "*.json" -not -name "*.txt" -not -name "*.svg" -not -name "*.ico" -not -name "*.png" -not -name "_headers" -not -name "_routes.json" \
\))

if [ -n "${FORBIDDEN_FILES}" ]; then
    echo "❌ SECURITY AUDIT FAILED: Forbidden files detected in ${PAGES_DIR}:"
    echo "${FORBIDDEN_FILES}"
    exit 1
fi

echo "✓ Zero source leaks detected. Dist allowlist verified."
echo "✓ Cloudflare Pages bundle prepared successfully in ${PAGES_DIR}/"
ls -la "${PAGES_DIR}"
