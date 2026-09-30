# Catalog and source federation

## Source model

LitePSM is an **aggregator and compatibility layer**, not the owner of every upstream package. Each catalog record points to its publisher/registry source. LitePSM may cache normalized public metadata for search, but it should not silently replace the publisher's artifact or claim authority over it.

```text
source adapters ──► validated source records ──► normalized listings
      │                                           │
      ├─ official registry API                    ├─ web catalog
      ├─ documented directory API                 ├─ static JSON API
      ├─ Git marketplace manifest                 ├─ discovery MCP
      └─ user-added local/private source           └─ install plan
```

## Upstream sources to support

| Source family | Initial approach | Important limit |
|---|---|---|
| Official MCP Registry | Consume its documented registry API and preserve its server/version identifiers and package metadata. | It catalogs MCP server metadata, not every package type or the MCP runtime. |
| Public Agent Skills directories | Consume documented search/detail APIs where available; otherwise use the skill's public Git source/manifest. Agent Skills folder format remains the artifact contract. | Counts and popularity are upstream signals, not quality or safety proof. |
| Claude-format plugin marketplaces | Accept a user-supplied Git repository or marketplace manifest and parse the documented `.claude-plugin/marketplace.json` format. Index selected public repositories only when access is allowed. | There is no assumed global public marketplace API. Marketplace entries may point to private repositories and require the user's Git credentials. |
| Codex/OpenAI plugin packages and marketplaces | Parse the portable Agent Plugins manifest and documented local/repository marketplace formats. Provide a source entry for the universal public directory only if a documented public feed/API is available. | The existence of a product directory does not grant LitePSM an API or permission to scrape it. Some plugin capabilities are host-specific. |
| Grok plugin marketplaces | Parse `.grok-plugin/marketplace.json` and its optional generated component index; support remote Git sources pinned to full commit SHA. Also test Claude-format marketplace compatibility where documented. | Marketplace catalogs are Git sources; an index record does not mean every contained hook, agent, or MCP feature is portable. |
| Generic Git/HTTP/local sources | Accept explicit source URLs and recognized manifest formats. Support private sources through the user's own local Git credential helper. | LitePSM never receives or stores a user's Git token. A private source cannot be public-searchable unless the publisher separately publishes metadata. |
| Connector directories | Treat a connector as a user-facing app listing with one or more provider implementations, initially MCP or a plugin dependency. | Do not claim a universal connector registry or implement each SaaS API. Add a native provider only after an explicit need and decision. |

The exact source facts and primary references are recorded in [09 — Research ledger](09-RESEARCH.md).

## Normalized Listing

Normalization is a **search/display projection**, not an attempt to erase upstream semantics. Preserve the original manifest and source reference (or a safe public subset) beside normalized fields.

```text
Listing
  id: stable LitePSM id (namespace/source + upstream id)
  kind: plugin | mcp | skill | connector
  name, summary, categories, keywords
  publisher identity claim + source URL
  upstream format + manifest version
  source locator + immutable ref when available
  versions[] with content digest and publication time
  components[] (skills, MCP configs, hooks, agents, commands, assets)
  requirements[] (client capabilities, runtime, auth method)
  permissions[] (filesystem/process/network/API access declarations)
  host compatibility claims[] and LitePSM test evidence[]
  provenance: imported-from, source timestamp, fetched-at
  original metadata / unknown fields
```

Unknown source fields must be retained in the raw record or explicitly marked unsupported; ingestion must not silently discard them and imply full format compatibility.

## Identity and duplicate handling

- Stable identity includes source namespace and upstream identifier; display name alone is never identity.
- Exact artifact duplicates may be grouped when publisher/source and content digest prove sameness; show all source routes.
- Forks and repackages remain distinct records, with duplicate/related links where evidence supports them.
- Versions are immutable references where source allows: Git commit SHA, registry version plus integrity digest, or published archive digest.
- Mutable branches/tags may be browsable but are not reproducible install targets until resolved to an immutable reference.

## Compatibility model

Compatibility is component- and host-specific, not a single percentage:

```text
skill files             supported / unsupported / unknown
MCP transport/auth      supported / unsupported / unknown
plugin manifest         supported / partial / unsupported / unknown
hooks                  supported only after explicit adapter and trust review
agent definitions      supported only for recognized schemas
host-specific apps      not portable unless a corresponding host integration exists
```

Keep four separate facts: **listed**, **source-verified**, **format-compatible**, and **tested**. “Verified” must identify what was verified (publisher identity, artifact digest, protocol behavior, or a host test); never use a lone green badge for all of them.

## Collection plan: 500–1,000 useful records

Reach catalog breadth through federation, not manual rewriting:

1. Import the Official MCP Registry's public metadata.
2. Index the official Agent Skills format through documented skills feeds/APIs and direct public Git sources.
3. Add selected public Git plugin marketplaces with user-visible source attribution.
4. Allow users/organizations to register additional marketplaces without publishing their private listings globally.
5. Deduplicate with source IDs/digests, track last-seen time, and flag stale/unavailable entries.
6. Curate a smaller tested set separately; an imported listing does not enter the tested set automatically.

The numeric target refers to searchable listings, not bespoke integrations, hosted runtimes, or quality certification.

## Ingestion lifecycle

```text
configured source
   → fetch documented index/API
   → bound bytes/time/count and validate schema
   → preserve source URI, source version, and fetch timestamp
   → normalize to Listing while retaining unknown data
   → deduplicate only with evidence
   → security/format checks and optional host tests
   → generate static search/index/item JSON
   → publish after CI validation / review
```

Ingestion must not execute a listed server, plugin, script, or hook. User-submitted metadata is untrusted input and must be escaped in the website.
