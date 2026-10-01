package connector

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/sarv-projects/litepsm/internal/secrets"
	"os"
	"strings"
	"testing"
	"time"
)

// pinnedResolver is a DNS table for tests: no network, deterministic answers.
type pinnedResolver struct {
	addrs map[string][]net.IP
	err   map[string]error
}

func (p pinnedResolver) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if err, ok := p.err[host]; ok {
		return nil, err
	}
	addrs, ok := p.addrs[host]
	if !ok {
		return nil, fmt.Errorf("no such host in test table: %s", host)
	}
	return addrs, nil
}

func publicIP(s string) net.IP { return net.ParseIP(s) }

func testPolicy(hosts ...string) EgressPolicy {
	return EgressPolicy{
		AllowedHosts: hosts,
		Resolver: pinnedResolver{addrs: map[string][]net.IP{
			"api.example.com": {publicIP("93.184.216.34")},
		}},
	}
}

// TestEgressRefusesPrivateResolvedAddresses is the confused-deputy core: a
// pinned name that resolves private must fail even though the NAME is allowed.
func TestEgressRefusesPrivateResolvedAddresses(t *testing.T) {
	cases := map[string]string{
		"loopback":    "127.0.0.1",
		"rfc1918-10":  "10.0.0.5",
		"rfc1918-172": "172.16.4.4",
		"rfc1918-192": "192.168.1.1",
		"link-local":  "169.254.169.254",
		"ipv6-loop":   "::1",
		"ipv6-ula":    "fd00::1",
	}
	for name, ip := range cases {
		t.Run(name, func(t *testing.T) {
			p := EgressPolicy{
				AllowedHosts: []string{"internal.example.com"},
				Resolver: pinnedResolver{addrs: map[string][]net.IP{
					"internal.example.com": {publicIP(ip)},
				}},
			}
			if _, err := p.CheckTarget(context.Background(), "https://internal.example.com/x"); err == nil {
				t.Fatalf("resolved private address %s was allowed", ip)
			}
		})
	}
}

func TestEgressRefusesUnpinnedHost(t *testing.T) {
	p := testPolicy("api.example.com")
	if _, err := p.CheckTarget(context.Background(), "https://evil.example.com/x"); err == nil {
		t.Fatal("unpinned host was allowed")
	}
}

func TestEgressRefusesHTTPByDefault(t *testing.T) {
	p := testPolicy("api.example.com")
	if _, err := p.CheckTarget(context.Background(), "http://api.example.com/x"); err == nil {
		t.Fatal("plain http was allowed without AllowHTTP")
	}
}

func TestEgressRefusesUserinfo(t *testing.T) {
	p := testPolicy("api.example.com")
	if _, err := p.CheckTarget(context.Background(), "https://user:pass@api.example.com/x"); err == nil {
		t.Fatal("userinfo in URL was allowed")
	}
}

func TestEgressAllowsPinnedPublicHost(t *testing.T) {
	p := testPolicy("api.example.com")
	u, err := p.CheckTarget(context.Background(), "https://api.example.com/v1/things?a=b")
	if err != nil {
		t.Fatalf("pinned public host refused: %v", err)
	}
	if u.Hostname() != "api.example.com" {
		t.Fatalf("unexpected host %q", u.Hostname())
	}
}

func TestSanitizeHeadersStripsCredentialHeaders(t *testing.T) {
	in := http.Header{
		"Authorization":       {"Bearer attacker-supplied"},
		"X-Custom":            {"keep"},
		"cookie":              {"session=abc"},
		"Proxy-Authorization": {"Basic xyz"},
	}
	out := SanitizeHeaders(in)
	if out.Get("Authorization") != "" || out.Get("Cookie") != "" || out.Get("Proxy-Authorization") != "" {
		t.Fatalf("credential headers survived: %v", out)
	}
	if out.Get("X-Custom") != "keep" {
		t.Fatalf("innocent header dropped: %v", out)
	}
	if in.Get("Authorization") == "" {
		t.Fatal("input map was mutated")
	}
}

// --- manifest ---

func validManifestJSON() string {
	return `{
		"schemaVersion": 1,
		"id": "com.example/widgets",
		"title": "Widgets",
		"description": "Example connector",
		"version": "1.0.0",
		"repository": {"url": "https://example.com/widgets", "source": "github"},
		"reach": {"openapi": {"ref": "https://example.com/openapi.json", "baseUrl": "https://api.example.com/v1"}},
		"auth": {"schemes": [{
			"type": "oauth2", "secret": true,
			"scopes": {"read": ["widgets:read"], "write": ["widgets:write"]},
			"tokenUrl": "https://example.com/oauth/token",
			"authorizeUrl": "https://example.com/oauth/authorize"
		}]},
		"inputs": [{"name": "WORKSPACE_ID", "description": "workspace", "is_required": true, "is_secret": false}]
	}`
}

