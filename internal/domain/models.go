package domain

import (
	"time"
)

// ListingKind defines catalog entry classifications.
type ListingKind string

const (
	KindPlugin    ListingKind = "plugin"
	KindMCP       ListingKind = "mcp"
	KindSkill     ListingKind = "skill"
	KindConnector ListingKind = "connector" // deprecated: deferred to v2
	KindAgent     ListingKind = "agent"
	KindRule      ListingKind = "rule"
	KindHook      ListingKind = "hook"
	KindTool      ListingKind = "tool"
	KindLSP       ListingKind = "lsp"
)

// ListingStatus represents publication lifecycle status.
type ListingStatus string

const (
	ListingStatusActive      ListingStatus = "active"
	ListingStatusStale       ListingStatus = "stale"
	ListingStatusDeprecated  ListingStatus = "deprecated"
	ListingStatusWithdrawn   ListingStatus = "withdrawn"
	ListingStatusUnavailable ListingStatus = "unavailable"
)

// ArtifactType represents immutable package download formats.
type ArtifactType string

const (
	ArtifactGitTree  ArtifactType = "git-tree"
	ArtifactArchive  ArtifactType = "archive"
	ArtifactNPM      ArtifactType = "npm"
	ArtifactPyPI     ArtifactType = "pypi"
	ArtifactCargo    ArtifactType = "cargo"
	ArtifactOCI      ArtifactType = "oci"
	ArtifactNuGet    ArtifactType = "nuget"
	ArtifactMCPB     ArtifactType = "mcpb"
	ArtifactLocalDir ArtifactType = "local-dir"
)

// FetchPolicy represents download integrity rules.
type FetchPolicy string

const (
	FetchImmutable    FetchPolicy = "immutable"
	FetchDigestPinned FetchPolicy = "digest-pinned"
	FetchHeadRef      FetchPolicy = "head-ref"
	FetchLocalOnly    FetchPolicy = "local-only"
)

// ComponentKind represents runnable or readable sub-elements.
type ComponentKind string

const (
	ComponentSkill           ComponentKind = "skill"
	ComponentMCPProvider     ComponentKind = "mcp-provider"
	ComponentHook            ComponentKind = "hook"
	ComponentCommand         ComponentKind = "command"
	ComponentAgentDefinition ComponentKind = "agent-definition"
	ComponentAgent           ComponentKind = "agent"
	ComponentRule            ComponentKind = "rule"
	ComponentTool            ComponentKind = "tool"
	ComponentLSP             ComponentKind = "lsp"
	ComponentAsset           ComponentKind = "asset"
)

// SupportLevel defines LitePSM support guarantee for a component.
type SupportLevel string

const (
	SupportYes     SupportLevel = "yes"
	SupportPartial SupportLevel = "partial"
	SupportNo      SupportLevel = "no"
	SupportUnknown SupportLevel = "unknown"
)

// InstallScope defines whether an installation is user-wide or workspace-scoped.
type InstallScope string

const (
	ScopeUser    InstallScope = "user"
	ScopeProject InstallScope = "project"
)

// InstallStatus defines lifecycle of an installed component.
type InstallStatus string

const (
	InstallActive   InstallStatus = "active"
	InstallDisabled InstallStatus = "disabled"
	InstallBroken   InstallStatus = "broken"
	InstallRemoved  InstallStatus = "removed"
)

// EffectProvenance describes how side effects were determined.
type EffectProvenance string

const (
	ProvenancePublisherDeclared EffectProvenance = "publisher_declared"
	ProvenanceCurated           EffectProvenance = "curated"
	ProvenanceRuntimeObserved   EffectProvenance = "runtime_observed"
	ProvenanceUserClassified    EffectProvenance = "user_classified"
)

// ApprovalChannel defines origin channel of user authorization.
type ApprovalChannel string

const (
	ApprovalInteractiveCLI ApprovalChannel = "interactive_cli"
	ApprovalAgentBridge    ApprovalChannel = "agent_bridge"
	ApprovalCIPolicy       ApprovalChannel = "ci_policy"
	ApprovalPreapproved    ApprovalChannel = "preapproved_rule"
)

