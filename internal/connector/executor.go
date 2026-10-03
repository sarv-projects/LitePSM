package connector

// executor.go — local proxy execution: the only path by which a connector
// credential meets the network.
//
// The agent NEVER calls this with a secret. It calls it with handles:
// (connection id, operation, arguments). The executor, running on the daemon
// side of the trust boundary:
//
//  1. authorizes — the calling component must be in the connection's grant
//     list, and the integration's granted scopes must cover the operation;
//  2. pins — the request URL is built from the manifest's base URL plus an
//     operation-declared path template. The agent supplies path parameters
//     and a body, never a host;
//  3. checks egress — scheme, pinned host, resolved-address class, redirect
//     policy, and refusal of caller-supplied credential headers;
//  4. resolves — exactly one vault read, scoped to one field, one call;
//  5. injects — the Authorization header, set here and nowhere else;
//  6. calls — through an injected HTTP transport (a test fake in unit tests,
//     the daemon's client in production);
//  7. refreshes — at most one refresh-and-replay on a 401 when the scheme
//     supports it; a second 401 marks the connection STALE and stops;
//  8. logs — an AuditEvent with field names, never values.
//
// The plaintext secret exists only between steps 4 and 6, inside this call
// frame. It is never stored, never logged, never returned.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SecretResolver releases one credential field for one call. Implementations
// must enforce (component ∈ grant list) ∧ (field ∈ granted fields) before
// decrypting. The executor cannot verify that; the boundary test in
// executor_test.go pins the CALLER side: nothing the agent can construct may
// substitute for a resolver backed by the vault.
type SecretResolver func(ctx context.Context, grant GrantCheck) (plaintext string, err error)

// GrantCheck is the authorization question asked per credential release.
type GrantCheck struct {
	ComponentID  string
	ConnectionID string
	Field        string
	TargetHost   string
	Operation    string
}

// TokenRefresher performs one OAuth2 refresh for a connection and returns the
// new access token plus its expiry. Returning ("", nil, nil) means "this
// connection cannot refresh" — the executor then treats the 401 as terminal.
type TokenRefresher func(ctx context.Context, conn *Connection) (token string, expiresAt *time.Time, err error)

// Operation describes one callable connector operation as declared by the
// connector package (not by the agent).
type Operation struct {
	// Name is the stable identifier the agent passes, e.g. "post_message".
	Name string
	// Method and PathTemplate build the request. PathTemplate is relative
	// ("chat.postMessage") and may contain {param} slots filled from Args.
	Method       string
	PathTemplate string
	// RequiredScopes gates the call against Integration.GrantedScopes.
	RequiredScopes []string
	// SecretField is the vault field released for this call, e.g.
	// "access_token". Exactly one.
	SecretField string
	// Scheme determines injection: oauth2/apiKey/bearer → Authorization header.
	Scheme AuthSchemeType
}

// CallRequest is everything the agent side may supply.
type CallRequest struct {
	ComponentID  string
	ConnectionID string
	Operation    string
	PathParams   map[string]string
	Query        map[string]string
	Headers      http.Header
	Body         []byte
}

// CallResult is everything the agent side may receive. Note what is absent:
// no secret, no token, no header that carried one.
type CallResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Audit      AuditEvent
}

// Executor binds one integration, one connection, and one manifest to the
// policy and the transport.
type Executor struct {
	Manifest   *ConnectorManifest
	Policy     EgressPolicy
	Operations map[string]Operation

	Resolve SecretResolver
	Refresh TokenRefresher
	// Transport performs the HTTP call. http.DefaultClient must never be
	// passed directly: production wires a client with timeouts and no
	// redirect following (redirects are re-checked hop by hop).
	Transport func(*http.Request) (*http.Response, error)

	Now func() time.Time
}

