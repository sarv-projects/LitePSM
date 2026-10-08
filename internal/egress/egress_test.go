package egress_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/egress"
)

// netipAddr converts a parsed IP for the IsAllowedAddr table.
func netipAddr(ip net.IP) (netip.Addr, bool) {
	return netip.AddrFromSlice(ip)
}

// catalogPolicy mirrors internal/catalog's historical egress posture: https
// only with the loopback-http exception, 3 redirect hops, cross-origin
// redirects allowed (CDN edges move), no private opt-in, 30s timeout.
func catalogPolicy() egress.Policy {
	return egress.Policy{
		AllowLoopbackHTTP:   true,
		MaxRedirects:        3,
		SameOriginRedirects: false,
		AllowPrivate:        false,
		Timeout:             30 * time.Second,
		MaxBodyBytes:        16 << 20,
	}
}

// remotePolicy mirrors mcpclient's remote-MCP posture: same as catalog but
// same-origin redirects only and a 60s timeout.
func remotePolicy() egress.Policy {
	return egress.Policy{
		AllowLoopbackHTTP:   true,
		MaxRedirects:        3,
		SameOriginRedirects: true,
		AllowPrivate:        false,
		Timeout:             60 * time.Second,
		MaxBodyBytes:        16 << 20,
	}
}

func publicLookup() egress.Lookup {
	return func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
}

// egressCode extracts the LPSM error code from an error chain.
func egressCode(t *testing.T, err error) string {
	t.Helper()
	var lpsm *domain.LPSMError
	if !errors.As(err, &lpsm) {
		t.Fatalf("expected *domain.LPSMError, got %T: %v", err, err)
	}
	return lpsm.Code
}

// --- CheckURL (ported catalog table + Phase 0 additions) ----------------

func TestCheckURLAllowsCustomHTTPSOriginButRejectsUnsafeForms(t *testing.T) {
	p := catalogPolicy()
	if err := egress.CheckURL("https://custom.registry.example/catalog", p); err != nil {
		t.Fatalf("configured public custom origin should remain supported: %v", err)
	}
	for _, raw := range []string{
		"http://registry.example/catalog",
		"https://user:secret@registry.example/catalog",
		"https:///missing-host",
		"file:///etc/passwd",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := egress.CheckURL(raw, p); err == nil {
				t.Fatalf("unsafe URL %q was accepted", raw)
			}
		})
	}
}

func TestCheckURLRefusesLinkLocalMetadataAndMetadataHostnames(t *testing.T) {
	for _, p := range []egress.Policy{{}, catalogPolicy(), remotePolicy()} {
		for _, raw := range []string{
			"http://169.254.169.254",  // cloud metadata over http
			"https://169.254.169.254", // cloud metadata over https
			"http://metadata.google.internal/computeMetadata/v1/",
			"http://[fe80::1]/",  // link-local
			"https://[fe80::1]/", // link-local
			"http://100.64.0.1/", // CGNAT
		} {
			if err := egress.CheckURL(raw, p); err == nil {
				t.Errorf("CheckURL(%q) accepted under policy %+v", raw, p)
			}
		}
	}
}

func TestCheckURLLoopbackHTTPAllowedOnlyByPolicy(t *testing.T) {
	allowed := egress.Policy{AllowLoopbackHTTP: true}
	for _, raw := range []string{
		"http://127.0.0.1:8080/mcp",
		"http://localhost:8080/mcp",
		"http://[::1]:8080/mcp",
	} {
		if err := egress.CheckURL(raw, allowed); err != nil {
			t.Errorf("CheckURL(%q) with AllowLoopbackHTTP refused: %v", raw, err)
		}
		if err := egress.CheckURL(raw, egress.Policy{}); err == nil {
			t.Errorf("CheckURL(%q) without AllowLoopbackHTTP was accepted", raw)
		}
	}
}

// --- Address classification ---------------------------------------------

