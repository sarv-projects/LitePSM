package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/state"
)

// DecisionKind represents the final policy evaluation outcome.
type DecisionKind string

const (
	DecisionAllow DecisionKind = "allow"
	DecisionDeny  DecisionKind = "deny"
	DecisionAsk   DecisionKind = "ask"
)

// CanonicalEffect identifies an action in the 17-action security taxonomy.
type CanonicalEffect string

const (
	EffectFilesystemRead   CanonicalEffect = "filesystem.read"
	EffectFilesystemWrite  CanonicalEffect = "filesystem.write"
	EffectFilesystemDelete CanonicalEffect = "filesystem.delete"
	EffectProcessSpawn     CanonicalEffect = "process.spawn"
	EffectProcessSignal    CanonicalEffect = "process.signal"
	EffectNetworkOutbound  CanonicalEffect = "network.outbound"
	EffectExternalRead     CanonicalEffect = "external.read"
	EffectExternalWrite    CanonicalEffect = "external.write"
	EffectExternalDelete   CanonicalEffect = "external.delete"
	EffectCredentialRead   CanonicalEffect = "credential.read"
	EffectCredentialWrite  CanonicalEffect = "credential.write"
	EffectSystemConfig     CanonicalEffect = "system.config.write"
	EffectHostConfig       CanonicalEffect = "host.config.write"
	EffectHookExecute      CanonicalEffect = "hook.execute"
	EffectPackageInstall   CanonicalEffect = "package.install"
	EffectProviderStart    CanonicalEffect = "provider.start"
	EffectProviderStop     CanonicalEffect = "provider.stop"
)

// ValidEffects maps all 17 supported canonical effects.
var ValidEffects = map[CanonicalEffect]bool{
	EffectFilesystemRead:   true,
	EffectFilesystemWrite:  true,
	EffectFilesystemDelete: true,
	EffectProcessSpawn:     true,
	EffectProcessSignal:    true,
	EffectNetworkOutbound:  true,
	EffectExternalRead:     true,
	EffectExternalWrite:    true,
	EffectExternalDelete:   true,
	EffectCredentialRead:   true,
	EffectCredentialWrite:  true,
	EffectSystemConfig:     true,
	EffectHostConfig:       true,
	EffectHookExecute:      true,
	EffectPackageInstall:   true,
	EffectProviderStart:    true,
	EffectProviderStop:     true,
}

// EffectDeclaration tags a canonical effect with its declaration provenance.
type EffectDeclaration struct {
	Effect     CanonicalEffect         `json:"effect"`
	Provenance domain.EffectProvenance `json:"provenance"`
	Target     string                  `json:"target,omitempty"`
}

// PolicyInput encapsulates all context required to evaluate an operation.
type PolicyInput struct {
	Actor               string              `json:"actor"` // user | agent | system
	HostID              string              `json:"hostId"`
	Operation           string              `json:"operation"` // install | update | invoke | start
	TargetRef           string              `json:"targetRef"` // ListingId or CapabilityId
	CapabilityID        string              `json:"capabilityId,omitempty"`
	Effects             []EffectDeclaration `json:"effects"`
	RequestedAccess     []string            `json:"requestedAccess"`
	SchemaFingerprint   string              `json:"schemaFingerprint,omitempty"`
	CASTreeDigest       string              `json:"casTreeDigest,omitempty"`       // Local CAS Merkle digest
	EndpointOrigin      string              `json:"endpointOrigin,omitempty"`      // Remote HTTPS origin
	ServerVersionDigest string              `json:"serverVersionDigest,omitempty"` // Remote server version
	Scope               domain.InstallScope `json:"scope"`
	WorkspaceID         string              `json:"workspaceId,omitempty"`
	ProjectRoot         string              `json:"projectRoot,omitempty"`
}

// PolicyDecision defines the outcome and required approval channel.
type PolicyDecision struct {
	Decision        DecisionKind           `json:"decision"` // allow | deny | ask
	ReasonCodes     []string               `json:"reasonCodes"`
	MatchedRuleIDs  []string               `json:"matchedRuleIds"`
	RequiredChannel domain.ApprovalChannel `json:"requiredChannel"`
	Detail          string                 `json:"detail,omitempty"`
}

// DenyRule defines explicit user or organizational blacklists.
type DenyRule struct {
	RuleID    string
	TargetRef string
	Effect    CanonicalEffect
	HostID    string
}

// Engine evaluates operations against the 5-tier security precedence model.
type Engine struct {
	db        *state.DB
	denyRules []DenyRule
}

// NewEngine creates a new policy engine instance.
func NewEngine(db *state.DB, denyRules []DenyRule) *Engine {
	return &Engine{
		db:        db,
		denyRules: denyRules,
	}
}

