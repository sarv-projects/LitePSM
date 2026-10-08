// Package egress is LiteSPM's shared outbound-HTTP (SSRF) guard.
//
// Every remote fetch the product makes — catalog sync, remote (URL) MCP
// endpoints, and anything added later — is forced through this package so the
// rules exist in exactly one place (two copies drift; see
// .slim/deepwork/b1-remote-mcp-research.md PART 2). The guard runs in three
// layers, all driven by one Policy:
//
//  1. CheckURL — static URL rules, no DNS: https-only (plain http only for
//     loopback when Policy.AllowLoopbackHTTP), a required host, no embedded
//     credentials, and refusal of literal hosts in ranges no policy may allow
//     (link-local/metadata, CGNAT, documentation, other special-use).
//  2. ResolveChecked / CheckRedirect — resolve every address and fail if ANY
//     address is not allowed (not "any one is fine"); redirect chains are
//     bounded, never downgrade https→http, re-run CheckURL per hop, default
//     port only, and — under Policy.SameOriginRedirects — never change origin.
//  3. DialContext / GuardedTransport — dial-time checked-IP pinning: resolve,
//     classify, then dial the checked address directly (rebinding-proof), with
//     the environment proxy disabled and DialTLSContext cleared so TLS cannot
//     bypass the checked dial. GuardedTransport re-runs layer 1+2 on every
//     request before delegating.
//
// Policy defaults are fail-closed: link-local/metadata/CGNAT are refused
// unconditionally with no knob, RFC1918/ULA is refused unless
// Policy.AllowPrivate, loopback is refused unless Policy.AllowLoopbackHTTP,
// and credentials in a URL are always refused.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

const (
	// DefaultMaxRedirects bounds a redirect chain when Policy.MaxRedirects
	// is unset (<= 0). ARCH/03 §5 documents a cap of 3 hops.
	DefaultMaxRedirects = 3

	// DefaultTimeout bounds one guarded HTTP exchange when Policy.Timeout
	// is unset (<= 0). It matches config.DefaultConfig's Network.Timeout,
	// so a caller that never looks at configuration still gets 30s.
	DefaultTimeout = 30 * time.Second
)

// Policy is the egress posture of one guarded client or one check. The zero
// value is strict: https only, at most DefaultMaxRedirects redirect hops,
// cross-origin redirects allowed (the historical default; set
// SameOriginRedirects to refuse them), private and loopback destinations
// refused, and no configured exception host.
type Policy struct {
	// AllowLoopbackHTTP permits plain http:// URLs to loopback hosts
	// (127.0.0.0/8, ::1, localhost) and permits loopback addresses when the
	// dialled host is itself loopback. Set by catalog sync (hermetic
	// httptest origins) and by remote-MCP policies (a local dev endpoint).
	AllowLoopbackHTTP bool

	// MaxRedirects bounds a redirect chain; <= 0 selects
	// DefaultMaxRedirects. A chain of exactly MaxRedirects hops is still
	// allowed; the hop after that is refused.
	MaxRedirects int

	// SameOriginRedirects refuses any redirect that changes scheme, host or
	// effective port (Decision 3: a remote MCP client may carry
	// Authorization/session headers, which a cross-host hop would forward to
	// a third party). The catalog keeps this false — CDN edges move.
	SameOriginRedirects bool

	// AllowPrivate opts in to RFC1918 / IPv6-ULA destinations (Decision 4:
	// a LAN server is a real use case and the canonical SSRF pivot, so it
	// must be an explicit, consent-visible choice). Never affects
	// link-local, metadata, CGNAT or other special-use ranges.
	AllowPrivate bool

	// AllowedPrivateHost is the one configured literal host exempt from the
	// public-address rule ("localhost" or a non-public IP literal, as
	// returned by ConfiguredPrivateHost). It never exempts ranges refused
	// unconditionally.
	AllowedPrivateHost string

	// Timeout bounds the client NewClient builds; <= 0 selects
	// DefaultTimeout. WrapClient never overwrites a caller's timeout.
	Timeout time.Duration

	// MaxBodyBytes is the response-body bound for fetches the guard's
	// callers make (probe/metadata reads). This package does not read
	// bodies; callers enforce it with io.LimitReader.
	MaxBodyBytes int64
}

// maxRedirects resolves the effective hop cap.
func (p Policy) maxRedirects() int {
	if p.MaxRedirects <= 0 {
		return DefaultMaxRedirects
	}
	return p.MaxRedirects
}

// timeout resolves the effective client timeout for NewClient.
func (p Policy) timeout() time.Duration {
	if p.Timeout <= 0 {
		return DefaultTimeout
	}
	return p.Timeout
}