func TestIsAllowedAddrHonoursUnconditionalRefusalsAndKnobs(t *testing.T) {
	public := net.ParseIP("93.184.216.34")
	loopback := net.ParseIP("127.0.0.1")
	private := net.ParseIP("10.2.3.4")
	strict := egress.Policy{}
	permissive := egress.Policy{AllowLoopbackHTTP: true, AllowPrivate: true}

	alwaysRefused := []string{
		"169.254.169.254", // metadata
		"169.254.1.2",     // link-local
		"fe80::1",         // link-local v6
		"100.64.0.1",      // CGNAT
		"0.0.0.0",         // unspecified
		"255.255.255.255", // broadcast
		"ff02::1",         // multicast
		"203.0.113.7",     // documentation
		"198.18.0.1",      // benchmarking
	}
	for _, raw := range alwaysRefused {
		ip := net.ParseIP(raw)
		addr, ok := netipAddr(ip)
		if !ok {
			t.Fatalf("could not parse %q", raw)
		}
		for _, p := range []egress.Policy{strict, permissive} {
			if egress.IsAllowedAddr(addr, p) {
				t.Errorf("IsAllowedAddr(%s) allowed under %+v — no knob may reach this range", raw, p)
			}
		}
	}

	policyCases := []struct {
		name            string
		ip              net.IP
		strict, allowed bool
	}{
		{"public", public, true, true},
		{"loopback", loopback, false, true},
		{"rfc1918", private, false, true},
	}
	for _, tc := range policyCases {
		addr, _ := netipAddr(tc.ip)
		if got := egress.IsAllowedAddr(addr, strict); got != tc.strict {
			t.Errorf("%s under strict policy = %v, want %v", tc.name, got, tc.strict)
		}
		if got := egress.IsAllowedAddr(addr, permissive); got != tc.allowed {
			t.Errorf("%s under permissive policy = %v, want %v", tc.name, got, tc.allowed)
		}
	}
}

// --- ResolveChecked / simulated resolvers --------------------------------

func TestHostnameResolvingToLoopbackRefusedUnderDefaultPolicy(t *testing.T) {
	loopbackLookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	for name, p := range map[string]egress.Policy{
		"zero":             {},
		"remote":           remotePolicy(),
		"catalog":          catalogPolicy(),
		"loopback-no-http": {AllowLoopbackHTTP: false},
	} {
		if _, err := egress.ResolveCheckedWithLookup(context.Background(), "internal.example", p, loopbackLookup); err == nil {
			t.Errorf("%s: hostname resolving to 127.0.0.1 was accepted by ResolveChecked", name)
		}
		dial := egress.DialContextWithLookup(p, loopbackLookup)
		if _, err := dial(context.Background(), "tcp", "internal.example:443"); err == nil {
			t.Errorf("%s: dial to a hostname resolving to 127.0.0.1 was allowed", name)
		}
	}

	// The loopback host itself is reachable under AllowLoopbackHTTP: the
	// literal short-circuits without a lookup.
	if _, err := egress.ResolveCheckedWithLookup(context.Background(), "127.0.0.1", remotePolicy(), loopbackLookup); err != nil {
		t.Fatalf("loopback literal refused under AllowLoopbackHTTP: %v", err)
	}
}

func TestResolveCheckedRefusesMetadataCGNATAndMixedAnswers(t *testing.T) {
	cases := []struct {
		name   string
		lookup egress.Lookup
	}{
		{"metadata-dns", func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		}},
		{"cgnat-dns", func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("100.64.0.1")}}, nil
		}},
		{"mixed-public-private", func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("93.184.216.34")},
				{IP: net.ParseIP("10.0.0.8")},
			}, nil
		}},
		{"dns-failure", func(context.Context, string) ([]net.IPAddr, error) {
			return nil, net.UnknownNetworkError("no DNS")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := egress.ResolveCheckedWithLookup(context.Background(), "origin.example", remotePolicy(), tc.lookup); err == nil {
				t.Fatal("ResolveChecked accepted an answer the policy refuses")
			}
		})
	}
}

