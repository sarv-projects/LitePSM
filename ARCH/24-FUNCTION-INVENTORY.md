# Function Inventory & Module Signatures

This document lists the public Go function signatures, inputs, outputs, error conditions, and designated test files for all 21 internal packages.

> Note: there is no `internal/approval` or `internal/audit` package. Approval consumption lives in `internal/state` (`ConsumeApproval`) and is evaluated by `internal/policy`; audit events live in `state.audit_events` via `RecordAuditEvent`. Section 10 below is retained as a pointer so old references do not 404.

---

## 1. Package: `internal/domain`
*Test file:* `internal/domain/domain_test.go`

```go
func ParseSourceID(raw string) (SourceID, error)
func ParseListingID(raw string) (ListingID, error)
func ParseComponentID(raw string) (ComponentID, error)
func NormalizeUpstreamID(raw string) string
func ComputeDigest(r io.Reader) (string, error)
func CanonicalizeJSON(value any) ([]byte, error)
func ComputePlanHash(plan *InstallPlan) (string, error)
func ComputeSchemaFingerprint(jsonSchema []byte) (string, error)
func ValidateListing(l *Listing) error
func ValidateVersionRecord(v *VersionRecord) error
func ValidateInstallPlan(p *InstallPlan) error
func ValidateEffect(effect string) error
```

---

## 2. Package: `internal/config`
*Test file:* `internal/config/config_test.go`

```go
func ResolvePlatformPaths() (*PlatformPaths, error)
func LoadUserConfig(path string) (*UserConfig, error)
func LoadProjectConfig(projectRoot string) (*ProjectConfig, error)
func LoadEnvironmentOverrides() (*ConfigOverrides, error)
func MergeConfig(defaults, user, project, env, cli *Config) (*Config, error)
func ValidateConfig(cfg *Config) error
func RedactConfigForDisplay(cfg *Config) string
```

---

## 3. Package: `internal/state`
*Test file:* `internal/state/state_test.go`

```go
func OpenStateDB(dbPath string) (*StateDB, error)
func (s *StateDB) MigrateStateDB(targetVersion int) error
func (s *StateDB) CheckIntegrity(ctx context.Context) error
func (s *StateDB) BeginWriteTx(ctx context.Context) (*WriteTx, error)
func (s *StateDB) GetOperation(ctx context.Context, id string) (*Operation, error)
func (s *StateDB) CreateOperation(ctx context.Context, op *Operation) error
func (s *StateDB) AdvanceOperationState(ctx context.Context, id, newState string) error
func (s *StateDB) RecordOperationStep(ctx context.Context, step *OperationStep) error
func (s *StateDB) PutPlan(ctx context.Context, plan *domain.InstallPlan) error
func (s *StateDB) GetPlan(ctx context.Context, planID string) (*domain.InstallPlan, error)
func (s *StateDB) PutApproval(ctx context.Context, a *domain.Approval) error
func (s *StateDB) ConsumeApproval(ctx context.Context, approvalID string) error
func (s *StateDB) PutInstall(ctx context.Context, inst *domain.InstallRecord) error
func (s *StateDB) GetInstall(ctx context.Context, installID string) (*domain.InstallRecord, error)
func (s *StateDB) ListInstalls(ctx context.Context, filter domain.InstallFilter) ([]domain.InstallRecord, error)
func (s *StateDB) PutProvider(ctx context.Context, prov *domain.ProviderRecord) error
func (s *StateDB) PutCapabilitiesSnapshot(ctx context.Context, caps []domain.Capability) error
func (s *StateDB) PutCapabilityGrant(ctx context.Context, grant *domain.CapabilityGrant) error
func (s *StateDB) RevokeCapabilityGrant(ctx context.Context, grantID string) error
func (s *StateDB) PutHostRegistration(ctx context.Context, reg *domain.HostRegistration) error
func (s *StateDB) GetHostRegistration(ctx context.Context, hostID, scope, workspaceID string) (*domain.HostRegistration, error)
func (s *StateDB) PutAuthProfile(ctx context.Context, profile *domain.AuthProfile) error
func (s *StateDB) GetAuthProfile(ctx context.Context, profileID string) (*domain.AuthProfile, error)
func (s *StateDB) ListAuthProfiles(ctx context.Context, providerID string) ([]domain.AuthProfile, error)
func (s *StateDB) RevokeAuthProfile(ctx context.Context, profileID string) error
func (s *StateDB) RecordOperationTree(ctx context.Context, opID, treeDigest string, createdByOp bool) error
func (s *StateDB) WasTreeCreatedByOperation(ctx context.Context, opID, treeDigest string) (bool, error)
func (s *StateDB) AppendAuditEvent(ctx context.Context, ev *domain.AuditEvent) error
```

