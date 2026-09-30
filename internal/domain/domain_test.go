package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSourceIDParsing(t *testing.T) {
	valid := []string{
		"builtin:mcp-registry",
		"git:anthropic-official",
		"npm:curated_tools",
		"src-1:feed-02",
	}

	for _, s := range valid {
		sid, err := ParseSourceID(s)
		if err != nil {
			t.Fatalf("expected %q to be valid SourceID, got err: %v", s, err)
		}
		if sid.String() != s {
			t.Fatalf("expected %q, got %q", s, sid.String())
		}
		if sid.Namespace() == "" || sid.Slug() == "" {
			t.Fatalf("expected non-empty namespace and slug for %q", s)
		}
	}

	invalid := []string{
		"",
		"noslug",
		":slug",
		"ns:",
		"invalid uppercase:slug",
		"ab:cd", // too short (namespace < 3 chars)
		"toolongnamespace12345678901234567890123:slug",
	}

	for _, s := range invalid {
		_, err := ParseSourceID(s)
		if err == nil {
			t.Fatalf("expected %q to fail SourceID parsing, but passed", s)
		}
	}
}

func TestListingID(t *testing.T) {
	sid := SourceID("builtin:mcp-registry")
	lid := NewListingID(KindMCP, sid, "@modelcontextprotocol/server-postgres")

	expectedPrefix := "mcp:builtin:mcp-registry:%40modelcontextprotocol"
	if !strings.HasPrefix(lid.String(), expectedPrefix) {
		t.Fatalf("expected prefix %q, got %q", expectedPrefix, lid.String())
	}

	parsed, err := ParseListingID(lid.String())
	if err != nil {
		t.Fatalf("failed to parse valid listing ID: %v", err)
	}

	if parsed.Kind() != KindMCP {
		t.Fatalf("expected kind %s, got %s", KindMCP, parsed.Kind())
	}

	if parsed.SourceID() != sid {
		t.Fatalf("expected sourceId %s, got %s", sid, parsed.SourceID())
	}

	upstream, err := parsed.UpstreamID()
	if err != nil {
		t.Fatalf("failed to decode upstream ID: %v", err)
	}
	if upstream != "@modelcontextprotocol/server-postgres" {
		t.Fatalf("expected decoded upstream '@modelcontextprotocol/server-postgres', got %q", upstream)
	}
}

func TestComponentID(t *testing.T) {
	lid := ListingID("mcp:builtin:mcp-registry:pg")
	cid := NewComponentID(lid, "1.4.0", ComponentMCPProvider, "postgres-server")

	expected := "mcp:builtin:mcp-registry:pg@1.4.0#mcp-provider/postgres-server"
	if cid.String() != expected {
		t.Fatalf("expected %q, got %q", expected, cid.String())
	}

	raw, parsedLid, ver, kind, name, err := ParseComponentID(cid.String())
	if err != nil {
		t.Fatalf("failed to parse ComponentID: %v", err)
	}
	if raw.String() != expected || parsedLid != lid || ver != "1.4.0" || kind != ComponentMCPProvider || name != "postgres-server" {
		t.Fatalf("component decomposition mismatch: got lid=%s ver=%s kind=%s name=%s", parsedLid, ver, kind, name)
	}

	// Invalid cases
	_, _, _, _, _, err = ParseComponentID("invalid")
	if err == nil {
		t.Fatal("expected error parsing invalid component ID")
	}
}

func TestInstallAndCapabilityID(t *testing.T) {
	instID, err := ParseInstallID("inst_01J9X8K2M4N5P6Q7R8S9T0U1V2")
	if err != nil {
		t.Fatalf("valid install ID failed: %v", err)
	}

	capID := NewCapabilityID(instID, "server", "query_db")
	expectedCap := "inst_01J9X8K2M4N5P6Q7R8S9T0U1V2/server/query_db"
	if capID.String() != expectedCap {
		t.Fatalf("expected %q, got %q", expectedCap, capID.String())
	}

	raw, parsedInst, comp, tool, err := ParseCapabilityID(capID.String())
	if err != nil {
		t.Fatalf("failed to parse capability ID: %v", err)
	}
	if raw != capID || parsedInst != instID || comp != "server" || tool != "query_db" {
		t.Fatalf("capability decomposition mismatch: inst=%s comp=%s tool=%s", parsedInst, comp, tool)
	}

	// Test invalid install ID
	_, err = ParseInstallID("badprefix_123")
	if err == nil {
		t.Fatal("expected error for bad install ID prefix")
	}
}

