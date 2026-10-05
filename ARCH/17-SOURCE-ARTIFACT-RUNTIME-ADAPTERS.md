# Source, Artifact & Runtime Adapters

Status: **Normative for the `source.Adapter` / `internal/artifact` contract; `DESIGNED` for the artifact-fetcher and runtime-adapter split below.**

This document separates three concerns: **metadata ingestion** (build-time, source adapters), **byte retrieval** (client-side), and **process execution** (client-side). Only the first is compiled today. The other two are specified as target interfaces and must not be described as implemented.

---

## 1. Architectural Decoupling

```text
┌────────────────────────────────────────────────────────────────────────┐
│ 1. source.Adapter (metadata ingestion — COMPILED)                      │
│    Reads upstream registries/feeds and emits normalized domain records │
│    (Listing, VersionRecord). NEVER downloads package bytes or runs code│
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ normalized Listings / VersionRecords
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 2. Artifact Fetcher (byte retrieval — DESIGNED)                        │
│    Target: download raw archive bytes from declared locators.          │
│    Verify cryptographic digests; NEVER execute install scripts.        │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ verified bytes / CAS tree
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 3. Runtime Adapter (process execution — DESIGNED)                      │
│    Target: check local runtime prerequisites; build argv LaunchSpec.   │
│    Hand off to the Provider Supervisor.                                │
└────────────────────────────────────────────────────────────────────────┘
```

The separation is load-bearing: a source adapter that can fetch and execute bytes is a supply-chain hazard. Preserve the invariant that ingestion is data-only.

---

## 2. Source Adapter Interface (compiled)

The real interface lives in `internal/source/adapter.go`:

```go
// IngestResult encapsulates the normalized domain records produced by a source adapter.
type IngestResult struct {
    SourceID   domain.SourceID
    SnapshotID string
    Listings   []*domain.Listing
    Versions   []*domain.VersionRecord
    ItemCount  int
    Digest     string
    IngestedAt time.Time
}

// Adapter defines the interface for an upstream catalog/registry ingestion adapter.
// Source adapters only parse and normalize discovery metadata; they perform zero package execution.
type Adapter interface {
    SourceID() domain.SourceID
    Ingest(ctx context.Context, snapshotID string) (*IngestResult, error)
}
```

### 2.1 Implementations (eight)

Eight adapters are compiled in `internal/source`. Three satisfy the two-method `Adapter` interface above; five marketplace adapters accept an **extra raw-manifest byte slice** because the caller (the build pipeline) supplies the already-fetched manifest:

```go
// Marketplace adapters (extra parameter; do NOT satisfy source.Adapter verbatim).
Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error)
```

| Adapter | File | Interface shape |
|---|---|---|
| `MCPRegistryAdapter` | `mcp_registry.go` | `Adapter` (`Ingest(ctx, snapshotID)`) |
| `AgentSkillsAdapter` | `skills.go` | `Adapter` (`Ingest(ctx, snapshotID)`) |
| `ACPAgentAdapter` | `acp_registry.go` | `Adapter` (`Ingest(ctx, snapshotID)`) |
| `ClaudeMarketplaceAdapter` | `claude_marketplace.go` | extra `rawManifest []byte` |
| `CodexMarketplaceAdapter` | `codex_marketplace.go` | extra `rawManifest []byte` |
| `CursorMarketplaceAdapter` | `cursor_marketplace.go` | extra `rawManifest []byte` |
| `GrokMarketplaceAdapter` | `grok_marketplace.go` | extra `rawManifest []byte` |
| `OpenAIPluginAdapter` | `openai_plugin.go` | extra `rawManifest []byte` |

`internal/source/sources.go` holds the checked-in registry of known upstream sources (`KnownSources`, `LookupSource`, `PublisherForSource`); it is data, not an adapter. Every adapter sets `VerificationSummary.Level = "unverified"` at ingestion — no signature or attestation check runs there. See [27 — Capability & Source Support Matrix](27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md) for the per-adapter status and gaps.

### 2.2 Normative constraints

*   Adapters stay statically compiled; no dynamic plugin loading.
*   An adapter MUST NOT download package bytes, resolve dependencies, or execute scripts.
*   An adapter MUST NOT set a verification level higher than `unverified` without a real, attributable check. See [26 §12.4](26-ECOSYSTEM-IA-PACKAGE-MODEL.md).

