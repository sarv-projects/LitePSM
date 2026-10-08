package catalog

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestCheckCatalogURLAllowsCustomHTTPSOriginButRejectsUnsafeForms(t *testing.T) {
	if err := checkCatalogURL("https://custom.registry.example/catalog"); err != nil {
		t.Fatalf("configured public custom origin should remain supported: %v", err)
	}
	for _, raw := range []string{
		"http://registry.example/catalog",
		"https://user:secret@registry.example/catalog",
		"https:///missing-host",
		"file:///etc/passwd",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := checkCatalogURL(raw); err == nil {
				t.Fatalf("unsafe catalog URL %q was accepted", raw)
			}
		})
	}
}

func TestCatalogRedirectAllowsPublicCrossOriginCDN(t *testing.T) {
	target, _ := url.Parse("https://cdn.example.net/v1/current.json")
	previous, _ := url.Parse("https://custom.registry.example/v1/current.json")
	req := &http.Request{URL: target}
	via := []*http.Request{{URL: previous}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	if err := catalogCheckRedirectWithLookup(req, via, lookup); err != nil {
		t.Fatalf("public CDN redirect should be allowed: %v", err)
	}
}

func TestCatalogRedirectRejectsPrivateAndNonPublicTargets(t *testing.T) {
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
			target, _ := url.Parse(raw)
			previous, _ := url.Parse("https://registry.example/v1/current.json")
			req := &http.Request{URL: target}
			via := []*http.Request{{URL: previous}}
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				t.Fatal("literal IP address should not be resolved")
				return nil, nil
			}
			if err := catalogCheckRedirectWithLookup(req, via, lookup); err == nil {
				t.Fatalf("redirect to non-public address %q was accepted", raw)
			}
		})
	}

	target, _ := url.Parse("https://mixed-dns.example/latest")
	previous, _ := url.Parse("https://registry.example/v1/current.json")
	req := &http.Request{URL: target}
	via := []*http.Request{{URL: previous}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("10.0.0.8")},
		}, nil
	}
	if err := catalogCheckRedirectWithLookup(req, via, lookup); err == nil {
		t.Fatal("redirect with mixed public/private DNS answers was accepted")
	}
}

func TestCatalogRedirectRejectsDNSFailure(t *testing.T) {
	target, _ := url.Parse("https://unresolvable.example/latest")
	previous, _ := url.Parse("https://registry.example/v1/current.json")
	req := &http.Request{URL: target}
	via := []*http.Request{{URL: previous}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return nil, net.UnknownNetworkError("no DNS")
	}
	if err := catalogCheckRedirectWithLookup(req, via, lookup); err == nil {
		t.Fatal("redirect with failed DNS lookup was accepted")
	}
}

func TestCatalogRedirectRejectsNonDefaultHTTPSPort(t *testing.T) {
	target, _ := url.Parse("https://cdn.example.net:8443/current.json")
	previous, _ := url.Parse("https://registry.example/v1/current.json")
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	if err := catalogCheckRedirectWithLookup(
		&http.Request{URL: target}, []*http.Request{{URL: previous}}, lookup); err == nil {
		t.Fatal("redirect to a non-default HTTPS port was accepted")
	}
}

func TestCatalogDialRejectsPrivateDNSBeforeConnecting(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.9")}}, nil
	}
	dial := catalogDialContextWithLookup("", lookup)
	if _, err := dial(context.Background(), "tcp", "catalog.example:443"); err == nil {
		t.Fatal("dial to a hostname resolving to a private address was allowed")
	}
}

func TestCatalogDefaultTransportDoesNotDelegateDNSToProxy(t *testing.T) {
	for name, client := range map[string]*http.Client{
		"default":  secureCatalogHTTPClient(0, "https://catalog.example"),
		"injected": NewClient("https://catalog.example", t.TempDir(), &http.Client{}).httpClient,
	} {
		guarded, ok := client.Transport.(catalogURLGuardTransport)
		if !ok {
			t.Fatalf("%s transport type = %T, want catalog URL guard", name, client.Transport)
		}
		transport, ok := guarded.base.(*http.Transport)
		if !ok {
			t.Fatalf("%s base transport type = %T, want *http.Transport", name, guarded.base)
		}
		if transport.Proxy != nil {
			t.Errorf("%s transport delegates destination resolution to a proxy", name)
		}
	}
}

func TestCatalogRedirectKeepsDowngradeAndHopBounds(t *testing.T) {
	target, _ := url.Parse("http://cdn.example.net/current.json")
	previous, _ := url.Parse("https://registry.example/v1/current.json")
	if err := catalogCheckRedirectWithLookup(&http.Request{URL: target}, []*http.Request{{URL: previous}}, nil); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect was accepted")
	}

	target, _ = url.Parse("https://cdn.example.net/current.json")
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	via := make([]*http.Request, 3)
	for i := range via {
		via[i] = &http.Request{URL: previous}
	}
	if err := catalogCheckRedirectWithLookup(&http.Request{URL: target}, via, lookup); err != nil {
		t.Fatalf("three-hop redirect chain should be accepted: %v", err)
	}
	via = append(via, &http.Request{URL: previous})
	if err := catalogCheckRedirectWithLookup(&http.Request{URL: target}, via, lookup); err == nil {
		t.Fatal("redirect chain over three hops was accepted")
	}
}

func TestCatalogHTTPRedirectDoesNotReachLoopbackTarget(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/private", http.StatusFound)
	}))
	defer origin.Close()

	customHTTPClient := origin.Client()
	customHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	client := NewClient(origin.URL, "", customHTTPClient)
	if _, err := client.httpClient.Get(origin.URL); err == nil {
		t.Fatal("private redirect should be rejected")
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("private redirect target received %d request(s)", got)
	}
}