func TestParseManifestAcceptsValid(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestJSON()))
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if m.ID != "com.example/widgets" {
		t.Fatalf("unexpected id %q", m.ID)
	}
}

func TestParseManifestRejectsUnknownFields(t *testing.T) {
	withExtra := strings.Replace(validManifestJSON(), `"title": "Widgets",`, `"title": "Widgets", "sneaky": true,`, 1)
	if _, err := ParseManifest([]byte(withExtra)); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestParseManifestRejectsCredentialMaterial(t *testing.T) {
	withSecret := strings.Replace(validManifestJSON(),
		`"authorizeUrl": "https://example.com/oauth/authorize"`,
		`"authorizeUrl": "https://example.com/oauth/authorize", "client_secret": "shh"`, 1)
	if _, err := ParseManifest([]byte(withSecret)); err == nil {
		t.Fatal("manifest carrying a client_secret was accepted")
	}
}

func TestParseManifestRejectsSecretInput(t *testing.T) {
	withSecretInput := strings.Replace(validManifestJSON(), `"is_secret": false`, `"is_secret": true`, 1)
	if _, err := ParseManifest([]byte(withSecretInput)); err == nil {
		t.Fatal("secret input declaration was accepted")
	}
}

func TestParseManifestRejectsBothReaches(t *testing.T) {
	both := strings.Replace(validManifestJSON(),
		`"reach": {"openapi": {"ref": "https://example.com/openapi.json", "baseUrl": "https://api.example.com/v1"}}`,
		`"reach": {"openapi": {"ref": "https://example.com/openapi.json", "baseUrl": "https://api.example.com/v1"}, "mcp": {"url": "https://api.example.com/mcp"}}`, 1)
	if _, err := ParseManifest([]byte(both)); err == nil {
		t.Fatal("dual reach declaration was accepted")
	}
}

func TestParseManifestRejectsVersionRange(t *testing.T) {
	ranged := strings.Replace(validManifestJSON(), `"version": "1.0.0"`, `"version": "^1.0.0"`, 1)
	if _, err := ParseManifest([]byte(ranged)); err == nil {
		t.Fatal("version range was accepted")
	}
}

// --- lifecycle ---

func TestDeriveStatusPrecedence(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	conn := &Connection{Visibility: "PRIVATE", RevokedAt: &past, DisabledAt: &past, ExpiresAt: &past, FailureCount: 99}
	if got := conn.DeriveStatus(now, 3); got != StatusRevoked {
		t.Fatalf("revoke must outrank everything, got %s", got)
	}
	conn.RevokedAt = nil
	if got := conn.DeriveStatus(now, 3); got != StatusDisabled {
		t.Fatalf("disable must outrank exhaustion/expiry, got %s", got)
	}
	conn.DisabledAt = nil
	if got := conn.DeriveStatus(now, 3); got != StatusFailed {
		t.Fatalf("exhaustion must outrank expiry, got %s", got)
	}
	conn.FailureCount = 0
	if got := conn.DeriveStatus(now, 3); got != StatusExpired {
		t.Fatalf("expected EXPIRED, got %s", got)
	}
	conn.ExpiresAt = nil
	if got := conn.DeriveStatus(now, 3); got != StatusActive {
		t.Fatalf("expected ACTIVE, got %s", got)
	}
	conn.RecordFailure(now)
	if got := conn.DeriveStatus(now, 3); got != StatusStale {
		t.Fatalf("failure after success must be STALE, got %s", got)
	}
}

func TestReconnectInPlacePreservesIdentity(t *testing.T) {
	conn := &Connection{ID: "c1", IntegrationID: "i1", Metadata: map[string]string{"login": "alice"}, FailureCount: 5}
	then := time.Now()
	if err := conn.ReconnectInPlace("secret:conn/c1/access_token", nil, then); err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
	if conn.ID != "c1" || conn.Metadata["login"] != "alice" {
		t.Fatalf("identity/metadata changed: %+v", conn)
	}
	if conn.FailureCount != 0 {
		t.Fatalf("failure streak not reset: %+v", conn)
	}
	if err := conn.ReconnectInPlace("", nil, then); err == nil {
		t.Fatal("empty secret reference accepted")
	}
}

func TestScopesCoverRequiresExactGrant(t *testing.T) {
	i := &Integration{GrantedScopes: []string{"widgets:read"}}
	if !i.ScopesCover(nil) {
		t.Fatal("empty requirement must pass")
	}
	if !i.ScopesCover([]string{"widgets:read"}) {
		t.Fatal("granted scope rejected")
	}
	if i.ScopesCover([]string{"widgets:write"}) {
		t.Fatal("ungranted scope covered")
	}
}

// --- executor ---

func testExecutor(secret string) (*Executor, *Integration, *Connection, *string) {
	m, _ := ParseManifest([]byte(validManifestJSON()))
	policy := testPolicy("api.example.com")
	var sawAuth string
	ex := &Executor{
		Manifest: m,
		Policy:   policy,
		Operations: map[string]Operation{
			"get_widget": {
				Name: "get_widget", Method: "GET", PathTemplate: "widgets/{id}",
				RequiredScopes: []string{"widgets:read"},
				SecretField:    "access_token", Scheme: AuthOAuth2,
			},
			"write_widget": {
				Name: "write_widget", Method: "POST", PathTemplate: "widgets",
				RequiredScopes: []string{"widgets:write"},
				SecretField:    "access_token", Scheme: AuthOAuth2,
			},
		},
		Resolve: func(_ context.Context, g GrantCheck) (string, error) {
			if g.ConnectionID != "conn-1" || g.Field != "access_token" || g.TargetHost != "api.example.com" {
				return "", fmt.Errorf("grant check mismatch: %+v", g)
			}
			return secret, nil
		},
		Transport: func(req *http.Request) (*http.Response, error) {
			sawAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		},
	}
	integ := &Integration{ID: "integ-1", ProviderID: "com.example/widgets", Scheme: AuthOAuth2, GrantedScopes: []string{"widgets:read"}, Enabled: true}
	conn := &Connection{ID: "conn-1", IntegrationID: "integ-1", ProviderID: "com.example/widgets", Visibility: "PRIVATE", SecretRef: "secret:conn/conn-1/access_token"}
	return ex, integ, conn, &sawAuth
}

func TestExecuteInjectsCredentialAndReturnsNoSecret(t *testing.T) {
	ex, integ, conn, sawAuth := testExecutor("tok-live-123")
	res, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "42"},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if *sawAuth != "Bearer tok-live-123" {
		t.Fatalf("credential not injected upstream: %q", *sawAuth)
	}
	if strings.Contains(string(res.Body), "tok-live-123") {
		t.Fatal("secret leaked into agent-visible body")
	}
	for k := range res.Headers {
		if strings.EqualFold(k, "Authorization") {
			t.Fatal("Authorization header leaked into agent-visible result")
		}
	}
	if res.Audit.Field != "access_token" || res.Audit.Outcome != "ok" {
		t.Fatalf("bad audit event: %+v", res.Audit)
	}
	if conn.FailureCount != 0 || conn.LastSuccessAt == nil {
		t.Fatalf("success not recorded: %+v", conn)
	}
}

