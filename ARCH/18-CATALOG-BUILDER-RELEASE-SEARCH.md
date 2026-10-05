# Catalog Builder, Release Architecture & Search

> **Honesty note (added 2026-10-05).** Sections 1, 2, and 4 of this document describe a target
> output shape that the current builder does **not** produce, and that is not published.
>
> *   `CompileRelease` emits exactly four files: `v1/current.json` and
>     `v1/releases/<id>/{listings,versions,manifest}.json`
>     (`internal/catalogbuild/compiler.go:102,118,151,172`). It does **not** emit `index.json`,
>     `shards/<kind>/<category>.json`, `items/`, or `metadata.json` as drawn in §1 and §2.
> *   `CompileRelease` has **no non-test caller**. Nothing publishes the `/v1/releases/` tree, so
>     `litespm catalog sync` cannot complete against the live origin: `/v1/current.json` returns 200
>     while the release files it names return 404.
> *   The `_headers` cache policy in §4 **is** implemented — `scripts/deploy-pages.sh:31-42` writes it
>     into the staging directory at deploy time. It is a build artifact under the gitignored
>     `pages-dist/`, so it is absent from the source tree. §4 needs no correction.
> *   `scripts/build_full_catalog.py` is what actually produces the deployed catalog
>     (`web/data/catalog.json`, `web/public/v1/current.json`, `web/data/release.json`), which
>     contradicts locked decision D4 ("Go `catalogbuild` is the single builder").
> *   The live `web/public/v1/current.json` also disagrees with the Go `CurrentPointer` struct: its
>     `manifestDigest` is the digest of `catalog.json` rather than `manifest.json`, it has no
>     `schemaVersion`, and it carries advisory/count fields the Go struct does not declare.
>
> The normative path contract in §2 (`/v1/releases/...`) **is** what the client now requests, and
> `TestReleasePathContractPinsDocumentedLayout` in `internal/catalog/catalog_test.go` pins it against
> a server that serves only the documented tree. See
> [31 — Competitive Landscape & Roadmap](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) §4.2–§4.4.
> Until the publisher lands, treat §1, §2 and §4 as `DESIGNED`, not `WIRED`.

## 1. Deterministic CI Catalog Builder

The catalog builder (`internal/catalogbuild`) compiles raw source snapshots into public static distribution assets. The build process is deterministic: given the same input snapshots and build version, the compiler produces byte-for-byte identical output.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Catalog Build Pipeline                          │
│                                                                        │
│   Ingest Source Snapshots (MCP Registry, Agent Skills, Marketplaces)   │
│         │                                                              │
│         ▼                                                              │
│   Deduplicate Listings (Identify Exact Digest Collisions)              │
│         │                                                              │
│         ▼                                                              │
│   Validate Domain Schemas (Draft 2020-12 Strict JSON Schema)           │
│         │                                                              │
│         ▼                                                              │
│   Partition Shards (shards/<kind>/<category>.json)          [DESIGNED] │
│         │                                                              │
│         ▼                                                              │
│   Generate Compact Search Index (index.json)                [DESIGNED] │
│         │                                                              │
│         ▼                                                              │
│   Calculate Manifest SHA-256 Digests (manifest.json)                   │
│         │                                                              │
│         ▼                                                              │
│   Generate Pointer & Release Metadata (/v1/current.json)               │
└────────────────────────────────────────────────────────────────────────┘
```

**`DESIGNED` stages are not emitted.** The compiled `CompileRelease` emits four files: `v1/current.json` and `v1/releases/<id>/{listings,versions,manifest}.json`. The shard/index/items/metadata stages above and the tree in §2 are the target shape, pending the release publisher tracked in [31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md).

---

## 2. Public Distribution File Structure

The generated output directory (`dist/`) published to Cloudflare Pages follows an immutable release
hierarchy (**target shape — see the honesty note at the top of this document; `index.json`,
`shards/`, `items/` and `metadata.json` are not emitted today, and only the
`v1/releases/<id>/{manifest,listings,versions}.json` subset is part of the client contract**):

```text
dist/
  ├── index.html                                        # Marketplace Web UI
  ├── v1/
  │   ├── current.json                                  # Active release pointer
  │   └── releases/
  │       └── rel_01J9X8K2M4N5P6Q7R8S9T0U1V2/
  │           ├── metadata.json                         # release info & provenance [DESIGNED]
  │           ├── manifest.json                         # digest of every release file (emitted)
  │           ├── listings.json                         # emitted
  │           ├── versions.json                         # emitted
  │           ├── index.json                            # compact search index        [DESIGNED]
  │           ├── shards/                               #                             [DESIGNED]
  │           │   ├── mcp/
  │           │   │   ├── database.json
  │           │   │   └── developer-tools.json
  │           │   └── skill/
  │           │       └── git.json
  │           └── items/                                #                             [DESIGNED]
  │               ├── mcp%3Abuiltin%3Apostgres.json
  │               └── skill%3Abuiltin%3Areview-pr.json