// Lookup resolves host to addresses. The signature matches
// net.Resolver.LookupIPAddr so callers and tests inject unchanged.
type Lookup func(ctx context.Context, host string) ([]net.IPAddr, error)

// defaultLookup is the system resolver.
func defaultLookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// nonPublicPrefixes are special-use ranges that are never a permitted
// destination, under any policy and regardless of any configured host:
// CGNAT/shared address space, protocol assignments, documentation, deprecated
// relay anycast, benchmarking, reserved space, and the IPv6 equivalents.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // shared address space (CGNAT)
	netip.MustParsePrefix("192.0.0.0/24"),   // protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),   // documentation
	netip.MustParsePrefix("192.88.99.0/24"), // deprecated relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

// normalizeAddr converts a parsed IP to an unmapped netip.Addr.
func normalizeAddr(ip net.IP) (netip.Addr, bool) {
	if ip == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// isPublicAddr reports globally routable unicast addresses only: no loopback,
// private, link-local, multicast, unspecified or special-use prefix (the
// extracted catalogIPIsPublic rule, policy-free).
func isPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// IsPublicIP reports whether ip passes the public-destination check with no
// policy additions (the extracted catalogIPIsPublic).
func IsPublicIP(ip net.IP) bool {
	addr, ok := normalizeAddr(ip)
	if !ok {
		return false
	}
	return isPublicAddr(addr)
}

// refusedWithoutKnob reports addresses that no policy may ever allow:
// link-local (including the 169.254.169.254 metadata endpoint), CGNAT and
// other special-use ranges, multicast, unspecified, broadcast — everything
// non-public that is neither loopback nor RFC1918/ULA (the two classes a
// knob can opt into).
func refusedWithoutKnob(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	if addr.IsLoopback() || addr.IsPrivate() {
		return false
	}
	return !isPublicAddr(addr)
}

// IsAllowedAddr reports whether addr is an acceptable destination under p —
// the public-destination rule plus the policy additions. It has no host
// context: the configured-host exception and the loopback-host qualification
// live in the resolve/dial layers, which know which host is being asked for.
func IsAllowedAddr(addr netip.Addr, p Policy) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if isPublicAddr(addr) {
		return true
	}
	if refusedWithoutKnob(addr) {
		return false
	}
	if addr.IsLoopback() {
		return p.AllowLoopbackHTTP
	}
	return p.AllowPrivate // RFC1918 / ULA
}

// configuredPrivateDestination reports whether host (optionally resolved to
// ip) is the policy's one configured private destination. A "localhost"
// configuration accepts any loopback address; a literal configuration
// accepts that literal (or no address yet, when classifying before a
// resolution).
func configuredPrivateDestination(host string, ip net.IP, allowedPrivateHost string) bool {
	if allowedPrivateHost == "" || !strings.EqualFold(host, allowedPrivateHost) {
		return false
	}
	if strings.EqualFold(allowedPrivateHost, "localhost") {
		return ip == nil || ip.IsLoopback()
	}
	configuredIP := net.ParseIP(allowedPrivateHost)
	return configuredIP != nil && (ip == nil || configuredIP.Equal(ip))
}

// ConfiguredPrivateHost returns the guard-relevant literal host of raw —
// "localhost" or a non-public IP literal — else "" (the extracted
// configuredPrivateHost: a config-supplied private registry is a deliberate,
// explicit trust decision).
func ConfiguredPrivateHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.EqualFold(host, "localhost") {
		return host
	}
	ip := net.ParseIP(host)
	if ip != nil && !IsPublicIP(ip) {
		return host
	}
	return ""
}

// IsLoopbackHost reports loopback/test hosts where plain http (and loopback
// dials) are acceptable when Policy.AllowLoopbackHTTP is set (httptest binds
// 127.0.0.1; never a production origin).
func IsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// allowsDestination is the resolve/dial-time rule for one address of host:
// public always passes; the one configured literal host passes for its own
// addresses; then the policy additions — RFC1918/ULA under AllowPrivate, and
// loopback addresses when the host itself is loopback under
// AllowLoopbackHTTP. A hostname that merely resolves to loopback does NOT
// pass (the host must be loopback itself), so hostile DNS cannot reclassify
// an origin as local. Ranges refusedWithoutKnob never pass, configured host
// or not.
func (p Policy) allowsDestination(host string, ip net.IP) bool {
	addr, ok := normalizeAddr(ip)
	if !ok {
		return false
	}
	if isPublicAddr(addr) {
		return true
	}
	if refusedWithoutKnob(addr) {
		return false
	}
	if configuredPrivateDestination(host, ip, p.AllowedPrivateHost) {
		return true
	}
	if addr.IsPrivate() {
		return p.AllowPrivate
	}
	// addr is loopback here.
	return p.AllowLoopbackHTTP && IsLoopbackHost(host)
}