---

## 4. Package: `internal/ipc`
*Test file:* `internal/ipc/ipc_test.go`

```go
func ResolveEndpoint() (string, error)
func EnsureDaemonRunning(ctx context.Context) error
func Connect(ctx context.Context, endpoint string) (*Client, error)
func (c *Client) Handshake(ctx context.Context, params HandshakeParams) (*HandshakeResult, error)
func Serve(ctx context.Context, endpoint string, handler RPCHandler) error
func AuthorizeLocalPeer(conn net.Conn) error
func DispatchRequest(ctx context.Context, req JSONRPCRequest) JSONRPCResponse
func CancelRequest(id uint64) error
func ShutdownGracefully(ctx context.Context) error
```

---

## 5. Package: `internal/catalog`
*Test file:* `internal/catalog/catalog_test.go`

```go
func FetchCurrentPointer(ctx context.Context, baseURL string) (*CatalogPointer, error)
func FetchReleaseMetadata(ctx context.Context, baseURL, releaseID string) (*CatalogRelease, error)
func FetchReleaseManifest(ctx context.Context, baseURL, releaseID string) (*ReleaseManifest, error)
func VerifyReleaseFiles(manifest *ReleaseManifest, releaseDir string) error
func AcceptRelease(ctx context.Context, release *CatalogRelease) error
func LoadCachedRelease(cacheDir, releaseID string) (*CachedRelease, error)
func Search(ctx context.Context, idx *SearchIndex, query string, filters SearchFilters) ([]SearchResult, error)
func GetListing(ctx context.Context, releaseID, listingID string) (*domain.Listing, error)
func GetVersion(ctx context.Context, releaseID, listingID, ver string) (*domain.VersionRecord, error)
func ResolveLatestDisplayVersion(listing *domain.Listing) (string, error)
func RefreshCatalog(ctx context.Context, baseURL string) (*CatalogPointer, error)
func PruneCache(cacheDir string, maxReleases int) error
```

---

## 6. Package: `internal/catalogbuild`
*Test file:* `internal/catalogbuild/catalogbuild_test.go`

```go
func LoadSourceConfigs(configDir string) ([]SourceConfig, error)
func SyncSource(ctx context.Context, src SourceConfig) (*domain.SourceSnapshot, error)
func NormalizeSnapshot(ctx context.Context, snap *domain.SourceSnapshot) ([]domain.Listing, error)
func MergeNormalizedListings(snapshots ...[]domain.Listing) ([]domain.Listing, error)
func DetectExactDuplicates(listings []domain.Listing) ([]domain.Listing, error)
func ValidateCatalog(listings []domain.Listing) []error
func BuildSearchIndex(listings []domain.Listing) (*SearchIndex, error)
func BuildItemFiles(outDir string, listings []domain.Listing) error
func BuildManifest(releaseDir string) (*ReleaseManifest, error)
func BuildReleaseMetadata(releaseID string, seq int) (*CatalogRelease, error)
func PublishDist(stagingDir, distDir string) error
```

---

## 7. Package: `internal/source`
*Test file:* `internal/source/source_test.go`

```go
func RegisterBuiltInAdapters()
func GetSourceAdapter(sourceType string) (SourceAdapter, error)
func ValidateSourceConfig(cfg SourceConfig) error
func FetchSourcePage(ctx context.Context, adapter SourceAdapter, cursor string) (*SourcePage, error)
func FetchSourceItem(ctx context.Context, adapter SourceAdapter, upstreamID, ver string) (*RawSourceItem, error)
func NormalizeSourceItem(adapter SourceAdapter, raw *RawSourceItem) (*domain.Listing, error)
func ResolveSourceVersion(adapter SourceAdapter, raw *RawSourceItem, reqVer string) (*domain.VersionRecord, error)
func PersistRawSourceRecord(cacheDir string, raw *RawSourceItem) (string, error)
```

