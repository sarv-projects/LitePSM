package connector

// manifest.go — the connector manifest: a signed, installable artifact that
// describes how to reach one third-party service and what credentials it needs.
//
// Design notes, not decoration:
//   - Auth vocabulary is borrowed, not invented. Scheme type names follow
//     OpenAPI Security Scheme types; OAuth field names follow RFC 7591 /
//     RFC 8414 / RFC 9728. An MCP host can consume this file with minimal
//     translation, and a reviewer can check every field against a public spec.
//   - `schemaVersion` and `reach` are ours. No published standard covers "which
//     transports does this connector speak" or "which version of this file is
//     this", so those two fields are explicitly LitePSM vocabulary.
//   - A manifest MUST NOT carry credential material. Not the token, not the
//     client secret, not an example that looks real enough to try. Validation
//     rejects the file if it does, because a connector manifest is
//     remote-code-adjacent input: only an explicit human install action moves
//     one from "discovered" to "installed".
//
// What this file does not do: store anything, refresh anything, or touch the
// network. It is a pure description plus a validator.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ManifestSchemaVersion is the only schema version this package reads.
const ManifestSchemaVersion = 1

// AuthSchemeType is a closed enum. Every new auth type is a reviewed host
// change, never arbitrary manifest content.
type AuthSchemeType string

const (
	AuthOAuth2 AuthSchemeType = "oauth2"
	AuthAPIKey AuthSchemeType = "apiKey"
	AuthHTTP   AuthSchemeType = "http"
	AuthBasic  AuthSchemeType = "basic"
)

// ReachKind names exactly one way to reach the service.
type ReachKind string

const (
	ReachMCP     ReachKind = "mcp"
	ReachOpenAPI ReachKind = "openapi"
)

// ConnectorManifest is the on-disk / on-wire description of one connector.
type ConnectorManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Version       string `json:"version"`

	Repository ManifestRepository `json:"repository"`
	Reach      ConnectorReach     `json:"reach"`

	Auth   ConnectorAuth    `json:"auth"`
	Inputs []ConnectorInput `json:"inputs,omitempty"`

	Annotations ConnectorAnnotations `json:"annotations,omitempty"`
}

// ManifestRepository is mandatory provenance: who published this, where.
type ManifestRepository struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

// ConnectorReach holds exactly one transport description.
type ConnectorReach struct {
	MCP     *MCPReach     `json:"mcp,omitempty"`
	OpenAPI *OpenAPIReach `json:"openapi,omitempty"`
}