// Evaluate performs strict 5-tier security evaluation.
func (e *Engine) Evaluate(ctx context.Context, input PolicyInput) PolicyDecision {
	// -------------------------------------------------------------
	// Tier 1: Hard Invariant Deny Rules (Cannot be overridden)
	// -------------------------------------------------------------

	// Invariant 1: Deny raw shell command execution from marketplace sources
	if strings.HasPrefix(input.TargetRef, "cmd:") || strings.HasPrefix(input.TargetRef, "command:") {
		return PolicyDecision{
			Decision:    DecisionDeny,
			ReasonCodes: []string{"INVARIANT_DENY_COMMAND_MARKETPLACE"},
			Detail:      "Command source execution from external marketplace is strictly prohibited",
		}
	}

	for _, eff := range input.Effects {
		// Invariant 2: Deny writing plaintext secrets to non-vault files
		if eff.Effect == EffectCredentialWrite {
			tLower := strings.ToLower(eff.Target)
			if strings.Contains(tLower, "disk") || strings.Contains(tLower, "file") || strings.Contains(tLower, ".env") || strings.Contains(tLower, "config") {
				return PolicyDecision{
					Decision:    DecisionDeny,
					ReasonCodes: []string{"INVARIANT_DENY_PLAINTEXT_SECRET_DISK"},
					Detail:      "Storing secrets in unencrypted local files is prohibited; use OS secret store",
				}
			}
		}

		// Invariant 3: Deny SSRF / loopback network access for untrusted third-party capabilities
		if eff.Effect == EffectNetworkOutbound {
			tLower := strings.ToLower(eff.Target)
			isLocalOrMeta := strings.Contains(tLower, "127.") ||
				strings.Contains(tLower, "localhost") ||
				strings.Contains(tLower, "169.254.") ||
				strings.Contains(tLower, "[::1]") ||
				strings.Contains(tLower, "::1") ||
				strings.Contains(tLower, "0.0.0.0") ||
				strings.Contains(tLower, "10.") ||
				strings.Contains(tLower, "192.168.")
			if isLocalOrMeta {
				if eff.Provenance != domain.ProvenanceUserClassified {
					return PolicyDecision{
						Decision:    DecisionDeny,
						ReasonCodes: []string{"INVARIANT_DENY_SSRF_LOOPBACK"},
						Detail:      "Access to loopback, private network, or cloud metadata endpoints is restricted",
					}
				}
			}
		}
	}

	// -------------------------------------------------------------
	// Tier 2: Explicit User Deny Rules (Config / Blacklist)
	// -------------------------------------------------------------
	for _, rule := range e.denyRules {
		if rule.TargetRef != "" && rule.TargetRef == input.TargetRef {
			return PolicyDecision{
				Decision:       DecisionDeny,
				ReasonCodes:    []string{"USER_EXPLICIT_DENY"},
				MatchedRuleIDs: []string{rule.RuleID},
				Detail:         fmt.Sprintf("Explicitly blacklisted by user rule %s", rule.RuleID),
			}
		}
		for _, eff := range input.Effects {
			if rule.Effect != "" && rule.Effect == eff.Effect {
				return PolicyDecision{
					Decision:       DecisionDeny,
					ReasonCodes:    []string{"USER_EFFECT_DENY"},
					MatchedRuleIDs: []string{rule.RuleID},
					Detail:         fmt.Sprintf("Effect %s blacklisted by rule %s", rule.Effect, rule.RuleID),
				}
			}
		}
	}

	// -------------------------------------------------------------
	// Tier 3: Explicit User Allow / Capability Grant with Identity Binding
	// -------------------------------------------------------------
	if input.CapabilityID != "" && input.SchemaFingerprint != "" {
		decision, valid := e.checkCapabilityGrant(ctx, input)
		if valid {
			return decision
		}
		if decision.Decision == DecisionDeny {
			return decision // Drift detected -> reject
		}
	}

	// -------------------------------------------------------------
	// Tier 4: Ask / Elicitation Required (Human in the loop)
	// -------------------------------------------------------------
	hasDangerousEffects := false
	var askReasons []string

	for _, eff := range input.Effects {
		switch eff.Effect {
		case EffectFilesystemWrite, EffectFilesystemDelete, EffectProcessSpawn, EffectNetworkOutbound, EffectExternalWrite, EffectExternalDelete, EffectCredentialRead:
			hasDangerousEffects = true
			askReasons = append(askReasons, string(eff.Effect))
		}
	}

	if hasDangerousEffects || input.Operation == "install" || input.Operation == "update" {
		channel := domain.ApprovalInteractiveCLI
		if input.Actor == "agent" {
			channel = domain.ApprovalAgentBridge
		}
		return PolicyDecision{
			Decision:        DecisionAsk,
			ReasonCodes:     askReasons,
			RequiredChannel: channel,
			Detail:          "Human authorization required for effectful action",
		}
	}

	// Read-only benign local operations (e.g. searching catalog, reading skill index)
	isReadOnlyOp := input.Operation == "read" || input.Operation == "search" || input.Operation == "describe" || input.Operation == "list"
	if isReadOnlyOp && (len(input.Effects) == 0 || (len(input.Effects) == 1 && input.Effects[0].Effect == EffectFilesystemRead)) {
		return PolicyDecision{
			Decision:    DecisionAllow,
			ReasonCodes: []string{"BENIGN_READ_ONLY"},
			Detail:      "Read-only action permitted automatically",
		}
	}

	// -------------------------------------------------------------
	// Tier 5: Default Deny (Fail-Closed)
	// -------------------------------------------------------------
	return PolicyDecision{
		Decision:    DecisionDeny,
		ReasonCodes: []string{"DEFAULT_DENY_FAIL_CLOSED"},
		Detail:      "Unclassified action failed closed",
	}
}

