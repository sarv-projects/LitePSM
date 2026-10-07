package profiles

import (
	"strings"
	"testing"
	"time"
)

const profileDoc = `schemaVersion = 1
name = "acme-backend.senior-review"
version = "2.1.0"
description = "Backend review workspace."

[[members]]
kind = "mcp"
requires = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"

[[members]]
kind = "skill"
requires = "skill:builtin:agent-skills:review-pr"
constraint = "1.2.0"

[[targets]]
host = "claude-code"
scope = "project"

[policy]
allowSources = ["builtin:mcp-registry"]
denyEffects = ["external.delete"]
`

func TestParseProfile(t *testing.T) {
	p, err := Parse([]byte(profileDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "acme-backend.senior-review" || len(p.Members) != 2 || len(p.Targets) != 1 {
		t.Errorf("profile = %+v", p)
	}
	if len(p.AllowSources) != 1 || len(p.DenyEffects) != 1 {
		t.Errorf("policy = %v %v", p.AllowSources, p.DenyEffects)
	}
	if d := p.Digest(); !strings.HasPrefix(d, "sha256:") {
		t.Errorf("digest = %q", d)
	}
}

func TestProfileRejectsBadKind(t *testing.T) {
	bad := "schemaVersion = 1\nname = \"x\"\n[[members]]\nkind = \"nope\"\nrequires = \"mcp:a:b:c\"\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("bad kind must fail")
	}
}

func TestDiff(t *testing.T) {
	a, _ := Parse([]byte(profileDoc))
	b, _ := Parse([]byte(profileDoc))
	if d := Diff(a, b); len(d) != 0 {
		t.Errorf("identical profiles diff = %v", d)
	}
	b.Members = b.Members[:1]
	d := Diff(a, b)
	if len(d) != 1 || !strings.HasPrefix(d[0], "remove") {
		t.Errorf("diff = %v", d)
	}
}

func TestLeaseLifecycle(t *testing.T) {
	grant := []string{"network.outbound", "fs.read"}
	l, err := IssueLease("inst_x/srv/q", "sha256:fp", "session", "sess-1", grant, []string{"fs.read"}, time.Hour, "approval-1")
	if err != nil {
		t.Fatalf("IssueLease: %v", err)
	}
	if l.ExpiresAt.Before(l.IssuedAt) || l.SessionID != "sess-1" {
		t.Errorf("lease = %+v", l)
	}
	now := time.Now().UTC()
	if err := l.Check(now, "sess-1", "sha256:fp"); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := l.Check(now, "sess-2", "sha256:fp"); err == nil {
		t.Error("cross-session use must fail")
	}
	if err := l.Check(now.Add(2*time.Hour), "sess-1", "sha256:fp"); err == nil {
		t.Error("expired lease must fail")
	}
	l2, _ := IssueLease("c", "fp", "task", "task-1", grant, []string{"fs.read"}, time.Hour, "")
	l2.Revoke(now)
	if err := l2.Check(now, "", "fp"); err == nil {
		t.Error("revoked lease must fail")
	}
}

func TestLeaseRules(t *testing.T) {
	grant := []string{"fs.read"}
	if _, err := IssueLease("c", "fp", "session", "s", grant, []string{"network.outbound"}, time.Hour, ""); err == nil {
		t.Error("lease must not widen the grant")
	}
	if _, err := IssueLease("c", "fp", "session", "", grant, nil, time.Hour, ""); err == nil {
		t.Error("unbound session lease must fail")
	}
	if _, err := IssueLease("c", "fp", "session", "s", grant, nil, 0, ""); err == nil {
		t.Error("leaseless TTL must fail")
	}
}
