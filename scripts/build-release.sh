#!/usr/bin/env bash
set -euo pipefail

# LitePSM Multi-Platform Build Script
# Targets: Windows (amd64, arm64), Linux (amd64, arm64), macOS (amd64, arm64)

VERSION="${VERSION:-0.1.0}"
DIST_DIR="dist"
mkdir -p "${DIST_DIR}"

echo "================================================="
echo "Building LitePSM v${VERSION} Multi-Platform Matrix"
echo "================================================="

TARGETS=(
    "windows/amd64/litepsm-windows-amd64.exe"
    "windows/arm64/litepsm-windows-arm64.exe"
    "linux/amd64/litepsm-linux-amd64"
    "linux/arm64/litepsm-linux-arm64"
    "darwin/amd64/litepsm-darwin-amd64"
    "darwin/arm64/litepsm-darwin-arm64"
)

for target in "${TARGETS[@]}"; do
    IFS="/" read -r os arch filename <<< "$target"
    out_path="${DIST_DIR}/${filename}"
    
    echo "● Compiling ${os}/${arch} -> ${out_path}..."
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w -X main.Version=${VERSION}" \
        -o "${out_path}" \
        ./cmd/litepsm
done

echo ""
echo "● Generating SHA-256 Checksums..."
(
    cd "${DIST_DIR}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum litepsm-* > SHA256SUMS.txt
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 litepsm-* > SHA256SUMS.txt
    fi
)

echo "✓ Build matrix complete! Artifacts in ${DIST_DIR}/:"
ls -la "${DIST_DIR}"
