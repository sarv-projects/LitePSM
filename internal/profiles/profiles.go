// Package profiles implements distributable workspace profiles plus
// time/scope-bounded capability leases (ARCH/35).
//
// Profiles declare requirements (never resolved versions); resolution
// produces a lock. Leases narrow a grant to a session/task with a mandatory
// TTL; expired or cross-scope use fails closed with typed errors.
package profiles

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Member is one profile requirement.
type Member struct {
	Kind       string `json:"kind"`
	Requires   string `json:"requires"`
	Constraint string `json:"constraint,omitempty"`
}

// Target is one deployment target.
type Target struct {
	Host  string `json:"host"`
	Scope string `json:"scope"`
}

// Profile is a named, versioned workspace description.
type Profile struct {
	SchemaVersion int      `json:"schemaVersion"`
	Name          string   `json:"name"`
	Version       string   `json:"version,omitempty"`
	Description   string   `json:"description,omitempty"`
	Members       []Member `json:"members"`
	Targets       []Target `json:"targets"`
	AllowSources  []string `json:"allowSources,omitempty"`
	DenyEffects   []string `json:"denyEffects,omitempty"`
}

// Validate checks the profile contract.
func (p *Profile) Validate() error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("LPSM-PROFILE-SCHEMA: schemaVersion must be 1, got %d", p.SchemaVersion)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("LPSM-PROFILE-NAME: name is required")
	}
	validKinds := map[string]bool{"mcp": true, "skill": true, "plugin": true, "agent": true, "rule": true, "hook": true, "tool": true, "lsp": true, "connector": true}
	for i, m := range p.Members {
		if !validKinds[m.Kind] {
			return fmt.Errorf("LPSM-PROFILE-KIND: members[%d].kind %q is not a v1 kind", i, m.Kind)
		}
		if strings.TrimSpace(m.Requires) == "" {
			return fmt.Errorf("LPSM-PROFILE-REQUIRES: members[%d].requires is required", i)
		}
	}
	return nil
}

// Digest returns the content-addressable digest (canonical JSON, sha256).
func (p *Profile) Digest() string {
	cp := *p
	sort.Slice(cp.Members, func(i, j int) bool {
		if cp.Members[i].Kind != cp.Members[j].Kind {
			return cp.Members[i].Kind < cp.Members[j].Kind
		}
		return cp.Members[i].Requires < cp.Members[j].Requires
	})
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Parse reads the TOML-subset profile form:
//
//	schemaVersion = 1
//	name = "acme-backend.senior-review"
//	version = "2.1.0"
//	[[members]]
//	kind = "mcp"
//	requires = "mcp:builtin:mcp-registry:postgres"
//	constraint = ">=1.4 <2"
func Parse(data []byte) (*Profile, error) {
	p := &Profile{}
	section := ""
	var curMember *Member
	var curTarget *Target
	flush := func() {
		if curMember != nil {
			p.Members = append(p.Members, *curMember)
			curMember = nil
		}
		if curTarget != nil {
			p.Targets = append(p.Targets, *curTarget)
			curTarget = nil
		}
	}
	for ln, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripInlineComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") {
			flush()
			if !strings.HasSuffix(line, "]]") {
				return nil, fmt.Errorf("line %d: unclosed [[table]]", ln+1)
			}
			switch strings.TrimSpace(line[2 : len(line)-2]) {
			case "members":
				curMember = &Member{}
			case "targets":
				curTarget = &Target{}
			default:
				return nil, fmt.Errorf("line %d: unknown array table", ln+1)
			}
			section = ""
			continue
		}
		if strings.HasPrefix(line, "[") {
			flush()
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: unclosed [table]", ln+1)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name != "policy" {
				return nil, fmt.Errorf("line %d: unknown table [%s]", ln+1, name)
			}
			section = name
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", ln+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := unquoteTOML(strings.TrimSpace(line[eq+1:]))
		switch {
		case curMember != nil:
			switch key {
			case "kind":
				curMember.Kind = val
			case "requires":
				curMember.Requires = val
			case "constraint":
				curMember.Constraint = val
			default:
				return nil, fmt.Errorf("line %d: unknown member key %q", ln+1, key)
			}
		case curTarget != nil:
			switch key {
			case "host":
				curTarget.Host = val
			case "scope":
				curTarget.Scope = val
			default:
				return nil, fmt.Errorf("line %d: unknown target key %q", ln+1, key)
			}
		case section == "policy":
			items := parseInlineArray(strings.TrimSpace(line[eq+1:]))
			switch key {
			case "allowSources":
				p.AllowSources = items
			case "denyEffects":
				p.DenyEffects = items
			default:
				return nil, fmt.Errorf("line %d: unknown policy key %q", ln+1, key)
			}
		default:
			switch key {
			case "schemaVersion":
				if val != "1" {
					return nil, fmt.Errorf("line %d: schemaVersion must be 1", ln+1)
				}
				p.SchemaVersion = 1
			case "name":
				p.Name = val
			case "version":
				p.Version = val
			case "description":
				p.Description = val
			default:
				return nil, fmt.Errorf("line %d: unknown key %q", ln+1, key)
			}
		}
	}
	flush()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// Diff reports member/target/policy deltas between two profiles without