---

## 8. Package: `internal/artifact`
*Test file:* `internal/artifact/artifact_test.go`

```go
func ResolveArtifact(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error)
func FetchArtifact(ctx context.Context, resolved *ResolvedArtifact, destFile string) (*FetchResult, error)
func ValidateHTTPRedirect(req *http.Request, via []*http.Request) error
func ValidateNetworkDestination(ip net.IP) error
func VerifyArtifactDigest(filePath, expectedDigest string) error
func InspectArchive(filePath string) (*ArchiveInspection, error)
func ValidateArchiveEntry(entryName string, size uint64) error
func ExtractArchiveSafely(r io.ReaderAt, size int64, stagingDir string) (*domain.ExtractedTreeInfo, error)
func ComputeTreeDigest(treeDir string) (string, error)
func CommitImmutableTree(stagingDir, casDir, digest string) error
func PruneUnreferencedArtifacts(casDir string, activeDigests []string) error
```

---

## 9. Package: `internal/resolver`
*Test file:* `internal/resolver/resolver_test.go`

```go
func ResolveInstallRequest(ctx context.Context, req domain.InstallRequest) (*domain.InstallPlan, error)
func SelectExactVersion(listing *domain.Listing, verConstraint string) (*domain.VersionRecord, error)
func ResolveComponents(ver *domain.VersionRecord, requested []string) ([]domain.Component, error)
func ResolveDependencies(ctx context.Context, root *domain.Listing, targetVer string) ([]domain.DependencyResolution, error)
func DetectDependencyCycle(graph map[string][]string) (bool, []string)
func ResolveArtifacts(components []domain.Component) ([]domain.ArtifactRef, error)
func ResolveRuntimeRequirements(components []domain.Component) ([]domain.Requirement, error)
func ComputeEffects(components []domain.Component) []domain.EffectDeclaration
func ComputeRequestedAccess(components []domain.Component) []domain.AccessDeclaration
func DetectCollisions(ctx context.Context, plan *domain.InstallPlan) ([]domain.Collision, error)
func ComputePreconditions(ctx context.Context, plan *domain.InstallPlan) ([]domain.Precondition, error)
func BuildInstallPlan(req domain.InstallRequest, res *ResolutionResult) (*domain.InstallPlan, error)
func RenderPlanSummary(plan *domain.InstallPlan) string
```

---

## 10. Approval logic (no separate package — lives in `internal/state` + `internal/policy`)
*Test files:* `internal/state/state_test.go`, `internal/policy/policy_test.go`

```go
// internal/state (repositories.go, operations.go)
func (db *DB) RecordApproval(ctx context.Context, approvalID, subjectType, subjectHash, actor, channel, scope string, expiresAt *time.Time) error
func (db *DB) ConsumeApproval(ctx context.Context, approvalID string) error
func (db *DB) RevokeApproval(ctx context.Context, approvalID string) error
func (db *DB) SaveCapabilityGrant(ctx context.Context, grant *domain.CapabilityGrant, grantedBy string) error
func (db *DB) GetActiveGrant(ctx context.Context, capabilityID, schemaFingerprint string) (*domain.CapabilityGrant, error)
```

---

## 11. Package: `internal/policy`
*Test file:* `internal/policy/policy_test.go`

```go
func LoadPolicySet(configDir string) (*PolicySet, error)
func EvaluateInstall(ctx context.Context, input domain.PolicyInput) domain.PolicyDecision
func EvaluateProviderStart(ctx context.Context, input domain.PolicyInput) domain.PolicyDecision
func EvaluateCapabilityInvocation(ctx context.Context, input domain.PolicyInput) domain.PolicyDecision
func EvaluateHostMutation(ctx context.Context, input domain.PolicyInput) domain.PolicyDecision
func MatchRules(rules []PolicyRule, input domain.PolicyInput) (*PolicyRule, error)
func ExplainDecision(decision domain.PolicyDecision) string
```

---

## 12. Package: `internal/install`
*Test file:* `internal/install/install_test.go`

