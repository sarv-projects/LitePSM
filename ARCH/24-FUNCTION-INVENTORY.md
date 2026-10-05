# Function Inventory — Aspirational Map

> **This document is not an API reference.** The previous revision presented itself as a
> normative list of public Go signatures for all 21 internal packages. Most of those signatures
> **did not exist**. It has been downgraded to an aspirational map. The authoritative API is the
> source tree: `go doc ./internal/<pkg>` (or `go doc -all ./internal/<pkg>` for methods).
>
> **One section matches the tree: §21 `internal/agent`**, whose signatures were confirmed against
> `go doc`. It is retained below as the reference example. Every other section's code blocks have
> been removed because their signatures (`CanonicalizeJSON(value any)`, `ValidateListing`,
> `OpenStateDB`, `CompileRelease` as originally shown, `Engine.Evaluate` as originally shown, and
> ~150 others) were fabrications, not source.
>
> Rank of this document: **informative / `DESIGNED`**. It creates no contract. It must never be
> cited to argue a function exists.

---

## 1. Actual entry points per package

The names below were read from `go doc -short ./internal/<pkg>` at `ff0a1db`. They are entry
**types and package-level symbols**, not a signature contract; a name being listed here does not
mean the capability it implies is wired (see [STATUS.md](../STATUS.md)).

| # | Package | Real exported entry points (types / package funcs) | Old inventory status |
|---|---|---|---|
| 1 | `internal/domain` | `ParseCapabilityID`, `ParseComponentID`, `CanonicalizeJSON(input []byte)`, `ComputeDigest`, `ComputePlanHash`, `ComputeSchemaFingerprint`, `ErrorCode`, `IsRetryable`; types `InstallPlan`, `Listing`, `LPSMError`, `CapabilityGrant`, … | **Superseded** — old signatures wrong (`CanonicalizeJSON(value any)`, `ValidateListing`, `ValidateInstallPlan`, `ValidateEffect` do not exist) |
| 2 | `internal/config` | types `Config`, `PlatformPaths`, `PolicyConfig`, `NetworkConfig`, … (behaviour through `PlatformPaths` methods in `paths.go`) | **Superseded** — `LoadUserConfig`/`MergeConfig`/`ValidateConfig`/`RedactConfigForDisplay` not present as listed |
| 3 | `internal/state` | type `DB` (+ methods), `Migration`, `OperationRecord`, `OperationTree`, `ProviderConfig`, `RecoverySummary` | **Superseded** — `OpenStateDB`, `StateDB`, `MigrateStateDB`, `BeginWriteTx` do not exist; the type is `DB` |
| 4 | `internal/ipc` | `DialIPC`, `ListenIPC`; types `Client`, `Server`, `RPCError`, `HandlerFunc`, `LineDelimitedCodec` | **Superseded** — `ResolveEndpoint`, `EnsureDaemonRunning`, `AuthorizeLocalPeer`, `DispatchRequest`, `CancelRequest` not present as package funcs |
| 5 | `internal/catalog` | type `Client`; `SearchIndex`, `SearchOptions`, `SearchResult`, `SyncResult` | **Superseded** — `FetchCurrentPointer`/`FetchReleaseManifest`/`RefreshCatalog`/`PruneCache` are not the API; sync/search are `Client` methods |
| 6 | `internal/catalogbuild` | `CompileRelease(releaseID string, sequence int, …) (*BuildOutput, error)`; types `BuildOutput`, `CurrentPointer`, `ReleaseManifest`, `ReleaseManifestFile` | **Superseded** — the listed `LoadSourceConfigs`/`NormalizeSnapshot`/`PublishDist`/`BuildSearchIndex` are not the compiler API |
| 7 | `internal/source` | `PublisherForSource`; adapter types `MCPRegistryAdapter`, `AgentSkillsAdapter`, `ClaudeMarketplaceAdapter`, `CodexMarketplaceAdapter`, `CursorMarketplaceAdapter`, `GrokMarketplaceAdapter`, `OpenAIPluginAdapter`, `ACPAgentAdapter`, `FlexSource` | **Superseded** — `GetSourceAdapter`/`FetchSourcePage`/`PersistRawSourceRecord` not present as listed |
| 8 | `internal/artifact` | `ComputeCanonicalTreeDigest`, `ExtractArchiveSafely`, `ExtractArchiveSafelyWithLimits`, `ExtractFileSafely`, `SpoolDownloadBounded`; `ExtractionLimits` | Partially matches; old block omitted the real `…WithLimits`/`…FileSafely` entry points |
| 9 | `internal/resolver` | `Resolver`, `Comparator`, `Constraint`, `Version`, `ListingProvider`, `ListingVersionMetadata` | **Superseded** — `ResolveInstallRequest`, `DetectDependencyCycle`, `BuildInstallPlan`, `RenderPlanSummary` not present as listed |
| 10 | `internal/install` | type `Engine`; `InstallOptions`, `ArchiveSourceFunc`, `TreeSourceFunc` | **Superseded** — `PrepareInstall`/`ExecuteInstall`/`StageInstall` are not the API; the engine takes source funcs |
| 11 | `internal/host` | `AtomicWriteFile`, `CreateAtomicBackup`, `PlanRemoval`, `RegisterAdapter`; adapters `ClaudeCodeAdapter`, `ClineAdapter`, `CodexAdapter`, `GrokBuildAdapter`, `OpenCodeAdapter`, `PiAgentAdapter`, `GenericAdapter`; `BridgeTarget` | **Superseded** — `GetAdapter`/`PlanHostSetup`/`ApplyHostSetup`/`VerifyHostSetup` not present as listed |
| 12 | `internal/skills` | `AgentIDs`, `AgentInstalled`, `AgentSkillDir`, `HostSkillDir`, `CopySkillDir`, `LedgerPath`, `RenderFullPrompt`, `RenderProgressiveIndex`, `SanitizeSkillName`; types `Installer`, `Ledger`, `SkillPackage`, `SkillSource` | **Superseded** — `ValidateSkillTree`/`ListInstalledSkills`/`LoadSkillBody` etc. are not package funcs as listed |
| 13 | `internal/secrets` | `DefaultVaultPath`, `ResolveLaunchSecrets`; `SecretStore`, `MemorySecretStore`, `FileEncryptedSecretStore`, `SecretRef` | **Superseded** — `OpenSecretStore` and the `(s *Store)` methods do not exist |
| 14 | `internal/auth` | `VerifyPKCE`; `AuthBroker`, `AuthDescription`, `AuthSession`, `LoopbackListener`, `PKCEPair` | **Superseded** — `DescribeAuth`/`StartAuth`/`CompleteAuth` are not package funcs as listed |
| 15 | `internal/provider` | types `Supervisor`, `ProviderHandle`, `InvocationRequest`, `InvocationResult`, `RingBuffer`, `StartReport`; `ConfiguredProvider` | **Superseded** — `EnsureProviderReady`/`StartProvider`/`InvokeCapability`/`ApplyRestartBackoff` are not package funcs |
| 16 | `internal/mcpclient` | `FingerprintSchema`; `ClientSession`, `StdioClient`, `StreamableHTTPClient`, `LegacyClient`, `ToolDefinition`, `ToolResult`, `DriftReport` | **Superseded** — `ConnectStdio`/`ConnectStreamableHTTP`/`ValidateToolInput` not present as listed |
| 17 | `internal/bridge` | `FormatInstalledPanel`; type `Shim`; `MCPTool`, `MCPToolResult`, `CapabilityItem` | **Superseded** — `ServeStdio`/`BuildBridgeToolSet`/the `Handle*` family are not package funcs as listed |
| 18 | `internal/doctor` | `ApplyRepairPlan`; types `Engine`, `DoctorReport`, `CheckResult`, `RepairPlan`, `CheckStatus`, `Category` | **Superseded** — `RunChecks`/`CheckStateDB`/`BuildRepairPlan` not present as package funcs |
| 19 | `internal/policy` | `NewEngine`, `(e *Engine) Evaluate`; `PolicyInput`, `PolicyDecision`, `DenyRule`, `CanonicalEffect`, `DecisionKind` | **Superseded** — there is one `Evaluate` on `Engine`, not the four `EvaluateX` package funcs listed |
| 20 | `internal/update` | `TargetBinaryName`, `VerifySelfBoot`; types `Updater`, `UpdateStatus`, `ReleaseInfo` | **Superseded** — `NewUpdater`/`CheckForUpdate`/`ApplyUpdate` are methods on `Updater`, not as listed |
| 21 | `internal/agent` | see §21 below | **Matches** |