```

### 2.1 Manifest Verification (`manifest.json`)
The manifest ensures that clients download authentic, uncorrupted files:
```json
{
  "$schema": "https://litespm.dev/schemas/v1/release-manifest.schema.json",
  "releaseId": "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2",
  "files": {
    "index.json": {
      "size": 48210,
      "digest": "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069"
    },
    "shards/mcp/database.json": {
      "size": 18230,
      "digest": "sha256:4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b"
    }
  }
}
```

---

## 3. Client Search Index & Deterministic Ranking

The local search index is `internal/catalog/search.go`. Downloading a compact `index.json` shard and filtering it in the browser is the `DESIGNED` client path (see §1/§2); the weights below are the **real, compiled scoring weights** and any future client index MUST reuse them so local and client ranking agree.

### 3.1 Deterministic Lexical Scoring Algorithm

`scoreListing` (unexported in `internal/catalog/search.go`) computes an additive score; there is no early-return tier that caps the score. The real weights are:

```go
func scoreListing(l *domain.Listing, queryNorm string, queryTokens []string) (float64, []string) {
    var score float64
    var matchedFields []string
    nameLower  := strings.ToLower(l.Name)
    titleLower := strings.ToLower(l.Title)
    summaryLower := strings.ToLower(l.Summary)
    idLower    := strings.ToLower(l.ID)

    // Exact canonical-ID match
    if idLower == queryNorm {
        score += 100.0
    }
    // Exact name, else prefix
    if nameLower == queryNorm {
        score += 50.0
    } else if strings.HasPrefix(nameLower, queryNorm) {
        score += 25.0
    }
    // Per query token, substring matches accumulate:
    for _, token := range queryTokens {
        if strings.Contains(nameLower, token)    { score += 10.0 }
        if titleLower != "" && strings.Contains(titleLower, token) { score += 8.0 }
        if summaryLower != "" && strings.Contains(summaryLower, token) { score += 4.0 }
        for _, cat := range l.Categories { // first matching category only
            if strings.Contains(strings.ToLower(cat), token) { score += 6.0; break }
        }
        for _, kw := range l.Keywords {    // first matching keyword only
            if strings.Contains(strings.ToLower(kw), token) { score += 5.0; break }
        }
    }
    // Dormant verification bonus (only when score > 0): security_audited +10, signature_verified +5.
    return score, matchedFields
}
```

Summary of real weights: exact ID `+100`; exact name `+50`; name prefix `+25`; per token — name `+10`, title `+8`, summary `+4`, category `+6`, keyword `+5`. Results sort by score descending, then name ascending; the default limit is 20.

*   **Popularity is Not a Signal:** No upstream source in the current matrix exposes star, download or install counts, so the catalog carries no popularity data. No popularity facet or sort mode (`sort: stars`) is exposed, and no popularity term may multiply relevance — scoring is the lexical weights above alone (ARCH/26 §12.4).

> Implementation note (honesty gap): `internal/catalog/search.go` also adds a verification bonus (`security_audited` +10, `signature_verified` +5) when `score > 0`. No adapter currently emits those levels (all emit `unverified`, and the catalog carries no audit evidence), so the bonus is dormant. Do not populate those levels — or rely on the bonus — until a real signature/audit check produces evidence (ARCH/26 §12.4). Do not introduce a second scoring table for the client index.

---

## 4. Cache Control & CDN Policies

Cloudflare Pages HTTP response headers are strictly configured via `_headers`, which
`scripts/deploy-pages.sh:31-42` generates into the staging directory at deploy time (a build
artifact under the gitignored `pages-dist/`, not a committed source file):

```text
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
```

---

## 5. Public Dist Allowlist (CI Leak Protection)

The GitHub Actions release workflow publishes strictly from a staging allowlist directory:
1.  Copies only `*.json`, `*.html`, `*.css`, `*.js`, `*.svg` from `dist/`.
2.  Explicitly verifies that no Git directories (`.git/`), environment files (`.env`), private source configs, or test fixtures are included in the upload bundle.
