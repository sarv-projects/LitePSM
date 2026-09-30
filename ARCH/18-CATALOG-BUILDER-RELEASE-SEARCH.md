# Catalog Builder, Release Architecture & Search

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
│   Partition Shards (shards/<kind>/<category>.json)                     │
│         │                                                              │
│         ▼                                                              │
│   Generate Compact Search Index (index.json)                           │
│         │                                                              │
│         ▼                                                              │
│   Calculate Manifest SHA-256 Digests (manifest.json)                   │
│         │                                                              │
│         ▼                                                              │
│   Generate Pointer & Release Metadata (/v1/current.json)               │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Public Distribution File Structure

The generated output directory (`dist/`) published to Cloudflare Pages follows an immutable release hierarchy:

```text
dist/
  ├── index.html                                        # Marketplace Web UI
  ├── v1/
  │   ├── current.json                                  # Active release pointer
  │   └── releases/
  │       └── rel_01J9X8K2M4N5P6Q7R8S9T0U1V2/
  │           ├── metadata.json                         # Release info & provenance
  │           ├── manifest.json                         # Digest of every release file
  │           ├── index.json                            # Compact search index
  │           ├── shards/
  │           │   ├── mcp/
  │           │   │   ├── database.json
  │           │   │   └── developer-tools.json
  │           │   └── skill/
  │           │       └── git.json
  │           └── items/
  │               ├── mcp%3Abuiltin%3Apostgres.json
  │               └── skill%3Abuiltin%3Areview-pr.json
```

### 2.1 Manifest Verification (`manifest.json`)
The manifest ensures that clients download authentic, uncorrupted files:
```json
{
  "$schema": "https://litepsm.dev/schemas/v1/release-manifest.schema.json",
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

To avoid runtime database dependencies, clients download the compact `index.json` shard and perform local in-memory lexical filtering.

### 3.1 Deterministic Lexical Scoring Algorithm
Search query terms are evaluated against candidate listings using strict score tiers:

```go
func ScoreListing(query string, candidate SearchIndexItem) int {
    q := strings.ToLower(strings.TrimSpace(query))
    name := strings.ToLower(candidate.Name)
    title := strings.ToLower(candidate.Title)

    // Tier 1: Exact Name / Title Match
    if name == q || title == q {
        return 1000
    }

    // Tier 2: Prefix Match on Name
    if strings.HasPrefix(name, q) {
        return 500
    }

    // Tier 3: Token Match in Name or Title
    tokens := strings.Fields(q)
    score := 0
    for _, t := range tokens {
        if strings.Contains(name, t) || strings.Contains(title, t) {
            score += 100
        }
    }

    // Tier 4: Token Match in Keywords or Categories
    for _, kw := range candidate.Keywords {
        if strings.Contains(strings.ToLower(kw), q) {
            score += 50
        }
    }

    // Tier 5: Token Match in Summary
    summary := strings.ToLower(candidate.Summary)
    for _, t := range tokens {
        if strings.Contains(summary, t) {
            score += 10
        }
    }

    return score
}
```

*   **Popularity is Orthogonal:** Upstream star counts or install metrics are exposed as separate filter facets (`sort: stars`), **never as an automatic relevance multiplier** that overrides exact keyword matches.

---

## 4. Cache Control & CDN Policies

Cloudflare Pages HTTP response headers are strictly configured via `_headers`:

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