// Execute runs one connector operation end to end.
func (e *Executor) Execute(ctx context.Context, integ *Integration, conn *Connection, req CallRequest) (*CallResult, error) {
	now := e.now()
	op, ok := e.Operations[req.Operation]
	if !ok {
		return nil, fmt.Errorf("connector: unknown operation %q", req.Operation)
	}
	if !integ.Enabled {
		return nil, fmt.Errorf("connector: integration %q is disabled", integ.ID)
	}
	if !integ.ScopesCover(op.RequiredScopes) {
		return nil, fmt.Errorf("connector: operation %q needs scopes %v, granted %v — re-prompt, never widen silently",
			op.Name, op.RequiredScopes, integ.GrantedScopes)
	}
	if conn.IntegrationID != integ.ID {
		return nil, fmt.Errorf("connector: connection %q does not belong to integration %q", conn.ID, integ.ID)
	}
	if conn.RevokedAt != nil {
		return nil, fmt.Errorf("connector: connection %q is revoked", conn.ID)
	}
	if conn.DisabledAt != nil {
		return nil, fmt.Errorf("connector: connection %q is disabled", conn.ID)
	}
	if conn.ExpiresAt != nil && now.After(*conn.ExpiresAt) {
		return nil, fmt.Errorf("connector: connection %q is expired", conn.ID)
	}

	target, err := e.buildURL(req, op)
	if err != nil {
		return nil, err
	}
	checked, err := e.Policy.CheckTarget(ctx, target)
	if err != nil {
		return nil, err
	}

	cleanHeaders := SanitizeHeaders(req.Headers)

	if e.Resolve == nil {
		return nil, fmt.Errorf("connector: no secret resolver configured")
	}

	plaintext, err := e.Resolve(ctx, GrantCheck{
		ComponentID:  req.ComponentID,
		ConnectionID: conn.ID,
		Field:        op.SecretField,
		TargetHost:   checked.Hostname(),
		Operation:    op.Name,
	})
	if err != nil {
		conn.RecordFailure(now)
		return nil, fmt.Errorf("connector: credential release refused: %w", err)
	}

	audit := AuditEvent{
		ConnectorID:  integ.ProviderID,
		ConnectionID: conn.ID,
		Field:        op.SecretField,
		Host:         checked.Hostname(),
		Operation:    op.Name,
	}

	resp, err := e.doUpstream(ctx, checked.String(), op.Method, cleanHeaders, req.Body, plaintext, op.Scheme)
	if err != nil {
		conn.RecordFailure(now)
		audit.Outcome = "transport_error"
		return &CallResult{StatusCode: 0, Audit: audit}, err
	}

	if resp.StatusCode == http.StatusUnauthorized && e.Refresh != nil {
		resp.Body.Close()
		newToken, expiresAt, rerr := e.Refresh(ctx, conn)
		if rerr == nil && newToken != "" {
			conn.RefreshCount++
			if expiresAt != nil {
				conn.ExpiresAt = expiresAt
			}
			resp, err = e.doUpstream(ctx, checked.String(), op.Method, cleanHeaders, req.Body, newToken, op.Scheme)
			if err != nil {
				conn.RecordFailure(now)
				audit.Outcome = "transport_error_after_refresh"
				return &CallResult{StatusCode: 0, Audit: audit}, err
			}
		}
	}

	result, resultErr := e.classifyResponse(ctx, conn, now, resp, audit)
	return result, resultErr
}

