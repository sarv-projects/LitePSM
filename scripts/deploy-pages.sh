#!/usr/bin/env bash
set -euo pipefail

# LitePSM Cloudflare Pages Static Packaging Script
# Bundles the static catalog shards (/v1/current.json, /v1/releases/) and Next.js web export.

PAGES_DIR="pages-dist"
rm -rf "${PAGES_DIR}"
mkdir -p "${PAGES_DIR}"

echo "● Building Next.js Static Export..."
(
    cd web
    npm run build
)

echo "● Copying Web Static Assets to ${PAGES_DIR}..."
cp -r web/out/* "${PAGES_DIR}/"

echo "● Verifying /v1/current.json endpoint..."
if [ ! -f "${PAGES_DIR}/v1/current.json" ]; then
    mkdir -p "${PAGES_DIR}/v1"
    cp web/public/v1/current.json "${PAGES_DIR}/v1/current.json"
fi

echo "✓ Cloudflare Pages bundle prepared in ${PAGES_DIR}/:"
ls -la "${PAGES_DIR}"