### Removed / non-existent packages

*   `internal/approval` and `internal/audit`: do not exist. Approval consumption lives in
    `internal/state`; audit events live in the `state.audit_events` table.
*   `internal/connector`: **deleted** under decision `D1` (zero production importers). It remains
    a design record only in [29](29-CONNECTOR-SYSTEM-DESIGN.md). No functions to list.

---

## 21. Package: `internal/agent` (the one section that matches the tree)

*Test file:* `internal/agent/agent_test.go`

```go
const RegistryURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"
const DefaultMaxRegistryBytes = 8 << 20

func IsDeprecated(id string) bool
func ParseRegistry(data []byte) (*Registry, error)
func MatchTarget(goos, goarch string) (Target, bool)
func HostTarget() (Target, bool)
func NewACPAdapter() *ACPAdapter
func FetchRegistry(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (*Registry, error)
func LoadRegistryFile(path string) (*Registry, error)

type ACPAdapter struct{}
func (a *ACPAdapter) Supports(ag Agent) bool
func (a *ACPAdapter) Resolve(ag Agent, target Target, binaryDir string) (*LaunchSpec, error)

type Registry struct{}
func (r *Registry) FindAgent(id string) (*Agent, bool)
```

Confirmed against `go doc ./internal/agent` at `ff0a1db`.

---

## Authority

When this document and the tree disagree, the tree wins. Regenerate the true inventory with:

```bash
for p in $(ls internal); do echo "== $p"; go doc -short ./internal/$p; done
```