func TestExecuteRefusesUngrantedScope(t *testing.T) {
	ex, integ, conn, _ := testExecutor("tok")
	_, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "write_widget",
	})
	if err == nil {
		t.Fatal("write operation allowed on read-only grant")
	}
}

func TestExecuteRefusesAbsolutePathTemplate(t *testing.T) {
	ex, integ, conn, _ := testExecutor("tok")
	ex.Operations["evil"] = Operation{Name: "evil", Method: "GET", PathTemplate: "https://evil.example.com/x", SecretField: "access_token", Scheme: AuthOAuth2}
	_, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "evil",
	})
	if err == nil {
		t.Fatal("absolute URL path template was allowed")
	}
}

func TestExecuteRefusesPathTraversalParam(t *testing.T) {
	ex, integ, conn, _ := testExecutor("tok")
	_, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "../admin"},
	})
	if err == nil {
		t.Fatal("path traversal parameter was allowed")
	}
}

func TestExecuteStripsCallerAuthorization(t *testing.T) {
	ex, integ, conn, sawAuth := testExecutor("tok-real")
	_, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "1"},
		Headers:    http.Header{"Authorization": {"Bearer attacker"}},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if *sawAuth != "Bearer tok-real" {
		t.Fatalf("caller-supplied Authorization survived: %q", *sawAuth)
	}
}