func TestCanonicalizeJSON(t *testing.T) {
	// Tests key sorting, whitespace elimination, and number canonicalization
	input := []byte(`{
		"z": 1.0,
		"a": "hello world",
		"m": {
			"sub_b": false,
			"sub_a": [3, 2, 1]
		},
		"c": null
	}`)

	canonical, err := CanonicalizeJSON(input)
	if err != nil {
		t.Fatalf("canonicalization failed: %v", err)
	}

	expected := `{"a":"hello world","c":null,"m":{"sub_a":[3,2,1],"sub_b":false},"z":1}`
	if string(canonical) != expected {
		t.Fatalf("canonical JSON mismatch:\nGot:      %s\nExpected: %s", string(canonical), expected)
	}
}

func TestPlanHashInvariance(t *testing.T) {
	now := time.Now()
	plan1 := &InstallPlan{
		SchemaVersion:    2,
		PlanID:           "plan_01J9X8K2M4N5P6Q7R8S9T0U1V2",
		CreatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
		CatalogReleaseID: "rel_2026_09_30",
		SourceSnapshots:  []string{"snap_01", "snap_02"},
		Request: PlanRequest{
			ListingID:   "mcp:builtin:mcp-registry:pg",
			TargetScope: ScopeUser,
		},
		Resolved: PlanResolved{
			Version: "1.4.0",
			Artifacts: []PlanArtifact{
				{
					ArtifactID: "art_01",
					Type:       "npm",
					Locator:    "https://registry.npmjs.org/@modelcontextprotocol/server-postgres/-/server-postgres-1.4.0.tgz",
					Digest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					Size:       1024,
				},
			},
		},
		Effects: []string{"fs:read", "net:outbound"},
		Preconditions: PlanPreconditions{
			RuntimesFound: []string{"node@20.0.0"},
		},
		Approval: PlanApproval{
			Channel:  ApprovalInteractiveCLI,
			Decision: "approve",
		},
	}

	hash1, err := ComputePlanHash(plan1)
	if err != nil {
		t.Fatalf("failed to compute plan hash 1: %v", err)
	}
	if !strings.HasPrefix(hash1, "sha256:") {
		t.Fatalf("expected hash to start with sha256:, got %s", hash1)
	}

	// Change volatile fields only: planId, createdAt, expiresAt, and approval
	plan2 := *plan1
	plan2.PlanID = "plan_DIFFERENT_ID_9999999999"
	plan2.CreatedAt = now.Add(1 * time.Hour)
	plan2.ExpiresAt = now.Add(2 * time.Hour)
	plan2.Approval.ApprovalID = "app_12345"

	hash2, err := ComputePlanHash(&plan2)
	if err != nil {
		t.Fatalf("failed to compute plan hash 2: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("planHash must be invariant to volatile fields!\nHash1: %s\nHash2: %s", hash1, hash2)
	}

	// Mutating execution-relevant field MUST change the hash
	plan3 := *plan1
	plan3.Effects = []string{"fs:read", "fs:write", "net:outbound"}
	hash3, err := ComputePlanHash(&plan3)
	if err != nil {
		t.Fatalf("failed to compute plan hash 3: %v", err)
	}

	if hash1 == hash3 {
		t.Fatal("planHash must change when execution-relevant fields change")
	}
}

func TestSchemaFingerprint(t *testing.T) {
	schema1 := []byte(`{
		"type": "object",
		"properties": {
			"query": { "type": "string" },
			"limit": { "type": "integer" }
		},
		"required": ["query"]
	}`)

	// Same schema but reversed key order and whitespace
	schema2 := []byte(`{"required":["query"],"properties":{"limit":{"type":"integer"},"query":{"type":"string"}},"type":"object"}`)

	fp1, err := ComputeSchemaFingerprint(schema1)
	if err != nil {
		t.Fatalf("failed to fingerprint schema 1: %v", err)
	}
	fp2, err := ComputeSchemaFingerprint(schema2)
	if err != nil {
		t.Fatalf("failed to fingerprint schema 2: %v", err)
	}

	if fp1 != fp2 {
		t.Fatalf("schema fingerprints must be identical for semantically identical schemas:\nFP1: %s\nFP2: %s", fp1, fp2)
	}
}

func TestErrorSerialization(t *testing.T) {
	err := ErrSchemaDrift("inst_01/server/query", "sha256:aaaa", "sha256:bbbb").
		WithCorrelation("corr-12345").
		WithDetail("extra_info", "provider returned new parameters")

	jsonBytes, jsonErr := err.JSON()
	if jsonErr != nil {
		t.Fatalf("failed to marshal LPSMError: %v", jsonErr)
	}

	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal LPSMError JSON: %v", err)
	}

	if parsed["code"] != "LPSM-PROVIDER-SCHEMA-DRIFT" {
		t.Fatalf("expected code LPSM-PROVIDER-SCHEMA-DRIFT, got %v", parsed["code"])
	}
	if parsed["correlationId"] != "corr-12345" {
		t.Fatalf("expected correlationId corr-12345, got %v", parsed["correlationId"])
	}
	if parsed["retryable"] != false {
		t.Fatalf("expected retryable false, got %v", parsed["retryable"])
	}
}