```go
func PrepareInstall(ctx context.Context, req domain.InstallRequest) (*domain.InstallPlan, error)
func ExecuteInstall(ctx context.Context, planID, approvalToken string) (*domain.InstallResult, error)
func PrepareUpdate(ctx context.Context, installID, targetVer string) (*domain.InstallPlan, error)
func ExecuteUpdate(ctx context.Context, planID, approvalToken string) (*domain.InstallResult, error)
func PrepareRemove(ctx context.Context, installID string) (*domain.RemovalPlan, error)
func ExecuteRemove(ctx context.Context, planID, approvalToken string) error
func StageInstall(ctx context.Context, op *Operation) error
func CommitInstall(ctx context.Context, op *Operation) error
func RollbackOperation(ctx context.Context, op *Operation) error
func RecoverIncompleteOperations(ctx context.Context) error
func GarbageCollectUnreferencedTrees(ctx context.Context) (int64, error)
```

---

## 13. Package: `internal/host`
*Test file:* `internal/host/host_test.go`

```go
func GetAdapter(hostID string) (HostAdapter, error)
func ProbeHost(ctx context.Context, hostID string) (*HostProbe, error)
func PlanHostSetup(ctx context.Context, hostID string, scope Scope) (*HostChangePlan, error)
func ApplyHostSetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
func VerifyHostSetup(ctx context.Context, hostID string) (*HostVerification, error)
func DetectHostConfigDrift(ctx context.Context, hostID string) (bool, error)
func PlanHostRemoval(ctx context.Context, hostID string) (*HostChangePlan, error)
func ApplyHostRemoval(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
func RenderManualHostInstructions(hostID string) (string, error)
```

---

## 14. Package: `internal/skills`
*Test file:* `internal/skills/skills_test.go`

```go
func ValidateSkillTree(treeDir string) (*SkillValidation, error)
func ReadSkillMetadata(treeDir string) (*domain.SkillMetadata, error)
func ListInstalledSkills(ctx context.Context) ([]domain.SkillSummary, error)
func LoadSkillBody(ctx context.Context, skillID string) (string, error)
func ListSkillResources(ctx context.Context, skillID string) ([]string, error)
func ReadSkillResource(ctx context.Context, skillID, relativePath string) ([]byte, error)
```

---

## 15. Package: `internal/secrets`
*Test file:* `internal/secrets/secrets_test.go`

```go
func OpenSecretStore() (SecretStore, error)
func (s *Store) PutSecret(ctx context.Context, namespace, key string, secretBytes []byte) (*SecretRef, error)
func (s *Store) GetSecret(ctx context.Context, ref SecretRef) ([]byte, error)
func (s *Store) DeleteSecret(ctx context.Context, ref SecretRef) error
func (s *Store) SecretExists(ctx context.Context, ref SecretRef) (bool, error)
func ResolveLaunchSecrets(ctx context.Context, provID string) (map[string]string, error)
```

---

## 16. Package: `internal/auth`
*Test file:* `internal/auth/auth_test.go`

```go
func DescribeAuth(ctx context.Context, provID string) (*AuthDescription, error)
func StartAuth(ctx context.Context, provID string) (*AuthSession, error)
func HandleOAuthCallback(ctx context.Context, sessionID, code, state string) (*AuthResult, error)
func CompleteAuth(ctx context.Context, sessionID string) (*AuthProfile, error)
func RefreshAuth(ctx context.Context, profileID string) error
func RevokeAuth(ctx context.Context, profileID string) error
func GetAuthStatus(ctx context.Context, provID string) (AuthStatus, error)
```

---

## 17. Package: `internal/provider`
*Test file:* `internal/provider/provider_test.go`

```go
func EnsureProviderReady(ctx context.Context, provID string) (*ProviderHandle, error)
func StartProvider(ctx context.Context, provID string) (*ProviderSession, error)
func StopProvider(ctx context.Context, provID string) error
func RestartProvider(ctx context.Context, provID string) error
func ProbeProvider(ctx context.Context, provID string) (*CapabilitiesSnapshot, error)
func NegotiateProtocol(ctx context.Context, prov *ProviderHandle) (string, error)
func RefreshCapabilities(ctx context.Context, provID string) error
func GetCapabilities(ctx context.Context, provID string) ([]domain.Capability, error)
func InvokeCapability(ctx context.Context, req InvocationRequest) (*InvocationResult, error)
func CancelInvocation(ctx context.Context, invocationID string) error
func GetInvocation(ctx context.Context, invocationID string) (*InvocationStatus, error)
func HandleProviderExit(provID string, exitCode int, err error)
func ApplyRestartBackoff(provID string) time.Duration
```

