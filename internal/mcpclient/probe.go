package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// DiscoveredCapability represents a probed tool from a provider with its schema fingerprint.
type DiscoveredCapability struct {
	NativeName        string          `json:"nativeName"`
	Description       string          `json:"description,omitempty"`
	InputSchema       json.RawMessage `json:"inputSchema"`
	SchemaFingerprint string          `json:"schemaFingerprint"`
}

// FingerprintSchema computes the canonical SHA-256 fingerprint of an MCP tool input schema.
func FingerprintSchema(schema json.RawMessage) (string, error) {
	return domain.ComputeSchemaFingerprint(schema)
}

// ProbeProvider queries an active MCP session for all exposed tools and computes their fingerprints.
func ProbeProvider(ctx context.Context, session ClientSession) ([]DiscoveredCapability, error) {
	tools, err := session.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	var capabilities []DiscoveredCapability
	for _, t := range tools {
		fp, err := FingerprintSchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("failed to fingerprint schema for tool %q: %w", t.Name, err)
		}

		capabilities = append(capabilities, DiscoveredCapability{
			NativeName:        t.Name,
			Description:       t.Description,
			InputSchema:       t.InputSchema,
			SchemaFingerprint: fp,
		})
	}

	return capabilities, nil
}

// DriftKind identifies the type of drift detected.
type DriftKind string

const (
	DriftNone     DriftKind = "none"
	DriftSchema   DriftKind = "schema"
	DriftCode     DriftKind = "code"
	DriftEndpoint DriftKind = "endpoint"
)

// DriftReport details whether an active grant matches current runtime reality.
type DriftReport struct {
	HasDrift DriftKind         `json:"hasDrift"`
	Error    *domain.LPSMError `json:"error,omitempty"`
	Details  string            `json:"details,omitempty"`
}

// DetectDrift checks whether an active grant has experienced schema, code, or endpoint drift.
func DetectDrift(
	grant *domain.CapabilityGrant,
	currentCap DiscoveredCapability,
	currentCASTreeDigest string,
	currentEndpointOrigin string,
	currentServerVersionDigest string,
) DriftReport {
	if grant == nil {
		return DriftReport{
			HasDrift: DriftSchema,
			Error:    domain.NewError(domain.CodePolicyDenied, "no active capability grant found", nil),
		}
	}

	// 1. Schema Drift Check (Applies to both local and remote)
	if grant.SchemaFingerprint != currentCap.SchemaFingerprint {
		return DriftReport{
			HasDrift: DriftSchema,
			Details:  fmt.Sprintf("schema fingerprint changed: expected %s, got %s", grant.SchemaFingerprint, currentCap.SchemaFingerprint),
			Error: domain.NewError(
				domain.CodeProviderSchemaDrift,
				fmt.Sprintf("tool %q input schema has changed since approval was granted", currentCap.NativeName),
				map[string]any{
					"tool":                currentCap.NativeName,
					"expectedFingerprint": grant.SchemaFingerprint,
					"actualFingerprint":   currentCap.SchemaFingerprint,
				},
			),
		}
	}

	// 2. Local CAS Tree Code Drift Check
	if grant.CASTreeDigest != "" && currentCASTreeDigest != "" {
		if grant.CASTreeDigest != currentCASTreeDigest {
			return DriftReport{
				HasDrift: DriftCode,
				Details:  fmt.Sprintf("CAS tree digest changed: expected %s, got %s", grant.CASTreeDigest, currentCASTreeDigest),
				Error: domain.NewError(
					domain.CodeProviderCodeDrift,
					fmt.Sprintf("underlying disk binary or source files for %q have been modified", currentCap.NativeName),
					map[string]any{
						"tool":               currentCap.NativeName,
						"expectedTreeDigest": grant.CASTreeDigest,
						"actualTreeDigest":   currentCASTreeDigest,
					},
				),
			}
		}
	}

	// 3. Remote Endpoint / Server Version Drift Check
	if grant.EndpointOrigin != "" && currentEndpointOrigin != "" {
		if grant.EndpointOrigin != currentEndpointOrigin {
			return DriftReport{
				HasDrift: DriftEndpoint,
				Details:  fmt.Sprintf("endpoint origin changed: expected %s, got %s", grant.EndpointOrigin, currentEndpointOrigin),
				Error: domain.NewError(
					domain.CodeProviderEndpointDrift,
					fmt.Sprintf("remote endpoint origin changed for %q", currentCap.NativeName),
					map[string]any{
						"tool":           currentCap.NativeName,
						"expectedOrigin": grant.EndpointOrigin,
						"actualOrigin":   currentEndpointOrigin,
					},
				),
			}
		}
	}

	if grant.ServerVersionDigest != "" && currentServerVersionDigest != "" {
		if grant.ServerVersionDigest != currentServerVersionDigest {
			return DriftReport{
				HasDrift: DriftEndpoint,
				Details:  fmt.Sprintf("remote server version changed: expected %s, got %s", grant.ServerVersionDigest, currentServerVersionDigest),
				Error: domain.NewError(
					domain.CodeProviderEndpointDrift,
					fmt.Sprintf("remote server version digest changed for %q", currentCap.NativeName),
					map[string]any{
						"tool":            currentCap.NativeName,
						"expectedVersion": grant.ServerVersionDigest,
						"actualVersion":   currentServerVersionDigest,
					},
				),
			}
		}
	}

	return DriftReport{HasDrift: DriftNone}
}
