package connector

// egress.go — the outbound SSRF policy every connector call passes through.
//
// A desktop agent proxies from the user's own network and LAN, so the egress
// policy matters MORE here than on a hosted platform: the caller's loopback,
// link-local metadata endpoints (169.254.169.254), and RFC 1918 neighbours are
// all one confused-deputy request away. The rules, in order:
//
//  1. The target URL is derived from the manifest's pinned base URL, never
//     from agent arguments. The agent supplies a path, not a host.
//  2. Private, loopback, link-local, multicast and unspecified addresses are
//     refused — after DNS resolution, not just by string match.
//  3. Redirects are followed at most MaxRedirects hops, and every hop is
//     re-resolved and re-checked. A redirect off the pinned domain fails.
//  4. The agent may never supply Authorization, Proxy-Authorization, or Cookie
//     headers. Credential injection happens exactly once, inside the executor,
//     after all checks pass.
//
// The checker is a pure function over a Resolver so tests can pin DNS without
// touching the network.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// MaxRedirects caps redirect following per call.
const MaxRedirects = 3

// MaxResponseBytes caps how much of a provider response the executor buffers.
const MaxResponseBytes = 4 << 20 // 4 MiB

// Resolver maps a hostname to its addresses. net.DefaultResolver is the
// production implementation; tests substitute a pinned table.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// SystemResolver delegates to the standard library.
type SystemResolver struct{}

func (SystemResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// EgressPolicy is the per-connector outbound contract, derived from the
// manifest plus the integration's explicit grants.
type EgressPolicy struct {
	// AllowedHosts is the pinned set of DNS names the connector may reach,
	// normally exactly one: the manifest's base URL host. Sibling subdomains
	// are NOT included unless listed.
	AllowedHosts []string
	// AllowHTTP permits plain http for development connectors only. Production
	// manifests must use https; validation warns otherwise.
	AllowHTTP bool
	// Resolver resolves hostnames for the private-address check.
	Resolver Resolver
}

// CheckTarget validates one request URL against the policy: scheme, pinned
// host, and resolved-address class. It returns the parsed URL for the caller
// to use, so parsing happens exactly once.
func (p EgressPolicy) CheckTarget(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("egress: invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && p.AllowHTTP) {
		return nil, fmt.Errorf("egress: scheme %q refused (https required)", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("egress: userinfo in URL refused")
	}
	host := strings.ToLower(u.Hostname())
	allowed := false
	for _, h := range p.AllowedHosts {
		if host == strings.ToLower(h) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("egress: host %q is not in this connector's pinned set", host)
	}
	if err := p.checkResolvedAddresses(ctx, host); err != nil {
		return nil, err
	}
	return u, nil
}

// checkResolvedAddresses refuses non-global-unicast addresses AFTER resolution.
// Checking the string form is insufficient: DNS can point a pinned name at a
// private address, and a redirect can do the same on a later hop.
func (p EgressPolicy) checkResolvedAddresses(ctx context.Context, host string) error {
	resolver := p.Resolver
	if resolver == nil {
		resolver = SystemResolver{}
	}
	ips, err := resolver.LookupIP(ctx, host)
	if err != nil {
		return fmt.Errorf("egress: cannot resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("egress: %q resolves to no addresses", host)
	}
	for _, ip := range ips {
		if !isPublicAddress(ip) {
			return fmt.Errorf("egress: %q resolves to non-public address %s: refused", host, ip)
		}
	}
	return nil
}

var nonPublicNets = []*net.IPNet{
	// Carrier-Grade NAT (RFC 6598)
	mustParseCIDR("100.64.0.0/10"),
	// Documentation (RFC 5737)
	mustParseCIDR("192.0.2.0/24"),
	mustParseCIDR("198.51.100.0/24"),
	mustParseCIDR("203.0.113.0/24"),
	// Benchmarking (RFC 2544)
	mustParseCIDR("198.18.0.0/15"),
	// IPv6 Documentation (RFC 3849)
	mustParseCIDR("2001:db8::/32"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return ipnet
}

// isPublicAddress reports whether ip is safe for a credential-bearing request.
// Everything that is not global unicast — loopback, private, link-local,
// multicast, unspecified, and the IPv4/IPv6 special ranges — is refused.
func isPublicAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Explicitly block CGNAT, documentation, and benchmarking ranges
	for _, subnet := range nonPublicNets {
		if subnet.Contains(ip) {
			return false
		}
	}
	if ip.IsInterfaceLocalMulticast() {
		return false
	}
	return ip.IsGlobalUnicast()
}

// CheckRedirect validates one redirect hop: same scheme-or-https, still inside
// the pinned host set, re-resolved and re-checked. The caller enforces the hop
// count; this enforces the destination.
func (p EgressPolicy) CheckRedirect(ctx context.Context, location string) (*url.URL, error) {
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("egress: empty redirect location")
	}
	return p.CheckTarget(ctx, location)
}

// RefusedRequestHeaders are stripped from any agent-supplied header set before
// the executor builds the upstream request. Credential injection happens after
// this, exactly once, from the vault — never from caller input.
var RefusedRequestHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Proxy-Authenticate",
	"Cookie",
	"Set-Cookie",
}

// SanitizeHeaders removes refused headers (case-insensitive) from a caller
// supplied map and returns the cleaned copy. The input is never mutated.
func SanitizeHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	refused := make(map[string]bool, len(RefusedRequestHeaders))
	for _, h := range RefusedRequestHeaders {
		refused[strings.ToLower(h)] = true
	}
	for k, vs := range in {
		if refused[strings.ToLower(k)] {
			continue
		}
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// PolicyForManifest derives the default egress policy from a manifest: the
// base URL's host, https only.
func PolicyForManifest(m *ConnectorManifest) (EgressPolicy, error) {
	if m.Reach.OpenAPI == nil {
		return EgressPolicy{}, fmt.Errorf("egress: manifest %q has no openapi reach to pin", m.ID)
	}
	u, err := url.ParseRequestURI(m.Reach.OpenAPI.BaseURL)
	if err != nil {
		return EgressPolicy{}, fmt.Errorf("egress: invalid baseUrl: %w", err)
	}
	return EgressPolicy{AllowedHosts: []string{u.Hostname()}}, nil
}