// MCPReach describes a Model Context Protocol server entry point.
type MCPReach struct {
	Command []string          `json:"command,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// OpenAPIReach points at a machine-readable API description.
type OpenAPIReach struct {
	Ref     string `json:"ref"`
	BaseURL string `json:"baseUrl"`
}

// ConnectorAuth groups the schemes a service accepts.
type ConnectorAuth struct {
	Schemes []AuthScheme `json:"schemes"`
}

// AuthScheme is one way to authenticate to the service.
type AuthScheme struct {
	Type AuthSchemeType `json:"type"`

	// IsSecret marks whether material for this scheme lives in the vault.
	// It is always true for credential schemes; the field exists so a future
	// non-secret scheme (e.g. a public app id) can be expressed honestly.
	IsSecret bool `json:"secret"`

	// Scopes splits read from write. A connector that only needs reads
	// declares only reads; write consent is always a separate step.
	Scopes ScopeGroups `json:"scopes,omitempty"`

	// ClientMetadata uses RFC 7591 registration field names verbatim.
	ClientMetadata map[string]string `json:"clientMetadata,omitempty"`

	// ResourceMetadataURL is the RFC 9728 protected-resource metadata URL.
	ResourceMetadataURL string `json:"resourceMetadataUrl,omitempty"`

	// TokenURL and AuthorizeURL are the OAuth endpoints. Public facts about
	// the provider, not secrets.
	TokenURL     string `json:"tokenUrl,omitempty"`
	AuthorizeURL string `json:"authorizeUrl,omitempty"`
}

// ScopeGroups separates read scopes from write scopes.
type ScopeGroups struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

// ConnectorInput is one non-secret configuration value the connector needs,
// e.g. a workspace id or region. Secret inputs are never declared here — they
// are implied by the auth scheme and live only in the vault.
type ConnectorInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Required    bool     `json:"is_required"`
	IsSecret    bool     `json:"is_secret"`
	Choices     []string `json:"choices,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// ConnectorAnnotations carries MCP ToolAnnotations-aligned hints.
type ConnectorAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

var (
	// manifestIDRe pins the reverse-dns/name shape. A manifest id is an
	// address in our catalog, not a display string.
	manifestIDRe = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)+/[a-z0-9][a-z0-9._-]*$`)
	// semverRe accepts an exact version only. Ranges are refused: a connector
	// resolves to bytes, and "bytes" means one digest, not a range.
	semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	// credentialShapedKeys is the normative blocklist. Any of these keys
	// carrying a non-empty value anywhere in the manifest fails validation.
	credentialShapedKeys = []string{
		"clientsecret", "client_secret", "apikey", "api_key", "accesstoken",
		"access_token", "refreshtoken", "refresh_token", "password",
		"privatekey", "private_key", "token", "secret", "bearer",
	}
)

// ParseManifest decodes and validates a connector manifest.
func ParseManifest(data []byte) (*ConnectorManifest, error) {
	var m ConnectorManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("connector manifest is not valid JSON: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate enforces the structural contract plus the no-credential rule.
func (m *ConnectorManifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported schemaVersion %d (this build reads %d)",
			m.SchemaVersion, ManifestSchemaVersion)
	}
	if !manifestIDRe.MatchString(m.ID) {
		return fmt.Errorf("id %q must be reverse-dns/name (e.g. \"com.github/github\")", m.ID)
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("version %q must be an exact semantic version, not a range", m.Version)
	}
	if strings.TrimSpace(m.Repository.URL) == "" {
		return fmt.Errorf("repository.url is mandatory provenance")
	}
	if _, err := url.ParseRequestURI(m.Repository.URL); err != nil {
		return fmt.Errorf("repository.url %q is not a valid URL: %w", m.Repository.URL, err)
	}
	set := 0
	if m.Reach.MCP != nil {
		set++
	}
	if m.Reach.OpenAPI != nil {
		set++
	}
	if set != 1 {
		return fmt.Errorf("reach must declare exactly one of mcp|openapi, got %d", set)
	}
	if m.Reach.OpenAPI != nil {
		if _, err := url.ParseRequestURI(m.Reach.OpenAPI.BaseURL); err != nil {
			return fmt.Errorf("reach.openapi.baseUrl %q is not a valid URL: %w", m.Reach.OpenAPI.BaseURL, err)
		}
	}
	if len(m.Auth.Schemes) == 0 {
		return fmt.Errorf("auth.schemes must declare at least one scheme")
	}
	for i, s := range m.Auth.Schemes {
		if err := validateScheme(i, s); err != nil {
			return err
		}
	}
	for i, in := range m.Inputs {
		if strings.TrimSpace(in.Name) == "" {
			return fmt.Errorf("inputs[%d]: name is required", i)
		}
		if in.IsSecret {
			return fmt.Errorf("inputs[%d] %q: secret inputs must not be declared in the manifest; they are implied by the auth scheme and live only in the vault", i, in.Name)
		}
	}
	if err := rejectCredentialMaterial(m); err != nil {
		return err
	}
	return nil
}

func validateScheme(i int, s AuthScheme) error {
	where := fmt.Sprintf("auth.schemes[%d]", i)
	switch s.Type {
	case AuthOAuth2, AuthAPIKey, AuthHTTP, AuthBasic:
	default:
		return fmt.Errorf("%s: unknown auth type %q (closed enum: oauth2|apiKey|http|basic)", where, s.Type)
	}
	if s.Type == AuthOAuth2 {
		if s.TokenURL == "" || s.AuthorizeURL == "" {
			return fmt.Errorf("%s: oauth2 requires tokenUrl and authorizeUrl", where)
		}
		if _, err := url.ParseRequestURI(s.TokenURL); err != nil {
			return fmt.Errorf("%s: tokenUrl invalid: %w", where, err)
		}
		if _, err := url.ParseRequestURI(s.AuthorizeURL); err != nil {
			return fmt.Errorf("%s: authorizeUrl invalid: %w", where, err)
		}
		if s.ResourceMetadataURL != "" {
			if _, err := url.ParseRequestURI(s.ResourceMetadataURL); err != nil {
				return fmt.Errorf("%s: resourceMetadataUrl invalid: %w", where, err)
			}
		}
	}
	return nil
}

// rejectCredentialMaterial re-serializes the manifest and scans every key for
// credential-shaped names carrying a value. A manifest that ships a secret —
// even an "example" one — is rejected, because example secrets get copied into
// real configs.
func rejectCredentialMaterial(m *ConnectorManifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	return scanForSecrets(decoded, "$")
}

func scanForSecrets(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lower := strings.ToLower(strings.ReplaceAll(k, "-", "_"))
			for _, banned := range credentialShapedKeys {
				if lower == banned {
					if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
						return fmt.Errorf("manifest carries credential material at %s.%s: manifests must never contain secrets", path, k)
					}
				}
			}
			if err := scanForSecrets(val, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range t {
			if err := scanForSecrets(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
