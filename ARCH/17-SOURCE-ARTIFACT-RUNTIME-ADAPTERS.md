# Source, Artifact & Runtime Adapters

## 1. Architectural Decoupling

To prevent security vulnerabilities and architectural confusion, LiteSPM strictly isolates the interfaces for **metadata ingestion**, **binary retrieval**, and **process execution**:

```text
┌────────────────────────────────────────────────────────────────────────┐
│ 1. SourceAdapter (CI Build-Time Metadata Engine)                       │
│    Reads upstream registries (MCP Registry, Agent Skills, Marketplaces)│
│    Emits normalized Listing schemas; NEVER downloads package binaries. │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Publishes Listing JSON
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 2. ArtifactFetcher (Client-Side Byte Retrieval)                        │
│    Downloads raw archive bytes from declared locator URLs.             │
│    Verifies cryptographic digests; NEVER executes installation scripts.│
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Emits Verified CAS Tree
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 3. RuntimeAdapter (Client-Side Process Execution)                      │
│    Verifies local runtime prerequisites (Node.js, Python, binaries).   │
│    Builds argv LaunchSpec; passes to Provider Supervisor.              │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. SourceAdapter Interface

The `SourceAdapter` interface (`internal/source`) runs during the CI catalog build:

```go
type SourceAdapter interface {
    Descriptor() SourceAdapterDescriptor
    ValidateConfig(config SourceConfig) error
    FetchIndex(ctx context.Context, cursor string) (*SourcePage, error)
    FetchItem(ctx context.Context, upstreamID string, version string) (*RawSourceItem, error)
    Normalize(raw *RawSourceItem) (*domain.Listing, error)
    ResolveVersion(raw *RawSourceItem, requestedVersion string) (*domain.VersionRecord, error)
}
```

### Implementations in v1
*   `MCPRegistryAdapter`: Ingests `server.json` records from `registry.modelcontextprotocol.io`.
*   `AgentSkillsAdapter`: Ingests directory feeds from `agentskills.io` and public GitHub repositories.
*   `ClaudeMarketplaceAdapter`: Parses public `.claude-plugin/marketplace.json` Git manifests (excluding `command` sources).
*   `OpenAIPluginAdapter`: Ingests portable `plugin.json` structures.
*   `GrokMarketplaceAdapter`: Ingests `.grok-plugin/marketplace.json` manifests.

---

## 3. ArtifactFetcher Interface

The `ArtifactFetcher` interface (`internal/artifact`) downloads and verifies archives on the user workstation:

```go
type ArtifactFetcher interface {
    Supports(ref domain.ArtifactRef) bool
    ResolveImmutable(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error)
    Fetch(ctx context.Context, resolved *ResolvedArtifact, destinationFile string) (*FetchResult, error)
    Verify(ctx context.Context, result *FetchResult, expectedDigest string) (*VerifiedArtifact, error)
}
```

### Implementations in v1
*   `HTTPArchiveFetcher`: Fetches tarball/zip over HTTPS; bounds download size to 256 MiB; enforces redirect limits; computes SHA-256 digest in-stream.
*   `GitTreeFetcher`: Clones shallow commit-pinned trees via local Git client; exports tree digest.

---

## 4. RuntimeAdapter Interface

The `RuntimeAdapter` interface (`internal/runtime`) materializes execution environments and constructs child process launch specifications:

```go
type RuntimeAdapter interface {
    Descriptor() RuntimeAdapterDescriptor
    Supports(desc domain.RuntimeDescriptor) bool
    PlanMaterialization(ctx context.Context, comp *domain.Component) (*RuntimePlan, error)
    Materialize(ctx context.Context, plan *RuntimePlan, casTreePath string) (*MaterializedRuntime, error)
    BuildLaunchSpec(ctx context.Context, mat *MaterializedRuntime, secrets map[string]string) (*LaunchSpec, error)
}
```

### Implementations (planned — no `internal/runtime` package exists yet)
*   `RemoteHTTPRuntime`, `NodeStdioRuntime`, `PythonStdioRuntime`, `BinaryStdioRuntime` are the target strategies. Current launch specs are built ad-hoc in `internal/install`, `internal/bridge`, and `internal/host` flows; no `RuntimeAdapter` interface is compiled in-tree. Do not present these as shipped.