---

## 3. Artifact Layer (`internal/artifact`) — compiled exports

No `ArtifactFetcher` interface exists. `internal/artifact/extractor.go` currently exports the following real functions, all of which operate on bytes already supplied by the caller:

| Export | Purpose |
|---|---|
| `SpoolDownloadBounded(r, maxBytes, tempFile)` | Streams bytes while enforcing a size bound; returns byte count and `sha256:…` digest. |
| `ExtractFileSafely(archivePath, archiveType, stagingDir, limits)` | Opens and safely extracts an archive on disk under explicit limits. |
| `ExtractArchiveSafely(r, size, archiveType, stagingDir)` | Safe extraction with production default limits. |
| `ExtractArchiveSafelyWithLimits(...)` | Safe extraction with caller-supplied `ExtractionLimits`. |
| `ComputeCanonicalTreeDigest(treeDir)` | Deterministic CAS tree digest. |

`ExtractionLimits` bounds archive size (256 MiB), total tree size (1 GiB), file count (20 000), single-file size (128 MiB), and path length (1 024). Archive-slip and size violations fail closed with typed `domain` errors. This layer is storage/extraction, not retrieval: something above it must supply the bytes.

---

## 4. Artifact Fetcher Interface — `DESIGNED`

Target interface (no `internal/artifact` fetcher type is compiled today; do not present this as shipped):

```go
type ArtifactFetcher interface {
    Supports(ref domain.ArtifactRef) bool
    ResolveImmutable(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error)
    Fetch(ctx context.Context, resolved *ResolvedArtifact, destinationFile string) (*FetchResult, error)
    Verify(ctx context.Context, result *FetchResult, expectedDigest string) (*VerifiedArtifact, error)
}
```

Target strategies: `HTTPArchiveFetcher` (HTTPS tarball/zip, bounded via `SpoolDownloadBounded`, redirect limit, streaming SHA-256) and `GitTreeFetcher` (shallow commit-pinned clone, canonical tree digest). Both must be data-only: they MUST NOT execute package code. The lockfile ([32](32-MANIFEST-LOCK-INTEROP.md)) records the artifact SHA-256 and CAS tree digest this layer is required to reproduce.

---

## 5. Runtime Adapter Interface — `DESIGNED`

Target interface (no `internal/runtime` package exists today):

```go
type RuntimeAdapter interface {
    Descriptor() RuntimeAdapterDescriptor
    Supports(desc domain.RuntimeDescriptor) bool
    PlanMaterialization(ctx context.Context, comp *domain.Component) (*RuntimePlan, error)
    Materialize(ctx context.Context, plan *RuntimePlan, casTreePath string) (*MaterializedRuntime, error)
    BuildLaunchSpec(ctx context.Context, mat *MaterializedRuntime, secrets map[string]string) (*LaunchSpec, error)
}
```

Target strategies: `RemoteHTTPRuntime`, `NodeStdioRuntime`, `PythonStdioRuntime`, `BinaryStdioRuntime`. Today launch specs are constructed ad hoc in `internal/install`, `internal/bridge`, and `internal/host`, and the provider supervisor consumes the `LaunchSpec` shape in [14 §2.2](14-BRIDGE-PROVIDER-MCP.md). The runtime adapter is the missing seam that would make materialisation and launch-spec construction testable and provider-agnostic.

---

## 6. Precedence & Related Documents

*   Ingestion contract and per-source status: [03 — Catalog Sources](03-CATALOG-SOURCES.md), [27 — Capability & Source Support Matrix](27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md).
*   Client install/materialise path: [04 — Client & Installation](04-CLIENT-INSTALL.md), [13 — Resolver & Install Engine](13-RESOLVER-INSTALL-ENGINE.md).
*   Provider supervision and the `LaunchSpec` contract: [14 — Bridge, Provider Supervisor & MCP](14-BRIDGE-PROVIDER-MCP.md).
*   Runtime invocation semantics on top of the supervisor: [34 — Runtime, Invocation & Receipts](34-RUNTIME-INVOCATION-RECEIPTS.md).