// CheckURL enforces the static rules of p on raw: no parse errors, no opaque
// URLs, a required host, no embedded credentials, https-only (plain http only
// for loopback hosts when p.AllowLoopbackHTTP), and — for literal IP hosts —
// refusal of ranges no policy may allow. It performs no DNS: RFC1918 and
// loopback literals are decided by the resolve/dial layers so a configured
// private-registry host keeps working.
func CheckURL(raw string, p Policy) error {
	u, err := url.Parse(raw)
	if err != nil {
		return domain.ErrEgressBlocked(raw, fmt.Sprintf("unparseable URL: %v", err))
	}
	if u.Opaque != "" || u.Hostname() == "" || u.User != nil {
		return domain.ErrEgressBlocked(raw, "URLs must have a host and must not contain credentials")
	}
	schemeAllowed := u.Scheme == "https" ||
		(u.Scheme == "http" && p.AllowLoopbackHTTP && IsLoopbackHost(u.Hostname()))
	if !schemeAllowed {
		return domain.ErrEgressBlocked(raw, fmt.Sprintf("URLs require https (got %q)", u.Scheme))
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if addr, ok := normalizeAddr(ip); !ok || refusedWithoutKnob(addr) {
			return domain.ErrEgressBlocked(raw,
				fmt.Sprintf("host %q is in an address range that is never permitted", u.Hostname()))
		}
	}
	return nil
}

// resolveHost resolves host: a literal returns itself, a name goes through
// lookup. Every failure is fail-closed with ErrEgressBlocked.
func resolveHost(ctx context.Context, host string, lookup Lookup) ([]net.IP, error) {
	if lookup == nil {
		lookup = defaultLookup
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, domain.ErrEgressBlocked(host, fmt.Sprintf("DNS resolution failed: %v", err))
	}
	if len(addrs) == 0 {
		return nil, domain.ErrEgressBlocked(host, "DNS returned no addresses")
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP == nil {
			return nil, domain.ErrEgressBlocked(host, "DNS returned an invalid address")
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

// ResolveChecked resolves host under p and fails if ANY address is not
// allowed (not "any address is fine"): one bad answer poisons the whole
// destination. It returns the checked addresses so callers can pin them.
func ResolveChecked(ctx context.Context, host string, p Policy) ([]netip.Addr, error) {
	return ResolveCheckedWithLookup(ctx, host, p, defaultLookup)
}

// ResolveCheckedWithLookup is ResolveChecked with an injected resolver.
func ResolveCheckedWithLookup(ctx context.Context, host string, p Policy, lookup Lookup) ([]netip.Addr, error) {
	ips, err := resolveHost(ctx, host, lookup)
	if err != nil {
		return nil, err
	}
	checked := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if !p.allowsDestination(host, ip) {
			return nil, domain.ErrEgressBlocked(host, "destination resolved to an address refused by the egress policy")
		}
		addr, _ := normalizeAddr(ip)
		checked = append(checked, addr)
	}
	return checked, nil
}

// resolvePublic resolves host and requires every address to be public with
// no policy additions. It backs the redirect re-check: a redirect is an
// origin-controlled jump to a destination the user never typed, so neither
// the configured-host exception nor any opt-in applies there.
func resolvePublic(ctx context.Context, host string, lookup Lookup) error {
	ips, err := resolveHost(ctx, host, lookup)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !IsPublicIP(ip) {
			return domain.ErrEgressBlocked(host, "destination resolved to a non-public address")
		}
	}
	return nil
}

// CheckRedirect bounds one redirect hop under p: at most MaxRedirects hops
// (default 3), no https→http downgrade, no origin change when
// SameOriginRedirects is set, CheckURL re-run on the target, default HTTPS
// port only, and every target address re-validated as public. Identical
// origin is not required when SameOriginRedirects is false (CDN edges move),
// but a downgrade is never a CDN move — it is a strip.
func CheckRedirect(p Policy, req *http.Request, via []*http.Request) error {
	return CheckRedirectWithLookup(p, req, via, defaultLookup)
}

// CheckRedirectWithLookup is CheckRedirect with an injected resolver.
func CheckRedirectWithLookup(p Policy, req *http.Request, via []*http.Request, lookup Lookup) error {
	if lookup == nil {
		lookup = defaultLookup
	}
	if max := p.maxRedirects(); len(via) > max {
		return fmt.Errorf("redirect chain exceeds %d hops", max)
	}
	if len(via) > 0 {
		prev := via[len(via)-1].URL
		if prev.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect downgrade from https to %s", req.URL.Scheme)
		}
		if p.SameOriginRedirects && !sameOrigin(prev, req.URL) {
			return domain.ErrEgressBlocked(req.URL.String(),
				"redirect changed origin; configure the final URL directly")
		}
	}
	if err := CheckURL(req.URL.String(), p); err != nil {
		return err
	}
	if port := req.URL.Port(); port != "" && port != "443" {
		return domain.ErrEgressBlocked(req.URL.String(), "redirects may only use the default HTTPS port")
	}
	return resolvePublic(req.Context(), req.URL.Hostname(), lookup)
}