func (e *Engine) checkCapabilityGrant(ctx context.Context, input PolicyInput) (PolicyDecision, bool) {
	if e.db == nil {
		return PolicyDecision{}, false
	}

	// Query active capability grants for this capability ID
	query := `
	SELECT grant_id, schema_fingerprint, cas_tree_digest, endpoint_origin, server_version_digest, status
	FROM capability_grants
	WHERE capability_id = ? AND status = 'active'
	  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);`

	rows, err := e.db.Raw().QueryContext(ctx, query, input.CapabilityID)
	if err != nil || rows == nil {
		return PolicyDecision{}, false
	}
	defer rows.Close()

	for rows.Next() {
		var grantID, expectedFP string
		var casDigest, endpointOrigin, serverVer sqlNullStr
		var status string

		if err := rows.Scan(&grantID, &expectedFP, &casDigest, &endpointOrigin, &serverVer, &status); err != nil {
			continue
		}

		// Check 1: Schema Drift Invalidation
		if expectedFP != input.SchemaFingerprint {
			return PolicyDecision{
				Decision:    DecisionDeny,
				ReasonCodes: []string{"LPSM-PROVIDER-SCHEMA-DRIFT"},
				Detail:      fmt.Sprintf("Schema drift detected for capability %s: prior grant invalidated", input.CapabilityID),
			}, false
		}

		// Check 2: Local CAS Code Drift Invalidation
		if input.CASTreeDigest != "" && casDigest.Valid && casDigest.String != input.CASTreeDigest {
			return PolicyDecision{
				Decision:    DecisionDeny,
				ReasonCodes: []string{"LPSM-PROVIDER-CODE-DRIFT"},
				Detail:      fmt.Sprintf("Underlying CAS code modified for capability %s: prior grant invalidated", input.CapabilityID),
			}, false
		}

		// Check 3: Remote Endpoint Origin Drift Invalidation
		if input.EndpointOrigin != "" && endpointOrigin.Valid && endpointOrigin.String != input.EndpointOrigin {
			return PolicyDecision{
				Decision:    DecisionDeny,
				ReasonCodes: []string{"LPSM-PROVIDER-ENDPOINT-DRIFT"},
				Detail:      fmt.Sprintf("Remote endpoint origin changed to %s: prior grant invalidated", input.EndpointOrigin),
			}, false
		}

		// Check 4: Remote Server Version Drift Invalidation
		if input.ServerVersionDigest != "" && serverVer.Valid && serverVer.String != input.ServerVersionDigest {
			return PolicyDecision{
				Decision:    DecisionDeny,
				ReasonCodes: []string{"LPSM-PROVIDER-CODE-DRIFT"},
				Detail:      fmt.Sprintf("Remote server version changed for capability %s: prior grant invalidated", input.CapabilityID),
			}, false
		}

		// All identity bindings match!
		return PolicyDecision{
			Decision:       DecisionAllow,
			ReasonCodes:    []string{"ACTIVE_CAPABILITY_GRANT"},
			MatchedRuleIDs: []string{grantID},
			Detail:         fmt.Sprintf("Authorized by active capability grant %s", grantID),
		}, true
	}

	return PolicyDecision{}, false
}

type sqlNullStr struct {
	String string
	Valid  bool
}

func (s *sqlNullStr) Scan(value any) error {
	if value == nil {
		s.String, s.Valid = "", false
		return nil
	}
	switch v := value.(type) {
	case string:
		s.String, s.Valid = v, true
	case []byte:
		s.String, s.Valid = string(v), true
	}
	return nil
}
