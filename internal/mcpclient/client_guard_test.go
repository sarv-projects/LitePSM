package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// requireEgressBlocked asserts that err is (or wraps) the SSRF guard's typed
// refusal, not a DNS, dial or protocol failure.
func requireEgressBlocked(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected the egress guard to refuse the request, got nil")
	}
	var lpsm *domain.LPSMError
	if !errors.As(err, &lpsm) {
		t.Fatalf("expected *domain.LPSMError, got %T: %v", err, err)
	}
	if lpsm.Code != "LPSM-EGRESS-BLOCKED" {
		t.Fatalf("expected LPSM-EGRESS-BLOCKED, got %s: %v", lpsm.Code, err)
	}
}

// A nil httpClient must fail closed: no scheme check was ever performed
// before Phase 0, so http:// (non-loopback) and metadata targets went
// straight to the network.
func TestNilClientRefusesUnsafeEndpoints(t *testing.T) {
	ctx := context.Background()
	for _, endpoint := range []string{
		"http://public.example/mcp",
		"http://169.254.169.254/mcp",
		"https://169.254.169.254/mcp",
	} {
		t.Run(endpoint, func(t *testing.T) {
			client, err := ConnectStreamableHTTP(ctx, endpoint, nil, nil)
			if err != nil {
				t.Fatalf("ConnectStreamableHTTP: %v", err)
			}
			_, err = client.ListTools(ctx)
			requireEgressBlocked(t, err)
		})
	}

	// The legacy client handshakes eagerly, so the refusal surfaces from
	// ConnectLegacy itself.
	_, err := ConnectLegacy(ctx, "http://public.example/mcp", nil, nil)
	requireEgressBlocked(t, err)
}

// A caller-supplied client is wrapped in the guard, not trusted — mirroring
// catalog.NewClientWithTimeout.
func TestCallerSuppliedClientIsWrappedByGuard(t *testing.T) {
	ctx := context.Background()
	client, err := ConnectStreamableHTTP(ctx, "http://public.example/mcp", nil, &http.Client{})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	_, err = client.ListTools(ctx)
	requireEgressBlocked(t, err)
}

// Under SameOriginRedirects a redirect off the configured origin is refused
// before the client follows it.
func TestNilClientRefusesOffOriginRedirect(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		http.Redirect(w, r, "https://off-origin.example/mcp", http.StatusFound)
	}))
	defer origin.Close()

	ctx := context.Background()
	client, err := ConnectStreamableHTTP(ctx, origin.URL, nil, nil)
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	_, err = client.ListTools(ctx)
	requireEgressBlocked(t, err)
	if got := originHits.Load(); got != 1 {
		t.Fatalf("origin received %d request(s), want exactly the first one", got)
	}
}

// The legitimate case: a hermetic httptest server over loopback http keeps
// working under the remote policy (AllowLoopbackHTTP), with a nil client.
func TestNilClientAllowsLoopbackHTTPTestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpcReq JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&rpcReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize", "tools/list":
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2026-07-28","tools":[{"name":"ping","inputSchema":{"type":"object"}}]}`),
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		default:
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Error:   &JSONRPCError{Code: -32601, Message: "Method not found"},
			})
		}
	}))
	defer server.Close()

	ctx := context.Background()

	modern, err := ConnectStreamableHTTP(ctx, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP over loopback http: %v", err)
	}
	tools, err := modern.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools over loopback http refused: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	legacy, err := ConnectLegacy(ctx, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("ConnectLegacy over loopback http refused: %v", err)
	}
	defer legacy.CloseSession()
}