// writing anything. activate must show this first.
func Diff(a, b *Profile) []string {
	var out []string
	am := map[string]string{}
	for _, m := range a.Members {
		am[m.Kind+"\x00"+m.Requires] = m.Constraint
	}
	bm := map[string]string{}
	for _, m := range b.Members {
		bm[m.Kind+"\x00"+m.Requires] = m.Constraint
	}
	for k, av := range am {
		if bv, ok := bm[k]; !ok {
			out = append(out, "remove "+strings.ReplaceAll(k, "\x00", " "))
		} else if av != bv {
			out = append(out, "change "+strings.ReplaceAll(k, "\x00", " ")+": "+av+" -> "+bv)
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			out = append(out, "add "+strings.ReplaceAll(k, "\x00", " "))
		}
	}
	sort.Strings(out)
	return out
}

// Lease scopes a capability to a session/task with a mandatory TTL.
type Lease struct {
	LeaseID           string    `json:"leaseId"`
	CapabilityID      string    `json:"capabilityId"`
	SchemaFingerprint string    `json:"schemaFingerprint"`
	Scope             string    `json:"scope"` // session | task
	SessionID         string    `json:"sessionId,omitempty"`
	TaskID            string    `json:"taskId,omitempty"`
	Permissions       []string  `json:"permissions"`
	IssuedAt          time.Time `json:"issuedAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
	RevokedAt         *time.Time `json:"revokedAt,omitempty"`
	IssuedBy          string    `json:"issuedBy,omitempty"`
}

// IssueLease creates a lease. TTL is mandatory; permissions must be a subset
// of the grant's effects (narrowing only); scope binding requires the bound
// session/task id.
func IssueLease(capabilityID, fingerprint, scope, boundID string, grantEffects, permissions []string, ttl time.Duration, issuedBy string) (*Lease, error) {
	if strings.TrimSpace(capabilityID) == "" || strings.TrimSpace(fingerprint) == "" {
		return nil, fmt.Errorf("LPSM-LEASE-ID: capability and schema fingerprint are required")
	}
	if scope != "session" && scope != "task" {
		return nil, fmt.Errorf("LPSM-LEASE-SCOPE: scope must be session|task, got %q", scope)
	}
	if strings.TrimSpace(boundID) == "" {
		return nil, fmt.Errorf("LPSM-LEASE-BINDING: a %s lease requires its %s id", scope, scope)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("LPSM-LEASE-TTL: a lease always expires; ttl must be positive")
	}
	allowed := map[string]bool{}
	for _, e := range grantEffects {
		allowed[e] = true
	}
	for _, p := range permissions {
		if !allowed[p] {
			return nil, fmt.Errorf("LPSM-LEASE-WIDEN: permission %q exceeds the grant", p)
		}
	}
	now := time.Now().UTC()
	l := &Lease{
		LeaseID:           "lease_" + newLeaseSuffix(),
		CapabilityID:      capabilityID,
		SchemaFingerprint: fingerprint,
		Scope:             scope,
		Permissions:       append([]string(nil), permissions...),
		IssuedAt:          now,
		ExpiresAt:         now.Add(ttl),
		IssuedBy:          issuedBy,
	}
	if scope == "session" {
		l.SessionID = boundID
	} else {
		l.TaskID = boundID
	}
	return l, nil
}

// Check validates a lease for use now in a session: expiry, revocation,
// scope binding and schema pinning all fail closed.
func (l *Lease) Check(now time.Time, sessionID, liveFingerprint string) error {
	if l.RevokedAt != nil {
		return fmt.Errorf("LPSM-LEASE-REVOKED: %s", l.LeaseID)
	}
	if !now.Before(l.ExpiresAt) {
		return fmt.Errorf("LPSM-LEASE-EXPIRED: %s expired at %s", l.LeaseID, l.ExpiresAt.Format(time.RFC3339))
	}
	if l.Scope == "session" && l.SessionID != sessionID {
		return fmt.Errorf("LPSM-LEASE-SCOPE: lease bound to session %q, not %q", l.SessionID, sessionID)
	}
	if liveFingerprint != "" && liveFingerprint != l.SchemaFingerprint {
		return fmt.Errorf("LPSM-PROVIDER-SCHEMA-DRIFT: lease %s pins a stale schema", l.LeaseID)
	}
	return nil
}

// Revoke marks the lease revoked (auto-revoke on session/task end calls this).
func (l *Lease) Revoke(now time.Time) {
	cp := now.UTC()
	l.RevokedAt = &cp
}

func stripInlineComment(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inStr = !inStr
		case '#':
			if !inStr {
				return line[:i]
			}
		}
	}
	return line
}

func unquoteTOML(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	}
	return s
}

func parseInlineArray(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := unquoteTOML(strings.TrimSpace(p)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func newLeaseSuffix() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	const alpha = "0123456789abcdefghjkmnpqrstvwxyz"
	out := make([]byte, 16)
	for i := range out {
		out[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(out)
}