func TestPrivateHTTPSRefusedByDefaultAndAllowedOnlyByAllowPrivateOptIn(t *testing.T) {
	ctx := context.Background()

	// Default/remote posture: an RFC1918 endpoint is refused outright.
	if _, err := egress.ResolveCheckedWithLookup(ctx, "10.2.3.4", remotePolicy(), nil); err == nil {
		t.Fatal("private https destination accepted without AllowPrivate")
	}
	if _, err := egress.ResolveCheckedWithLookup(ctx, "192.168.1.5", egress.Policy{}, nil); err == nil {
		t.Fatal("private destination accepted under the zero policy")
	}

	// Decision 4 opt-in: AllowPrivate is what makes a LAN endpoint reachable.
	allowed := remotePolicy()
	allowed.AllowPrivate = true
	if _, err := egress.ResolveCheckedWithLookup(ctx, "10.2.3.4", allowed, nil); err != nil {
		t.Fatalf("private destination refused with AllowPrivate: %v", err)
	}
}

func TestLinkLocalMetadataAndCGNATRefusedWithAndWithoutAllowPrivate(t *testing.T) {
	ctx := context.Background()
	// The knob must not reach link-local/metadata/CGNAT — with OR without
	// AllowPrivate, and regardless of a configured host.
	hosts := []string{"169.254.169.254", "169.254.1.2", "100.64.0.1"}
	for _, host := range hosts {
		for name, p := range map[string]egress.Policy{
			"without-allow-private": {},
			"with-allow-private":    {AllowPrivate: true, AllowLoopbackHTTP: true},
			"with-configured-host":  {AllowedPrivateHost: host},
		} {
			if _, err := egress.ResolveCheckedWithLookup(ctx, host, p, nil); err == nil {
				t.Errorf("%s accepted %s — the refusal must be unconditional", name, host)
			}
		}
	}
}

// --- CheckRedirect (ported catalog cases + Phase 0 additions) ------------

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func redirectReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	return &http.Request{URL: mustParseURL(t, raw)}
}

func TestRedirectAllowsPublicCrossOriginUnderCatalogPolicy(t *testing.T) {
	via := []*http.Request{{URL: mustParseURL(t, "https://custom.registry.example/v1/current.json")}}
	err := egress.CheckRedirectWithLookup(catalogPolicy(),
		redirectReq(t, "https://cdn.example.net/v1/current.json"), via, publicLookup())
	if err != nil {
		t.Fatalf("public CDN redirect should be allowed under the catalog policy: %v", err)
	}
}

func TestRedirectRefusesCrossOriginUnderRemotePolicyButAllowsSameOrigin(t *testing.T) {
	previous := "https://custom.registry.example/v1/current.json"
	via := []*http.Request{{URL: mustParseURL(t, previous)}}

	cross := redirectReq(t, "https://cdn.example.net/v1/current.json")
	if err := egress.CheckRedirectWithLookup(remotePolicy(), cross, via, publicLookup()); err == nil {
		t.Fatal("cross-host redirect accepted under SameOriginRedirects")
	}
	// Scheme change is an origin change too (checked without tripping the
	// downgrade rule first: prev is http, target is https).
	httpVia := []*http.Request{{URL: mustParseURL(t, "http://custom.registry.example/v1/current.json")}}
	schemeChange := redirectReq(t, "https://custom.registry.example/v1/current.json")
	if err := egress.CheckRedirectWithLookup(remotePolicy(), schemeChange, httpVia, publicLookup()); err == nil {
		t.Fatal("scheme-changing redirect accepted under SameOriginRedirects")
	}

	same := redirectReq(t, "https://custom.registry.example/v2/current.json")
	if err := egress.CheckRedirectWithLookup(remotePolicy(), same, via, publicLookup()); err != nil {
		t.Fatalf("same-origin redirect should be allowed: %v", err)
	}
}

