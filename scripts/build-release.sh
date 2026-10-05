#!/usr/bin/env bash
set -euo pipefail

# LiteSPM Multi-Platform Build Script
# Targets: Windows (amd64, arm64), Linux (amd64, arm64), macOS (amd64, arm64)

VERSION="${VERSION:-0.3.0}"
DIST_DIR="dist"
mkdir -p "${DIST_DIR}"

echo "================================================="
echo "Building LiteSPM v${VERSION} Multi-Platform Matrix"
echo "================================================="

TARGETS=(
    "windows/amd64/litespm-windows-amd64.exe"
    "windows/arm64/litespm-windows-arm64.exe"
    "linux/amd64/litespm-linux-amd64"
    "linux/arm64/litespm-linux-arm64"
    "darwin/amd64/litespm-darwin-amd64"
    "darwin/arm64/litespm-darwin-arm64"
)

for target in "${TARGETS[@]}"; do
    IFS="/" read -r os arch filename <<< "$target"
    out_path="${DIST_DIR}/${filename}"
    
    echo "● Compiling ${os}/${arch} -> ${out_path}..."
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w -X main.Version=${VERSION}" \
        -o "${out_path}" \
        ./cmd/litespm
done

echo ""
echo "● Generating SHA-256 Checksums..."
(
    cd "${DIST_DIR}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum litespm-* > SHA256SUMS.txt
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 litespm-* > SHA256SUMS.txt
    fi
)

echo "✓ Build matrix complete! Artifacts in ${DIST_DIR}/:"
ls -la "${DIST_DIR}"
