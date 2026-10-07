package policy

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
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

// DenyRule defines explicit user or organizational blacklists. Every
// non-empty field must match for the rule to apply (a conjunction): a rule
// that sets TargetRef and Effect denies only that effect on that target, and a
// rule that sets HostID applies only to operations from that host. A rule with
// no field set matches nothing.
type DenyRule struct {
	RuleID    string          `json:"ruleId"`
	TargetRef string          `json:"targetRef,omitempty"`
	Effect    CanonicalEffect `json:"effect,omitempty"`
	HostID    string          `json:"hostId,omitempty"`
}

// blockedNetworkPrefixes are the address ranges a third-party capability may
// never reach: loopback, private (RFC 1918), link-local / cloud metadata,
// carrier-grade NAT, the "this network" block, and the IPv6 equivalents.
var blockedNetworkPrefixes = mustPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

// networkTargetHost extracts the host from a network.outbound target, which
// may be a URL, host:port, a bare host, or a bracketed IPv6 literal.
func networkTargetHost(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil {
			return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		}
		return ""
	}
	// Strip any path/query before looking for a port.
	if i := strings.IndexAny(t, "/?#"); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndex(t, "@"); i >= 0 {
		t = t[i+1:]
	}
	if strings.HasPrefix(t, "[") {
		if end := strings.Index(t, "]"); end > 0 {
			return strings.ToLower(t[1:end])
		}
		return ""
	}
	// host:port, but not a bare IPv6 literal (which has several colons).
	if strings.Count(t, ":") == 1 {
		t = t[:strings.Index(t, ":")]
	}
	return strings.ToLower(strings.TrimSuffix(t, "."))
}

// parseLenientIP parses canonical IP literals plus the legacy IPv4 forms the
// OS resolver (inet_aton) accepts: "2130706433", "0x7f.1", "017700000001".
func parseLenientIP(host string) (netip.Addr, bool) {
	if i := strings.Index(host, "%"); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Unmap(), true
	}
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return netip.Addr{}, false
		}
		n, err := strconv.ParseUint(p, 0, 32) // base prefix: 0x hex, 0 octal
		if err != nil {
			return netip.Addr{}, false
		}
		nums[i] = n
	}
	var v uint64
	for i := 0; i < len(nums)-1; i++ {
		if nums[i] > 255 {
			return netip.Addr{}, false
		}
		v |= nums[i] << (8 * uint(3-i))
	}
	last := nums[len(nums)-1]
	if last >= 1<<(8*uint(4-(len(nums)-1))) {
		return netip.Addr{}, false
	}
	v |= last
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}

// isRestrictedNetworkTarget reports whether a network.outbound target points at
// loopback, private, link-local, metadata or otherwise non-routable space. The
// check is structural (parsed addresses and CIDR membership), never a substring
// match, so "example10.com" is not blocked and "2130706433" is.
func isRestrictedNetworkTarget(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".localdomain") {
		return true
	}
	addr, ok := parseLenientIP(host)
	if !ok {
		return false
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, p := range blockedNetworkPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (r DenyRule) matches(input PolicyInput) (matchedEffect CanonicalEffect, ok bool) {
	if r.TargetRef == "" && r.Effect == "" && r.HostID == "" {
		return "", false
	}
	if r.HostID != "" && r.HostID != input.HostID {
		return "", false
	}
	if r.TargetRef != "" && r.TargetRef != input.TargetRef {
		return "", false
	}
	if r.Effect != "" {
		for _, eff := range input.Effects {
			if eff.Effect == r.Effect {
				return r.Effect, true
			}
		}
		return "", false
	}
	return "", true
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
	targetLower := strings.ToLower(strings.TrimSpace(input.TargetRef))
	if strings.HasPrefix(targetLower, "cmd:") || strings.HasPrefix(targetLower, "command:") {
		return PolicyDecision{
			Decision:    DecisionDeny,
			ReasonCodes: []string{"INVARIANT_DENY_COMMAND_MARKETPLACE"},
			Detail:      "Command source execution from external marketplace is strictly prohibited",
		}
	}

	for _, eff := range input.Effects {
		// Invariant 0: only the canonical effect taxonomy is evaluable; an
		// unknown effect is never treated as benign.
		if !ValidEffects[eff.Effect] {
			return PolicyDecision{
				Decision:    DecisionDeny,
				ReasonCodes: []string{"INVARIANT_DENY_UNKNOWN_EFFECT"},
				Detail:      fmt.Sprintf("Effect %q is not a canonical effect", eff.Effect),
			}
		}

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

		// Invariant 3: Deny SSRF / loopback / private-range network access.
		// The check is structural and independent of any caller-supplied
		// provenance: a caller cannot declare its way past it. An empty or
		// unparsable target cannot be proven safe, so it is denied as well.
		if eff.Effect == EffectNetworkOutbound {
			host := networkTargetHost(eff.Target)
			if host == "" {
				return PolicyDecision{
					Decision:    DecisionDeny,
					ReasonCodes: []string{"INVARIANT_DENY_NETWORK_TARGET_UNKNOWN"},
					Detail:      "Outbound network access requires a declared, parsable target host",
				}
			}
			if isRestrictedNetworkTarget(host) {
				return PolicyDecision{
					Decision:    DecisionDeny,
					ReasonCodes: []string{"INVARIANT_DENY_SSRF_LOOPBACK"},
					Detail:      "Access to loopback, private network, or cloud metadata endpoints is restricted",
				}
			}
		}
	}

	// -------------------------------------------------------------
	// Tier 2: Explicit User Deny Rules (Config / Blacklist)
	// -------------------------------------------------------------
	for _, rule := range e.denyRules {
		effect, ok := rule.matches(input)
		if !ok {
			continue
		}
		if effect != "" {
			return PolicyDecision{
				Decision:       DecisionDeny,
				ReasonCodes:    []string{"USER_EFFECT_DENY"},
				MatchedRuleIDs: []string{rule.RuleID},
				Detail:         fmt.Sprintf("Effect %s blacklisted by rule %s", effect, rule.RuleID),
			}
		}
		return PolicyDecision{
			Decision:       DecisionDeny,
			ReasonCodes:    []string{"USER_EXPLICIT_DENY"},
			MatchedRuleIDs: []string{rule.RuleID},
			Detail:         fmt.Sprintf("Explicitly blacklisted by user rule %s", rule.RuleID),
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