func TestRedirectRefusesLinkLocalTargets(t *testing.T) {
	via := []*http.Request{{URL: mustParseURL(t, "https://registry.example/v1/current.json")}}
	for _, raw := range []string{
		"https://169.254.169.254/latest/meta-data",
		"https://[fe80::1]/latest",
	} {
		t.Run(raw, func(t *testing.T) {
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				t.Fatal("literal link-local address must not be resolved")
				return nil, nil
			}
			if err := egress.CheckRedirectWithLookup(remotePolicy(), redirectReq(t, raw), via, lookup); err == nil {
				t.Fatalf("redirect to link-local target %q was accepted", raw)
			}
			if err := egress.CheckRedirectWithLookup(catalogPolicy(), redirectReq(t, raw), via, lookup); err == nil {
				t.Fatalf("catalog policy accepted redirect to link-local target %q", raw)
			}
		})
	}
}

func TestRedirectRejectsPrivateAndNonPublicTargets(t *testing.T) {
	via := []*http.Request{{URL: mustParseURL(t, "https://registry.example/v1/current.json")}}
	for _, raw := range []string{
		"https://127.0.0.1/latest",
		"https://10.2.3.4/latest",
		"https://100.64.0.1/latest",
		"https://198.18.0.1/latest",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/latest",
		"https://[fd00::1]/latest",
		"https://0.0.0.0/latest",
	} {
		t.Run(raw, func(t *testing.T) {
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				t.Fatal("literal IP address should not be resolved")
				return nil, nil
			}
			if err := egress.CheckRedirectWithLookup(catalogPolicy(), redirectReq(t, raw), via, lookup); err == nil {
				t.Fatalf("redirect to non-public address %q was accepted", raw)
			}
		})
	}

	target := redirectReq(t, "https://mixed-dns.example/latest")
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("10.0.0.8")},
		}, nil
	}
	if err := egress.CheckRedirectWithLookup(catalogPolicy(), target, via, lookup); err == nil {
		t.Fatal("redirect with mixed public/private DNS answers was accepted")
	}
}

func TestRedirectRejectsDNSFailure(t *testing.T) {
	target := redirectReq(t, "https://unresolvable.example/latest")
	via := []*http.Request{{URL: mustParseURL(t, "https://registry.example/v1/current.json")}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return nil, net.UnknownNetworkError("no DNS")
	}
	if err := egress.CheckRedirectWithLookup(catalogPolicy(), target, via, lookup); err == nil {
		t.Fatal("redirect with failed DNS lookup was accepted")
	}
}

func TestRedirectRejectsNonDefaultHTTPSPort(t *testing.T) {
	target := redirectReq(t, "https://cdn.example.net:8443/current.json")
	via := []*http.Request{{URL: mustParseURL(t, "https://registry.example/v1/current.json")}}
	if err := egress.CheckRedirectWithLookup(catalogPolicy(), target, via, publicLookup()); err == nil {
		t.Fatal("redirect to a non-default HTTPS port was accepted")
	}
}

func TestRedirectKeepsDowngradeAndHopBounds(t *testing.T) {
	previous := mustParseURL(t, "https://registry.example/v1/current.json")

	// https → http downgrade is refused under both policies.
	downgrade := redirectReq(t, "http://cdn.example.net/current.json")
	via := []*http.Request{{URL: previous}}
	for name, p := range map[string]egress.Policy{"catalog": catalogPolicy(), "remote": remotePolicy()} {
		if err := egress.CheckRedirectWithLookup(p, downgrade, via, nil); err == nil {
			t.Errorf("%s: HTTPS-to-HTTP redirect was accepted", name)
		}
	}

	// Hop cap: exactly MaxRedirects (3) hops accepted, the next refused.
	target := redirectReq(t, "https://cdn.example.net/current.json")
	hops := make([]*http.Request, 3)
	for i := range hops {
		hops[i] = &http.Request{URL: previous}
	}
	if err := egress.CheckRedirectWithLookup(catalogPolicy(), target, hops, publicLookup()); err != nil {
		t.Fatalf("three-hop redirect chain should be accepted: %v", err)
	}
	hops = append(hops, &http.Request{URL: previous})
	if err := egress.CheckRedirectWithLookup(catalogPolicy(), target, hops, publicLookup()); err == nil {
		t.Fatal("redirect chain over three hops was accepted")
	}
	// The zero policy defaults to the same 3-hop bound.
	if err := egress.CheckRedirectWithLookup(egress.Policy{}, target, hops, publicLookup()); err == nil {
		t.Fatal("zero-value policy did not default MaxRedirects to 3")
	}
}

