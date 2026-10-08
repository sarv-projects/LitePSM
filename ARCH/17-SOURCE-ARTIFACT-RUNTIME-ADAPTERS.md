# Source, Artifact & Runtime Adapters

Status: **Normative for the `source.Adapter` / `internal/artifact` contract; `IMPLEMENTED` for `HTTPArchiveFetcher` and the `internal/runtime` launch-planning registry — both compile and are unit-tested, neither has a non-test caller (`WIRED` is false). `DESIGNED` remains for the unwritten `GitTreeFetcher` strategy and for runtime materialisation.**

This document separates three concerns: **metadata ingestion** (build-time, source adapters), **byte retrieval** (client-side), and **process execution** (client-side). All three are compiled today; **none of them is `WIRED`** — the source adapters have test-only callers (`ARCH/03` §1), and the fetcher and runtime packages have no non-test caller at all. Roles 2 and 3 are `IMPLEMENTED` — types and tests exist, no production code calls them — and must not be described as live.

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
│ 2. Artifact Fetcher (byte retrieval — IMPLEMENTED, unwired)            │
│    HTTPArchiveFetcher: downloads raw archive bytes from                │
│    a declared locator, verifies digests, never runs scripts.           │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ verified bytes / CAS tree
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 3. Runtime Adapter (process execution — IMPLEMENTED, unwired)          │
│    internal/runtime: probes toolchains (PATH lookup) and               │
│    plans an argv Launch; hands off to the supervisor.                  │
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

`internal/artifact/extractor.go` exports the following functions, all of which operate on bytes
already supplied by the caller:

| Export | Purpose |
|---|---|
| `SpoolDownloadBounded(r, maxBytes, tempFile)` | Streams bytes while enforcing a size bound; returns byte count and `sha256:…` digest. |
| `ExtractFileSafely(archivePath, archiveType, stagingDir, limits)` | Opens and safely extracts an archive on disk under explicit limits. |
| `ExtractArchiveSafely(r, size, archiveType, stagingDir)` | Safe extraction with production default limits. |
| `ExtractArchiveSafelyWithLimits(...)` | Safe extraction with caller-supplied `ExtractionLimits`. |
| `ComputeCanonicalTreeDigest(treeDir)` | Deterministic CAS tree digest. |

`ExtractionLimits` bounds archive size (256 MiB), total tree size (1 GiB), file count (20 000), single-file size (128 MiB), and path length (1 024). Archive-slip and size violations fail closed with typed `domain` errors. This layer is storage/extraction, not retrieval: something above it must supply the bytes.

---

## 4. Artifact Fetcher Interface — `HTTPArchiveFetcher` implemented

`HTTPArchiveFetcher` is compiled in `internal/artifact/fetcher.go` and matches the interface below;
`GitTreeFetcher` (shallow commit-pinned clone) is still `DESIGNED`.

```go
type ArtifactFetcher interface {
    Supports(ref domain.ArtifactRef) bool
    ResolveImmutable(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error)
    Fetch(ctx context.Context, resolved *ResolvedArtifact, destinationFile string) (*FetchResult, error)
    Verify(ctx context.Context, result *FetchResult, expectedDigest string) (*VerifiedArtifact, error)
}
```

`HTTPArchiveFetcher` implements the ARCH/03 §5 rules the repository previously lacked: HTTPS only,
no credentials in URLs, SSRF refusal of loopback/link-local/private/unspecified/multicast addresses
at dial time (checking the concrete IP, so DNS cannot be re-pointed between check and connect),
redirect cap, no https→http downgrade, request timeout, `SpoolDownloadBounded` size cap, streaming
SHA-256, and fail-closed verification (an artifact with no expected digest is refused). It is
data-only: it never executes or interprets the payload; extraction stays in `extractor.go`.

**No production caller yet.** Nothing supplies it an `ArtifactRef`: the published catalog carries
no artifact locators (`catalogbuild` emits `Artifacts: []`), and the client index does not expose
version artifact refs. Its end-to-end behaviour is pinned by
`internal/artifact/fetcher_test.go` and `test/archive_install_e2e_test.go` (download → verify →
extract → CAS install). Wiring it in is gated on the catalog carrying artifact locators.

Target strategy still to be written: `GitTreeFetcher` (shallow commit-pinned clone, canonical tree
digest). It is data-only and MUST NOT execute package code. The lockfile
([32](32-MANIFEST-LOCK-INTEROP.md)) records the artifact SHA-256 and CAS tree digest this layer is
required to reproduce.

---

## 5. Runtime Adapter Interface — launch planning `IMPLEMENTED`, materialisation `DESIGNED`

The package **does** exist: `internal/runtime/runtime.go` (its package doc cites this section)
compiles the model (`Kind` for node/python/native/oci/remote, `Spec`, `Launch`), a three-method
`Adapter` interface, a `Registry` with five adapters (`NodeAdapter`, `PythonAdapter`,
`NativeAdapter`, `OCIAdapter`, `RemoteAdapter`), and the digest-pin rule for OCI. Availability
probing is PATH lookup only and `Plan` never executes package code. Its sole caller is
`internal/runtime/runtime_test.go`, so the package is `IMPLEMENTED`, not `WIRED`.

The richer interface this document originally specified — the half that would own
materialisation — is still `DESIGNED`:

```go
type RuntimeAdapter interface {
    Descriptor() RuntimeAdapterDescriptor
    Supports(desc domain.RuntimeDescriptor) bool
    PlanMaterialization(ctx context.Context, comp *domain.Component) (*RuntimePlan, error)
    Materialize(ctx context.Context, plan *RuntimePlan, casTreePath string) (*MaterializedRuntime, error)
    BuildLaunchSpec(ctx context.Context, mat *MaterializedRuntime, secrets map[string]string) (*LaunchSpec, error)
}
```

Target strategies: `RemoteHTTPRuntime`, `NodeStdioRuntime`, `PythonStdioRuntime`, `BinaryStdioRuntime`. In production today launch specs are still constructed ad hoc in `internal/install`, `internal/bridge`, and `internal/host` — nothing calls `internal/runtime` — and the provider supervisor consumes the `LaunchSpec` shape in [14 §2.2](14-BRIDGE-PROVIDER-MCP.md). Wiring this package in (and giving it materialisation) is the seam that would make materialisation and launch-spec construction testable and provider-agnostic.

---

## 6. Precedence & Related Documents

*   Ingestion contract and per-source status: [03 — Catalog Sources](03-CATALOG-SOURCES.md), [27 — Capability & Source Support Matrix](27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md).
*   Client install/materialise path: [04 — Client & Installation](04-CLIENT-INSTALL.md), [13 — Resolver & Install Engine](13-RESOLVER-INSTALL-ENGINE.md).
*   Provider supervision and the `LaunchSpec` contract: [14 — Bridge, Provider Supervisor & MCP](14-BRIDGE-PROVIDER-MCP.md).
*   Runtime invocation semantics on top of the supervisor: [34 — Runtime, Invocation & Receipts](34-RUNTIME-INVOCATION-RECEIPTS.md).
