# Domain Model & Canonical Identifiers

## 1. Canonical Identifier Grammars

Display names and titles are mutable and non-unique. All internal operations, database primary keys, and cryptographic hashes rely strictly on canonical, immutable identifier grammars.

```text
┌─────────────────┬──────────────────────────────────────────────────────────────────┐
│ Identifier Type │ Canonical Format & Grammar                                       │
├─────────────────┼──────────────────────────────────────────────────────────────────┤
│ SourceId        │ <namespace>:<slug>                                               │
│                 │ Regex: ^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$                       │
│                 │ Example: builtin:mcp-registry, git:anthropic-official            │
├─────────────────┼──────────────────────────────────────────────────────────────────┤
│ ListingId       │ <kind>:<source-id>:<percent-encoded-upstream-id>                 │
│                 │ Regex: ^(plugin|mcp|skill|connector):[a-z0-9_:-]+:[A-Za-z0-9_.~%+-]+$ │
│                 │ Example: mcp:builtin:mcp-registry:%40modelcontextprotocol%2Fpg   │
├─────────────────┼──────────────────────────────────────────────────────────────────┤
│ ComponentId     │ <listing-id>@<resolved-version>#<kind>/<component-name>          │
│                 │ Example: mcp:builtin:mcp-registry:pg@1.4.0#mcp-provider/server   │
├─────────────────┼──────────────────────────────────────────────────────────────────┤
│ InstallId       │ inst_<26-char-ULID-or-UUIDv7>                                    │
│                 │ Example: inst_01J9X8K2M4N5P6Q7R8S9T0U1V2                         │
├─────────────────┼──────────────────────────────────────────────────────────────────┤
│ CapabilityId    │ <install-id>/<component-name>/<provider-native-tool-name>        │
│                 │ Example: inst_01J9X8K2M4N5P6/server/query_db                     │
└─────────────────┴──────────────────────────────────────────────────────────────────┘
```

---

## 2. Core Domain Entities

### 2.1 Listing
Represents a normalized discovery entry in the LitePSM catalog.
```go
type Listing struct {
    SchemaVersion        int                   `json:"schemaVersion"`
    ID                   string                `json:"id"` // ListingId
    Kind                 ListingKind           `json:"kind"` // plugin | mcp | skill | connector
    Name                 string                `json:"name"`
    Title                string                `json:"title,omitempty"`
    Summary              string                `json:"summary"`
    Description          string                `json:"description,omitempty"`
    Categories           []string              `json:"categories"`
    Keywords             []string              `json:"keywords"`
    PublisherClaim       PublisherClaim        `json:"publisherClaim"`
    Source               SourceReference       `json:"source"`
    Versions             []VersionSummary      `json:"versions"`
    ComponentsSummary    []ComponentSummary    `json:"componentsSummary"`
    RequirementsSummary  []RequirementSummary  `json:"requirementsSummary"`
    CompatibilitySummary []CompatibilityFact   `json:"compatibilitySummary"`
    VerificationSummary  VerificationSummary   `json:"verificationSummary"`
    Provenance           ProvenanceRecord      `json:"provenance"`
    Status               ListingStatus         `json:"status"` // active | stale | deprecated | withdrawn | unavailable
    RawMetadataRef       string                `json:"rawMetadataRef,omitempty"`
}
```

### 2.2 VersionRecord
Represents a specific release of a listing with resolved artifact pointers.
```go
type VersionRecord struct {
    ListingID            string                 `json:"listingId"`
    Version              string                 `json:"version"` // Display version
    ImmutableRef         string                 `json:"immutableRef,omitempty"` // Pinned Git SHA / registry digest
    SourceSnapshotID     string                 `json:"sourceSnapshotId"`
    Artifacts            []ArtifactRef          `json:"artifacts"`
    Components           []Component            `json:"components"`
    Dependencies         []DependencyConstraint `json:"dependencies"`
    Requirements         []Requirement          `json:"requirements"`
    PermissionsDeclared  []PermissionDeclaration`json:"permissionsDeclared"`
    CompatibilityClaims  []CompatibilityFact    `json:"compatibilityClaims"`
    TestEvidence         []TestEvidenceRecord   `json:"testEvidence"`
    Signatures           []SignatureRecord      `json:"signatures,omitempty"`
    PublishedAt          *time.Time             `json:"publishedAt,omitempty"`
    FetchedAt            time.Time              `json:"fetchedAt"`
    RawMetadataRef       string                 `json:"rawMetadataRef,omitempty"`
}
```

### 2.3 ArtifactRef
Describes an immutable package download target.
```go
type ArtifactRef struct {
    ArtifactID        string      `json:"artifactId"`
    Type              ArtifactType `json:"type"` // git-tree | archive | npm | pypi | cargo | oci | nuget | mcpb | local-dir
    Locator           string      `json:"locator"` // URL or relative path
    ImmutableRef      string      `json:"immutableRef,omitempty"` // Pinned commit or registry version
    Digest            string      `json:"digest,omitempty"` // sha256:<hex>
    Size              int64       `json:"size,omitempty"` // Byte count
    MediaType         string      `json:"mediaType,omitempty"`
    Subpath           string      `json:"subpath,omitempty"`
    RuntimeDescriptor string      `json:"runtimeDescriptor,omitempty"`
    FetchPolicy       FetchPolicy `json:"fetchPolicy"`
}
```

### 2.4 Component
A discrete runnable or readable sub-element within a package.
```go
type Component struct {
    ID                string               `json:"id"` // ComponentId
    Kind              ComponentKind        `json:"kind"` // skill | mcp-provider | hook | command | agent-definition | asset
    Name              string               `json:"name"`
    Path              string               `json:"path,omitempty"`
    SourceArtifactID  string               `json:"sourceArtifactId,omitempty"`
    HostExtensions    map[string]any       `json:"hostExtensions,omitempty"`
    Runtime           *RuntimeDescriptor   `json:"runtime,omitempty"`
    DeclaredEffects   []EffectDeclaration  `json:"declaredEffects"`
    SupportedByLitePSM SupportLevel        `json:"supportedByLitePSM"` // yes | partial | no | unknown
}
```

---

## 3. Cryptographic & Canonical Hashing (RFC 8785)

To guarantee that hashes remain byte-identical across platforms, architectures, and programming languages, all object hashing uses **RFC 8785 (JSON Canonicalization Scheme - JCS)** followed by SHA-256.

### 3.1 Plan Hash (`planHash`)
Computed over the canonical JSON of all execution-relevant fields in `InstallPlan`:
```text
planHash = SHA-256( JCS({
  "catalogReleaseId": plan.CatalogReleaseId,
  "effects": plan.Effects,
  "hostChanges": plan.HostChanges,
  "preconditions": plan.Preconditions,
  "providerLaunches": plan.ProviderLaunches,
  "request": plan.Request,
  "requestedAccess": plan.RequestedAccess,
  "resolved": plan.Resolved,
  "schemaVersion": plan.SchemaVersion,
  "sourceSnapshots": plan.SourceSnapshots
}))
```
*Note: Volatile fields (`planId`, `createdAt`, `expiresAt`) are excluded from `planHash`.*

### 3.2 Schema Fingerprint (`schemaFingerprint`)
Computed over a provider tool's input JSON Schema to detect schema drift:
```text
schemaFingerprint = SHA-256( JCS( tool.InputSchema ) )
```
If an upstream server alters its parameter types, adds required parameters, or modifies property descriptions, `schemaFingerprint` changes immediately.