---

## 18. Package: `internal/mcpclient`
*Test file:* `internal/mcpclient/mcpclient_test.go`

```go
func ConnectStdio(ctx context.Context, spec LaunchSpec) (ClientSession, error)
func ConnectStreamableHTTP(ctx context.Context, endpoint string, headers map[string]string) (ClientSession, error)
func ConnectLegacySSE(ctx context.Context, endpoint string) (ClientSession, error)
func (s *Session) ListTools(ctx context.Context) ([]ToolDefinition, error)
func (s *Session) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error)
func (s *Session) SubscribeToListChanges(ctx context.Context, ch chan<- ListChangeEvent) error
func ValidateToolInput(schema json.RawMessage, input json.RawMessage) error
func ValidateToolOutput(schema json.RawMessage, output json.RawMessage) error
func PropagateCancellation(ctx context.Context, requestID string) error
func (s *Session) CloseSession() error
```

---

## 19. Package: `internal/bridge`
*Test file:* `internal/bridge/bridge_test.go`

```go
func ServeStdio(ctx context.Context, hostID string) error
func HandshakeWithDaemon(ctx context.Context) error
func BuildBridgeToolSet(hostProfile HostProfile) []mcp.Tool
func HandleSearchCatalog(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleGetExtension(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandlePrepareInstall(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleRequestInstall(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleListInstalled(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleSearchCapabilities(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleDescribeCapability(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleLoadSkill(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleReadSkillResource(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleInvokeCapability(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleGetInvocation(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func HandleCancelInvocation(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error)
func MapDaemonErrorToMCP(err error) mcp.ToolResult
```

---

## 20. Package: `internal/doctor` (audit events live in `internal/state`)
*Test file:* `internal/doctor/doctor_test.go`

```go
// internal/state
func (db *DB) RecordAuditEvent(ctx context.Context, actor, action, targetRef, decision, approvalID, opID, outcome, metadataJSON string) error

// internal/doctor
func RunChecks(ctx context.Context, scope Scope) (*DoctorReport, error)
func CheckStateDB(ctx context.Context) CheckResult
func CheckOperationJournal(ctx context.Context) CheckResult
func CheckArtifacts(ctx context.Context) CheckResult
func CheckHostRegistrations(ctx context.Context) CheckResult
func CheckSecretRefs(ctx context.Context) CheckResult
func CheckProviderRequirements(ctx context.Context) CheckResult
func CheckCatalogCache(ctx context.Context) CheckResult
func BuildRepairPlan(report *DoctorReport) (*RepairPlan, error)
func ApplyRepairPlan(ctx context.Context, plan *RepairPlan) error
```

---

## 21. Package: `internal/agent`
*Test file:* `internal/agent/agent_test.go`

```go
func ParseRegistry(data []byte) (*Registry, error)
func MatchTarget(goos, goarch string) (Target, bool)
func HostTarget() (Target, bool)
func NewACPAdapter() *ACPAdapter
func (a *ACPAdapter) Supports(ag Agent) bool
func (a *ACPAdapter) Resolve(ag Agent, target Target, binaryDir string) (*LaunchSpec, error)
func LoadRegistryFile(path string) (*Registry, error)
func FetchRegistry(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (*Registry, error)
func (r *Registry) FindAgent(id string) (*Agent, bool)
func IsDeprecated(id string) bool
```

---

## 22. Package: `internal/connector` — removed (not implemented)

No such package exists in the tree. The earlier local execution core was deleted as unreachable code (no production importer, no wiring path); local proxy execution remains a design record only ([29](29-CONNECTOR-SYSTEM-DESIGN.md)). No live functions to list.

---

## 23. Package: `internal/update`
*Test file:* `internal/update/update_test.go`

```go
func NewUpdater(baseURL string) *Updater
func TargetBinaryName() string
func (u *Updater) CheckForUpdate(ctx context.Context, currentVersion string) (*UpdateStatus, *ReleaseInfo, error)
func (u *Updater) ApplyUpdate(ctx context.Context, newBinaryBytes []byte, expectedSHA256 string, targetBinaryPath string, stagingDir string) error
func VerifySelfBoot(binaryPath string) error
```