// --- Transport / client shape --------------------------------------------

func TestGuardedTransportDisablesProxyAndDialTLSAndPinsTheDial(t *testing.T) {
	guard := egress.NewGuardedTransport(nil, egress.Policy{})
	transport, ok := guard.Base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport type = %T, want *http.Transport", guard.Base)
	}
	if transport.Proxy != nil {
		t.Error("guarded transport delegates destination resolution to a proxy")
	}
	if transport.DialTLSContext != nil {
		t.Error("DialTLSContext must be cleared so TLS flows through the checked dial")
	}
	if transport.DialContext == nil {
		t.Error("guarded transport must install the checked-IP dialer")
	}
}

func TestNewClientCarriesGuardedDefaults(t *testing.T) {
	client := egress.NewClient(egress.Policy{})
	if client.Timeout != egress.DefaultTimeout {
		t.Errorf("zero-policy timeout = %v, want %v", client.Timeout, egress.DefaultTimeout)
	}
	if client.CheckRedirect == nil {
		t.Fatal("guarded client must carry the redirect policy")
	}
	if _, ok := client.Transport.(egress.GuardedTransport); !ok {
		t.Fatalf("transport type = %T, want egress.GuardedTransport", client.Transport)
	}
	if got := egress.NewClient(egress.Policy{Timeout: 7 * time.Second}).Timeout; got != 7*time.Second {
		t.Errorf("configured timeout = %v, want 7s", got)
	}
}

func TestWrapClientKeepsCallerSettingsAndIsNotMutating(t *testing.T) {
	caller := &http.Client{Timeout: 5 * time.Second}
	wrapped := egress.WrapClient(caller, remotePolicy())
	if wrapped.Timeout != 5*time.Second {
		t.Errorf("caller timeout was overwritten: %v", wrapped.Timeout)
	}
	if caller.CheckRedirect != nil || caller.Transport != nil {
		t.Error("WrapClient mutated the caller-owned client")
	}
	if wrapped.CheckRedirect == nil {
		t.Fatal("wrapped client must carry the redirect policy")
	}
}

func TestWrapClientRunsGuardBeforeCallerRedirectPolicy(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://off-origin.example"+target.URL, http.StatusFound)
	}))
	defer origin.Close()

	// The caller would allow any redirect; the guard must refuse it first.
	caller := origin.Client()
	caller.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	client := egress.WrapClient(caller, remotePolicy())

	if _, err := client.Get(origin.URL); err == nil {
		t.Fatal("off-origin redirect was accepted")
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("off-origin redirect target received %d request(s)", got)
	}
}

// --- End-to-end over httptest (loopback http per policy) ------------------

func TestLoopbackHTTPFlowsWorkUnderRemotePolicy(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := egress.NewClient(remotePolicy()).Get(server.URL)
	if err != nil {
		t.Fatalf("loopback http request refused under AllowLoopbackHTTP: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("server received %d requests, want 1", hits.Load())
	}

	// The same request with the knob off never reaches the server.
	off := remotePolicy()
	off.AllowLoopbackHTTP = false
	before := hits.Load()
	if _, err := egress.NewClient(off).Get(server.URL); err == nil {
		t.Fatal("loopback http request allowed without AllowLoopbackHTTP")
	}
	if hits.Load() != before {
		t.Fatalf("refused request still reached the server (%d → %d)", before, hits.Load())
	}
}