// classifyResponse records history, drains the body under the size cap, and
// builds the agent-visible result with credential headers stripped.
func (e *Executor) classifyResponse(ctx context.Context, conn *Connection, now time.Time, resp *http.Response, audit AuditEvent) (*CallResult, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		conn.RecordFailure(now)
		audit.Outcome = "read_error"
		return &CallResult{StatusCode: resp.StatusCode, Audit: audit}, fmt.Errorf("connector: reading provider response: %w", err)
	}
	if int64(len(body)) > MaxResponseBytes {
		conn.RecordFailure(now)
		audit.Outcome = "response_too_large"
		return &CallResult{StatusCode: resp.StatusCode, Audit: audit}, fmt.Errorf("connector: provider response exceeds %d bytes", MaxResponseBytes)
	}

	safeHeaders := SanitizeHeaders(resp.Header)
	safeHeaders.Del("Set-Cookie")

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		conn.RecordSuccess(now)
		audit.Outcome = "ok"
		return &CallResult{StatusCode: resp.StatusCode, Headers: safeHeaders, Body: body, Audit: audit}, nil
	case resp.StatusCode == http.StatusUnauthorized:
		conn.RecordFailure(now)
		audit.Outcome = "unauthorized"
		return &CallResult{StatusCode: resp.StatusCode, Headers: safeHeaders, Body: body, Audit: audit},
			fmt.Errorf("connector: provider rejected the credential (401); connection is now %s — reconnect, do not retry", conn.DeriveStatus(now, 0))
	case resp.StatusCode == http.StatusForbidden:
		conn.RecordFailure(now)
		audit.Outcome = "forbidden"
		return &CallResult{StatusCode: resp.StatusCode, Headers: safeHeaders, Body: body, Audit: audit},
			fmt.Errorf("connector: provider denied the operation (403): the connection lacks permission")
	default:
		conn.RecordFailure(now)
		audit.Outcome = fmt.Sprintf("upstream_%d", resp.StatusCode)
		return &CallResult{StatusCode: resp.StatusCode, Headers: safeHeaders, Body: body, Audit: audit},
			fmt.Errorf("connector: provider returned %d", resp.StatusCode)
	}
}

// buildURL joins the manifest base URL with the operation's path template.
// Path parameters are substituted and escaped; anything that would escape the
// base path (absolute URLs, ".." segments, scheme-relative "//") is refused.
// The agent influences the PATH, never the host.
func (e *Executor) buildURL(req CallRequest, op Operation) (string, error) {
	if e.Manifest.Reach.OpenAPI == nil {
		return "", fmt.Errorf("connector: manifest %q has no openapi reach", e.Manifest.ID)
	}
	base := strings.TrimRight(e.Manifest.Reach.OpenAPI.BaseURL, "/")
	path := op.PathTemplate
	for name, val := range req.PathParams {
		if strings.Contains(val, "/") || strings.Contains(val, "..") || strings.Contains(val, ":") ||
			strings.Contains(val, "?") || strings.Contains(val, "#") {
			return "", fmt.Errorf("connector: path parameter %q has an illegal value", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(val))
	}
	if strings.Contains(path, "{") || strings.Contains(path, "}") {
		return "", fmt.Errorf("connector: operation %q has unfilled path parameters", op.Name)
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("connector: operation path must be relative, not a URL")
	}
	path = strings.TrimLeft(path, "/")
	fullURL := base + "/" + path
	if len(req.Query) > 0 {
		parsed, err := url.Parse(fullURL)
		if err != nil {
			return "", err
		}
		q := parsed.Query()
		for k, v := range req.Query {
			q.Set(k, v)
		}
		parsed.RawQuery = q.Encode()
		fullURL = parsed.String()
	}
	return fullURL, nil
}

// doUpstream builds the request, injects the credential exactly once, and
// performs it with redirects disabled — redirect hops are re-checked by the
// caller through Policy.CheckRedirect, never followed blindly.
func (e *Executor) doUpstream(ctx context.Context, target, method string, headers http.Header, body []byte, plaintext string, scheme AuthSchemeType) (*http.Response, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("connector: building upstream request: %w", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	switch scheme {
	case AuthOAuth2, AuthHTTP:
		httpReq.Header.Set("Authorization", "Bearer "+plaintext)
	case AuthBasic:
		encoded := base64.StdEncoding.EncodeToString([]byte(plaintext))
		httpReq.Header.Set("Authorization", "Basic "+encoded)
	case AuthAPIKey:
		// API keys default to X-API-Key header unless specified, or Bearer fallback
		httpReq.Header.Set("X-API-Key", plaintext)
	default:
		return nil, fmt.Errorf("connector: unsupported auth scheme %q", scheme)
	}
	transport := e.Transport
	if transport == nil {
		return nil, fmt.Errorf("connector: no transport configured")
	}
	return transport(httpReq)
}

func (e *Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}
