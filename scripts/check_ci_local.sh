#!/usr/bin/env bash
# check_ci_local.sh — run the .github/workflows/ci.yml stages on a local
# Linux or macOS machine, in CI order, with the same commands.
#
# Why this exists: every CI stage must be reproducible on the developer's
# machine with one command, so "works on my machine" and "works in CI" are the
# same claim again. (Historical note: the jobs behind this script were red for
# weeks as "non-reproducible flakes" — they were environment-assumption tests
# plus a Windows-only catalog persistence bug, root-caused and fixed on
# 2026-10-07; job logs are fetchable with `gh run view <id> --log-failed`.)
#
# Usage:
#   scripts/check_ci_local.sh          # static checks + generated-data gates + go test -race
#   scripts/check_ci_local.sh --web    # ...also npm ci / tsc / static export (needs Node 20)
#   scripts/check_ci_local.sh --quick  # ...skip `go test -race` (use while iterating)
#
# Windows runs the same test suite without -race (see ci.yml); on Windows use
# Git Bash or run the commands below individually.
set -euo pipefail
cd "$(dirname "$0")/.."

stage() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAILED at stage: %s\n' "$1" >&2; exit 1; }

WEB=0
QUICK=0
for arg in "$@"; do
  case "$arg" in
    --web) WEB=1 ;;
    --quick) QUICK=1 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

stage "gofmt"
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "The following files are not gofmt-formatted:"
  echo "$unformatted"
  fail "gofmt"
fi

stage "go vet ./..."
go vet ./... || fail "go vet"

stage "shell script syntax (bash -n scripts/*.sh)"
bash -n scripts/*.sh || fail "shell syntax"

stage "python byte-compile (scripts/*.py)"
python3 -m py_compile scripts/*.py || fail "py_compile"

stage "node entry points"
node --check npm/bin/litespm.js || fail "node check (wrapper)"
node --check npm/scripts/install-binary.js || fail "node check (installer)"

stage "npm wrapper and installer tests"
mkdir -p dist
go build -o "dist/$(node -p "require('./npm/bin/litespm.js').getBinaryName()")" ./cmd/litespm || fail "build for npm test"
(cd npm && npm test) || fail "npm test"

stage "dataset reproduces the released tree"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
go run ./cmd/litespm catalog build \
  --materialize --out "$tmp/release-check" \
  --prev web/public/v1/current.json || fail "catalog build --materialize"
go run ./cmd/litespm catalog build --verify "$tmp/release-check" || fail "catalog build --verify"

# Re-runs the generator and requires the tree to come back byte-identical.
# It MODIFIES web/data + web/lib in place when they were stale — inspect with
# `git diff` before discarding anything.
stage "generated host data is current"
go run scripts/gen_hosts_ts.go || fail "gen_hosts_ts"
git diff --exit-code -- web/data/hosts.json web/data/skill-targets.json web/lib/hosts.ts \
  || fail "generated host data (re-run 'go run scripts/gen_hosts_ts.go' and commit the result)"

stage "python catalog provenance check"
python3 scripts/build_full_catalog.py --check || fail "build_full_catalog --check"

if [ "$QUICK" -eq 0 ]; then
  stage "go test -race ./..."
  go test -race ./... || fail "go test -race"
else
  stage "go test -race ./... (skipped: --quick)"
fi

if [ "$WEB" -eq 1 ]; then
  stage "web: npm ci / tsc / static export"
  (cd web && npm ci && npx tsc --noEmit && npm run build) || fail "web build"
  test -f web/out/index.html || fail "web/out/index.html missing"
fi

printf '\nAll local CI stages passed.\n'