// sameOrigin compares scheme, host and effective port.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

// effectivePort returns the explicit port or the scheme's default.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return ""
}

// checkRequest is the transport-level re-check: CheckURL plus a destination
// check of the request host — unless the host is the configured private
// destination, which is not resolved at all (a configured literal needs no
// DNS and must not be refused by it).
func checkRequest(req *http.Request, p Policy, lookup Lookup) error {
	if err := CheckURL(req.URL.String(), p); err != nil {
		return err
	}
	host := req.URL.Hostname()
	if configuredPrivateDestination(host, nil, p.AllowedPrivateHost) {
		return nil
	}
	_, err := ResolveCheckedWithLookup(req.Context(), host, p, lookup)
	return err
}

// DialContext returns the checked dialer for p: resolve → classify → dial
// the checked address directly, so a rebind between check and connect cannot
// change the destination.
func DialContext(p Policy) func(context.Context, string, string) (net.Conn, error) {
	return DialContextWithLookup(p, defaultLookup)
}

// DialContextWithLookup is DialContext with an injected resolver.
func DialContextWithLookup(p Policy, lookup Lookup) func(context.Context, string, string) (net.Conn, error) {
	if lookup == nil {
		lookup = defaultLookup
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := resolveHost(ctx, host, lookup)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !p.allowsDestination(host, ip) {
				return nil, domain.ErrEgressBlocked(host, "destination resolved to an address refused by the egress policy")
			}
		}
		dialer := &net.Dialer{}
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = domain.ErrEgressBlocked(host, "destination resolved to no addresses")
		}
		return nil, lastErr
	}
}

// ConfigureTransport returns the inner transport for guarded egress: an
// *http.Transport is cloned with its proxy disabled (a proxy would resolve
// the origin for us, bypassing the checked-IP dial), DialTLSContext cleared
// (TLS must flow through the checked DialContext), and DialContext replaced
// by the checked dialer of p. Any other RoundTripper is returned unchanged —
// it cannot be dial-pinned, but still receives the request-level checks.
func ConfigureTransport(base http.RoundTripper, p Policy) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		clone := transport.Clone()
		clone.Proxy = nil
		clone.DialTLSContext = nil
		clone.DialContext = DialContext(p)
		return clone
	}
	return base
}

// GuardedTransport is the guarded-transport shape: the configured inner
// transport plus the policy applied to every request before delegating.
type GuardedTransport struct {
	// Base is the inner transport from ConfigureTransport (proxy off,
	// checked-IP dial).
	Base http.RoundTripper

	// Policy is the egress policy enforced on every request.
	Policy Policy

	// Lookup resolves hostnames for the request-time destination check;
	// nil selects the system resolver.
	Lookup Lookup
}

// NewGuardedTransport configures base for guarded egress under p and wraps
// it with p's request checks.
func NewGuardedTransport(base http.RoundTripper, p Policy) GuardedTransport {
	return GuardedTransport{Base: ConfigureTransport(base, p), Policy: p}
}

// RoundTrip enforces the policy on req before delegating to the inner
// transport.
func (g GuardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	lookup := g.Lookup
	if lookup == nil {
		lookup = defaultLookup
	}
	if err := checkRequest(req, g.Policy, lookup); err != nil {
		return nil, err
	}
	base := g.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// NewClient builds a fully guarded HTTP client for p: bounded timeout,
// guarded transport, and the redirect policy on every hop. This is the
// fail-closed default — never ship a bare &http.Client{} for a remote
// endpoint again.
func NewClient(p Policy) *http.Client {
	return &http.Client{
		Timeout:   p.timeout(),
		Transport: NewGuardedTransport(nil, p),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return CheckRedirect(p, req, via)
		},
	}
}

// WrapClient returns a copy of c guarded by p — the caller's client is never
// mutated. Its timeout and other settings are kept, its CheckRedirect (if
// any) runs after the guard, and its transport is replaced by the guarded
// shape. A nil c builds a fresh guarded client via NewClient.
func WrapClient(c *http.Client, p Policy) *http.Client {
	if c == nil {
		return NewClient(p)
	}
	clone := *c
	callerRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := CheckRedirect(p, req, via); err != nil {
			return err
		}
		if callerRedirect != nil {
			return callerRedirect(req, via)
		}
		return nil
	}
	clone.Transport = NewGuardedTransport(clone.Transport, p)
	return &clone
}