// ApprovalStatus tracks single-use consumption and expiration.
type ApprovalStatus string

const (
	ApprovalActive   ApprovalStatus = "active"
	ApprovalConsumed ApprovalStatus = "consumed"
	ApprovalRevoked  ApprovalStatus = "revoked"
	ApprovalExpired  ApprovalStatus = "expired"
)

// PublisherClaim tracks author attribution.
type PublisherClaim struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Contact string `json:"contact,omitempty"`
}

// SourceReference links a listing to its upstream origin.
type SourceReference struct {
	SourceID   string `json:"sourceId"`
	UpstreamID string `json:"upstreamId"`
	URL        string `json:"url,omitempty"`
}

// VersionSummary gives a concise version entry in a listing.
type VersionSummary struct {
	Version      string     `json:"version"`
	ImmutableRef string     `json:"immutableRef,omitempty"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
}

// ComponentSummary gives a concise component summary in a listing.
type ComponentSummary struct {
	Kind ComponentKind `json:"kind"`
	Name string        `json:"name"`
}

// CompatibilityFact details supported host/environment combinations.
type CompatibilityFact struct {
	HostID      string `json:"hostId"`
	HostVersion string `json:"hostVersion,omitempty"`
	Status      string `json:"status"` // verified | compatible | broken | untrusted
	Notes       string `json:"notes,omitempty"`
}

// VerificationSummary gives safety and signature status.
type VerificationSummary struct {
	Level      string    `json:"level"` // unverified | signature_verified | security_audited
	AuditedAt  time.Time `json:"auditedAt,omitempty"`
	Auditor    string    `json:"auditor,omitempty"`
	ScoreStars int       `json:"scoreStars,omitempty"`
}

// ProvenanceRecord tracks ingestion lineage.
type ProvenanceRecord struct {
	SourceSnapshotID string    `json:"sourceSnapshotId"`
	IngestedAt       time.Time `json:"ingestedAt"`
	CatalogReleaseID string    `json:"catalogReleaseId,omitempty"`
}

// Listing represents a normalized discovery entry in the LitePSM catalog.
type Listing struct {
	SchemaVersion        int                 `json:"schemaVersion"`
	ID                   string              `json:"id"` // ListingId
	Kind                 ListingKind         `json:"kind"`
	Name                 string              `json:"name"`
	Title                string              `json:"title,omitempty"`
	Summary              string              `json:"summary"`
	Description          string              `json:"description,omitempty"`
	Categories           []string            `json:"categories"`
	Keywords             []string            `json:"keywords"`
	PublisherClaim       PublisherClaim      `json:"publisherClaim"`
	Source               SourceReference     `json:"source"`
	Versions             []VersionSummary    `json:"versions"`
	ComponentsSummary    []ComponentSummary  `json:"componentsSummary"`
	RequirementsSummary  []string            `json:"requirementsSummary"`
	CompatibilitySummary []CompatibilityFact `json:"compatibilitySummary"`
	VerificationSummary  VerificationSummary `json:"verificationSummary"`
	Provenance           ProvenanceRecord    `json:"provenance"`
	Status               ListingStatus       `json:"status"`
	RawMetadataRef       string              `json:"rawMetadataRef,omitempty"`
}

// DependencyConstraint specifies version constraints for dependencies.
type DependencyConstraint struct {
	ListingID  string `json:"listingId"`
	Constraint string `json:"constraint"` // semver range
}

// Requirement specifies runtime prerequisites (e.g. node >= 18, python >= 3.10).
type Requirement struct {
	Type    string `json:"type"` // runtime | tool | env | secret
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// PermissionDeclaration lists access scopes requested by a package.
type PermissionDeclaration struct {
	Type        string `json:"type"`
	Target      string `json:"target,omitempty"`
	Description string `json:"description,omitempty"`
}

// TestEvidenceRecord documents test run status.
type TestEvidenceRecord struct {
	HostID     string    `json:"hostId"`
	Passed     bool      `json:"passed"`
	ExecutedAt time.Time `json:"executedAt"`
}

// SignatureRecord represents cryptographic provenance signature.
type SignatureRecord struct {
	KeyID     string `json:"keyId"`
	Signature string `json:"signature"`
	Algorithm string `json:"algorithm"`
}

// RuntimeDescriptor details how to execute a component.
type RuntimeDescriptor struct {
	Type       string            `json:"type"` // stdio | sse | http | inprocess
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Endpoint   string            `json:"endpoint,omitempty"`
	WorkingDir string            `json:"workingDir,omitempty"`
}

// ArtifactRef describes an immutable package download target.
type ArtifactRef struct {
	ArtifactID        string       `json:"artifactId"`
	Type              ArtifactType `json:"type"`
	Locator           string       `json:"locator"`
	ImmutableRef      string       `json:"immutableRef,omitempty"`
	Digest            string       `json:"digest,omitempty"` // sha256:<hex>
	Size              int64        `json:"size,omitempty"`
	MediaType         string       `json:"mediaType,omitempty"`
	Subpath           string       `json:"subpath,omitempty"`
	RuntimeDescriptor string       `json:"runtimeDescriptor,omitempty"`
	FetchPolicy       FetchPolicy  `json:"fetchPolicy"`
}

// EffectDeclaration describes specific side effects.
type EffectDeclaration struct {
	Type       string           `json:"type"` // "fs:read" | "fs:write" | "net:outbound" | "process:exec"
	Target     string           `json:"target,omitempty"`
	Provenance EffectProvenance `json:"provenance"`
	Confidence float32          `json:"confidence"`
}

// Component represents a discrete runnable or readable sub-element within a package.
type Component struct {
	ID                 string              `json:"id"` // ComponentId
	Kind               ComponentKind       `json:"kind"`
	Name               string              `json:"name"`
	Path               string              `json:"path,omitempty"`
	SourceArtifactID   string              `json:"sourceArtifactId,omitempty"`
	HostExtensions     map[string]any      `json:"hostExtensions,omitempty"`
	Runtime            *RuntimeDescriptor  `json:"runtime,omitempty"`
	DeclaredEffects    []EffectDeclaration `json:"declaredEffects"`
	SupportedByLitePSM SupportLevel        `json:"supportedByLitePSM"`
}

// VersionRecord represents a specific release of a listing with resolved artifact pointers.
type VersionRecord struct {
	ListingID           string                  `json:"listingId"`
	Version             string                  `json:"version"`
	ImmutableRef        string                  `json:"immutableRef,omitempty"`
	SourceSnapshotID    string                  `json:"sourceSnapshotId"`
	Artifacts           []ArtifactRef           `json:"artifacts"`
	Components          []Component             `json:"components"`
	Dependencies        []DependencyConstraint  `json:"dependencies"`
	Requirements        []Requirement           `json:"requirements"`
	PermissionsDeclared []PermissionDeclaration `json:"permissionsDeclared"`
	CompatibilityClaims []CompatibilityFact     `json:"compatibilityClaims"`
	TestEvidence        []TestEvidenceRecord    `json:"testEvidence"`
	Signatures          []SignatureRecord       `json:"signatures,omitempty"`
	PublishedAt         *time.Time              `json:"publishedAt,omitempty"`
	FetchedAt           time.Time               `json:"fetchedAt"`
	RawMetadataRef      string                  `json:"rawMetadataRef,omitempty"`
}

// InstallRecord captures an installed component bound to a scope and canonical workspace.
type InstallRecord struct {
	InstallID   string        `json:"installId"`
	ListingID   string        `json:"listingId"`
	Version     string        `json:"version"`
	TreeDigest  string        `json:"treeDigest"`
	Scope       InstallScope  `json:"scope"`
	WorkspaceID string        `json:"workspaceId,omitempty"`
	ProjectRoot string        `json:"projectRoot,omitempty"`
	Status      InstallStatus `json:"status"`
	InstalledAt time.Time     `json:"installedAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}

// InstallComponentRecord maps an install record to individual components.
type InstallComponentRecord struct {
	InstallID     string        `json:"installId"`
	ComponentName string        `json:"componentName"`
	Kind          ComponentKind `json:"kind"`
	Path          string        `json:"path,omitempty"`
	Status        string        `json:"status"`
}

// HostRegistrationRecord tracks integration bindings injected into agent host configurations.
type HostRegistrationRecord struct {
	HostID         string       `json:"hostId"`
	Scope          InstallScope `json:"scope"`
	WorkspaceID    string       `json:"workspaceId"`
	ConfigPath     string       `json:"configPath"`
	ConfigFormat   string       `json:"configFormat"`
	RegisteredAt   time.Time    `json:"registeredAt"`
	LastVerifiedAt time.Time    `json:"lastVerifiedAt"`
	Status         string       `json:"status"`
}

// AuthProfile manages credentials and session tokens for local or remote providers.
type AuthProfile struct {
	ProfileID    string    `json:"profileId"`
	ProviderID   string    `json:"providerId"`
	ProfileType  string    `json:"profileType"`
	SecretRef    string    `json:"secretRef"`
	Status       string    `json:"status"`
	MetadataJSON string    `json:"metadataJson,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// CapabilityGrant represents policy-approved access to a capability.
type CapabilityGrant struct {
	GrantID             string     `json:"grantId"`
	CapabilityID        string     `json:"capabilityId"`
	SchemaFingerprint   string     `json:"schemaFingerprint"`
	CASTreeDigest       string     `json:"casTreeDigest,omitempty"`
	EndpointOrigin      string     `json:"endpointOrigin,omitempty"`
	ServerVersionDigest string     `json:"serverVersionDigest,omitempty"`
	Status              string     `json:"status"`
	ExpiresAt           *time.Time `json:"expiresAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// PlanRequest encapsulates the user's install request.
type PlanRequest struct {
	ListingID          string       `json:"listingId"`
	RequestedVersion   string       `json:"requestedVersion,omitempty"`
	SelectedComponents []string     `json:"selectedComponents,omitempty"`
	TargetScope        InstallScope `json:"targetScope"`
	TargetHost         string       `json:"targetHost,omitempty"`
	WorkspaceID        string       `json:"workspaceId,omitempty"`
	ProjectRoot        string       `json:"projectRoot,omitempty"`
}

// PlanArtifact represents resolved artifact references in an install plan.
type PlanArtifact struct {
	ArtifactID string `json:"artifactId"`
	Type       string `json:"type"`
	Locator    string `json:"locator"`
	Digest     string `json:"digest,omitempty"`
	Size       int64  `json:"size,omitempty"`
}

// PlanResolved contains deterministic outputs of the resolver.
type PlanResolved struct {
	Version             string         `json:"version"`
	ImmutableRefs       []string       `json:"immutableRefs,omitempty"`
	Artifacts           []PlanArtifact `json:"artifacts"`
	Dependencies        []string       `json:"dependencies,omitempty"`
	RuntimeRequirements []Requirement  `json:"runtimeRequirements,omitempty"`
}

// PlanPreconditions lists prerequisites checked before installation.
type PlanPreconditions struct {
	FilesystemPaths []string `json:"filesystemPaths,omitempty"`
	PortsAvailable  []int    `json:"portsAvailable,omitempty"`
	RuntimesFound   []string `json:"runtimesFound,omitempty"`
	DiskSpaceBytes  int64    `json:"diskSpaceBytes,omitempty"`
}

// PlanApproval records user authorization of a plan.
type PlanApproval struct {
	ApprovalID string          `json:"approvalId,omitempty"`
	Channel    ApprovalChannel `json:"channel"`
	Decision   string          `json:"decision"` // approve | reject
	DecidedAt  *time.Time      `json:"decidedAt,omitempty"`
	ApprovedBy string          `json:"approvedBy,omitempty"`
}

// HostChange details atomic modifications proposed for an agent config file.
type HostChange struct {
	HostID     string `json:"hostId"`
	ConfigPath string `json:"configPath"`
	Action     string `json:"action"` // inject_bridge | register_command | remove_entry
	EntryKey   string `json:"entryKey"`
	ValueJSON  string `json:"valueJson,omitempty"`
}

// ProviderLaunch specifies how the daemon launches and supervises a provider.
type ProviderLaunch struct {
	ProviderID string            `json:"providerId"`
	Transport  string            `json:"transport"` // stdio | http
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Endpoint   string            `json:"endpoint,omitempty"`
}

// RequestedAccess describes capabilities and permissions requested by the plan.
type RequestedAccess struct {
	Tools []string `json:"tools,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Hosts []string `json:"hosts,omitempty"`
}

// InstallPlan strictly adheres to schemas/install-plan.schema.json.
type InstallPlan struct {
	SchemaVersion    int               `json:"schemaVersion"`
	PlanID           string            `json:"planId"`
	PlanHash         string            `json:"planHash"`
	CreatedAt        time.Time         `json:"createdAt"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	CatalogReleaseID string            `json:"catalogReleaseId"`
	SourceSnapshots  []string          `json:"sourceSnapshots,omitempty"`
	Request          PlanRequest       `json:"request"`
	Resolved         PlanResolved      `json:"resolved"`
	Effects          []string          `json:"effects"`
	Preconditions    PlanPreconditions `json:"preconditions"`
	Approval         PlanApproval      `json:"approval"`
	HostChanges      []HostChange      `json:"hostChanges,omitempty"`
	ProviderLaunches []ProviderLaunch  `json:"providerLaunches,omitempty"`
	RequestedAccess  *RequestedAccess  `json:"requestedAccess,omitempty"`
}

// ProviderRecord tracks a registered MCP provider daemon process or remote endpoint.
type ProviderRecord struct {
	ProviderID    string    `json:"providerId"`
	InstallID     string    `json:"installId"`
	ComponentName string    `json:"componentName"`
	Transport     string    `json:"transport"`
	Endpoint      string    `json:"endpoint,omitempty"`
	Command       string    `json:"command,omitempty"`
	ArgsJSON      string    `json:"argsJson,omitempty"`
	EnvJSON       string    `json:"envJson,omitempty"`
	WorkingDir    string    `json:"workingDir,omitempty"`
	AuthProfileID string    `json:"authProfileId,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// CapabilityRecord stores individual discovered tools and schemas.
type CapabilityRecord struct {
	CapabilityID      string    `json:"capabilityId"`
	ProviderID        string    `json:"providerId"`
	Name              string    `json:"name"`
	Description       string    `json:"description,omitempty"`
	InputSchemaJSON   string    `json:"inputSchemaJson"`
	SchemaFingerprint string    `json:"schemaFingerprint"`
	DiscoveredAt      time.Time `json:"discoveredAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ExtractedTreeInfo encapsulates output metadata from safe archive extraction.
type ExtractedTreeInfo struct {
	FileCount  int    `json:"fileCount"`
	TotalBytes int64  `json:"totalBytes"`
	TreeDigest string `json:"treeDigest"` // sha256:<merkle-tree-hex>
	RootPath   string `json:"rootPath"`
}

// DependencyResolution records a resolved dependency node in the installation graph.
type DependencyResolution struct {
	ListingID       string         `json:"listingId"`
	SelectedVersion string         `json:"selectedVersion"`
	VersionRecord   *VersionRecord `json:"versionRecord,omitempty"`
	Direct          bool           `json:"direct"`
	Depth           int            `json:"depth"`
}

// DependencyResolutionResult encapsulates the full graph resolution result.
type DependencyResolutionResult struct {
	RootListingID    string                          `json:"rootListingId"`
	SelectedVersions map[string]string               `json:"selectedVersions"`
	TopologicalOrder []string                        `json:"topologicalOrder"`
	ResolvedRanges   map[string]string               `json:"resolvedRanges"`
	Nodes            map[string]DependencyResolution `json:"nodes,omitempty"`
}