func TestExecuteRefreshesOnceThenStops(t *testing.T) {
	ex, integ, conn, _ := testExecutor("tok-old")
	calls := 0
	ex.Transport = func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	ex.Refresh = func(_ context.Context, _ *Connection) (string, *time.Time, error) {
		return "tok-new", nil, nil
	}
	res, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "1"},
	})
	if err != nil {
		t.Fatalf("expected recovery after one refresh, got: %v", err)
	}
	if res.StatusCode != 200 || calls != 2 {
		t.Fatalf("expected one refresh+replay, got status=%d calls=%d", res.StatusCode, calls)
	}

	// Second 401 with no working refresh must NOT loop.
	calls = 0
	ex.Transport = func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	ex.Refresh = func(_ context.Context, _ *Connection) (string, *time.Time, error) {
		return "", nil, nil
	}
	before := conn.FailureCount
	_, err = ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "1"},
	})
	if err == nil {
		t.Fatal("expected terminal 401 error")
	}
	if calls != 1 {
		t.Fatalf("expected exactly one upstream call, got %d", calls)
	}
	if conn.FailureCount != before+1 {
		t.Fatalf("failure not recorded once: %+v", conn)
	}
}

// TestAgentSideHandlesCarryNoSecret is the structural boundary test from
// ARCH-29 §8 item 2: everything the agent side can construct — request,
// result, audit event, manifest — must contain no secret material.
func TestAgentSideHandlesCarryNoSecret(t *testing.T) {
	const secret = "tok-super-secret-value"
	ex, integ, conn, _ := testExecutor(secret)
	res, err := ex.Execute(context.Background(), integ, conn, CallRequest{
		ComponentID: "agent", ConnectionID: "conn-1", Operation: "get_widget",
		PathParams: map[string]string{"id": "1"},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	check := func(name, s string) {
		t.Helper()
		if strings.Contains(s, secret) {
			t.Fatalf("%s contains the secret", name)
		}
	}
	check("result body", string(res.Body))
	check("audit event", FormatAuditEvent(res.Audit))
	for k, vs := range res.Headers {
		for _, v := range vs {
			check("result header "+k, v)
		}
	}
	raw, _ := io.ReadAll(strings.NewReader(validManifestJSON()))
	check("manifest fixture", string(raw))
	if res.Audit.ConnectionID == "" {
		t.Fatal("audit must still identify the connection by handle")
	}
}

func TestCensoredPlaceholderIsStableAndOpaque(t *testing.T) {
	a := CensoredPlaceholder("same-secret")
	b := CensoredPlaceholder("same-secret")
	c := CensoredPlaceholder("different-secret")
	if a != b {
		t.Fatal("placeholder not stable")
	}
	if a == c {
		t.Fatal("placeholder collides across values")
	}
	if strings.Contains(a, "same-secret") {
		t.Fatal("placeholder leaks the value")
	}
}

func TestRedactFieldsReplacesSecretPaths(t *testing.T) {
	record := map[string]any{
		"headers": map[string]any{"Authorization": "Bearer abc", "Content-Type": "application/json"},
		"body":    map[string]any{"message": "hello"},
	}
	out := RedactFields(record, []string{"headers.Authorization"}, func(path string) (string, bool) { return "", true })
	authz := out["headers"].(map[string]any)["Authorization"].(string)
	if !strings.HasPrefix(authz, ":censored:") {
		t.Fatalf("secret not redacted: %q", authz)
	}
	if out["headers"].(map[string]any)["Content-Type"] != "application/json" {
		t.Fatal("innocent field redacted")
	}
	if out["body"].(map[string]any)["message"] != "hello" {
		t.Fatal("unrelated subtree changed")
	}
	if record["headers"].(map[string]any)["Authorization"] != "Bearer abc" {
		t.Fatal("input record was mutated")
	}
}

func TestSeedManifestsParse(t *testing.T) {
	for _, f := range []string{"seeds/com.github.github.json", "seeds/com.slack.slack.json"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("seed %s unreadable: %v", f, err)
		}
		m, err := ParseManifest(data)
		if err != nil {
			t.Fatalf("seed %s invalid: %v", f, err)
		}
		policy, err := PolicyForManifest(m)
		if err != nil {
			t.Fatalf("seed %s has no pinnable egress: %v", f, err)
		}
		if len(policy.AllowedHosts) != 1 {
			t.Fatalf("seed %s must pin exactly one host", f)
		}
	}
}

func TestGrantStoreDefaultDeny(t *testing.T) {
	g := NewGrantStore()
	if _, ok := g.Lookup("c1", "agent", "access_token"); ok {
		t.Fatal("empty grant table allowed a lookup")
	}
}

func TestGrantStoreRefusesWildcards(t *testing.T) {
	g := NewGrantStore()
	for _, bad := range []FieldGrant{
		{ConnectionID: "*", ComponentID: "a", Field: "f"},
		{ConnectionID: "c", ComponentID: "*", Field: "f"},
		{ConnectionID: "c", ComponentID: "a", Field: "*"},
		{ConnectionID: "", ComponentID: "a", Field: "f"},
	} {
		if err := g.Approve(bad); err == nil {
			t.Fatalf("grant accepted: %+v", bad)
		}
	}
}

func TestGrantStoreApproveLookupRevoke(t *testing.T) {
	g := NewGrantStore()
	if err := g.Approve(FieldGrant{ConnectionID: "c1", ComponentID: "agent", Field: "access_token"}); err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	if _, ok := g.Lookup("c1", "agent", "access_token"); !ok {
		t.Fatal("approved grant not found")
	}
	if _, ok := g.Lookup("c1", "agent", "refresh_token"); ok {
		t.Fatal("unapproved field resolved")
	}
	if _, ok := g.Lookup("c1", "other-agent", "access_token"); ok {
		t.Fatal("unapproved component resolved")
	}
	g.RevokeComponent("c1", "agent")
	if _, ok := g.Lookup("c1", "agent", "access_token"); ok {
		t.Fatal("revoked grant still resolves")
	}
}

func TestVaultResolverEnforcesGrants(t *testing.T) {
	ctx := context.Background()
	mem, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	defer mem.Close()
	uri, _ := StandardRefFor("conn-9", "access_token")
	ref, _ := secrets.ParseSecretRef(uri)
	if _, err := mem.Put(ctx, ref.Namespace, ref.Key, []byte("tok-vault-1")); err != nil {
		t.Fatalf("put: %v", err)
	}

	grants := NewGrantStore()
	resolve := VaultResolver(mem, grants, StandardRefFor)

	// No grant yet: must refuse without touching the vault result.
	if _, err := resolve(ctx, GrantCheck{ComponentID: "agent", ConnectionID: "conn-9", Field: "access_token", TargetHost: "api.example.com"}); err == nil {
		t.Fatal("ungranted release succeeded")
	}
	// Grant a DIFFERENT field: still refuse.
	if err := grants.Approve(FieldGrant{ConnectionID: "conn-9", ComponentID: "agent", Field: "refresh_token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(ctx, GrantCheck{ComponentID: "agent", ConnectionID: "conn-9", Field: "access_token", TargetHost: "api.example.com"}); err == nil {
		t.Fatal("wrong-field release succeeded")
	}
	// Grant the right triple: resolves exactly once, exact value.
	if err := grants.Approve(FieldGrant{ConnectionID: "conn-9", ComponentID: "agent", Field: "access_token"}); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(ctx, GrantCheck{ComponentID: "agent", ConnectionID: "conn-9", Field: "access_token", TargetHost: "api.example.com"})
	if err != nil {
		t.Fatalf("granted release failed: %v", err)
	}
	if got != "tok-vault-1" {
		t.Fatalf("wrong value: %q", got)
	}
}

func TestStandardRefForRejectsSeparators(t *testing.T) {
	if _, err := StandardRefFor("a/b", "f"); err == nil {
		t.Fatal("slash in connection id accepted")
	}
	if _, err := StandardRefFor("c", "f:g"); err == nil {
		t.Fatal("colon in field accepted")
	}
	uri, err := StandardRefFor("conn-1", "access_token")
	if err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	if !strings.HasPrefix(uri, "secret:") {
		t.Fatalf("ref is not a secret URI: %q", uri)
	}
}

func TestGrantedFieldsListsNamesOnly(t *testing.T) {
	g := NewGrantStore()
	_ = g.Approve(FieldGrant{ConnectionID: "c", ComponentID: "a", Field: "b_token"})
	_ = g.Approve(FieldGrant{ConnectionID: "c", ComponentID: "a", Field: "a_token"})
	fields := g.GrantedFields("c", "a")
	if len(fields) != 2 || fields[0] != "a_token" || fields[1] != "b_token" {
		t.Fatalf("unexpected field list: %v", fields)
	}
}
