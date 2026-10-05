package mcpclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestModern2026_StreamableHTTP(t *testing.T) {
	var receivedMethodHeader string
	var receivedNameHeader string
	var receivedMeta RequestMeta

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethodHeader = r.Header.Get("Mcp-Method")
		receivedNameHeader = r.Header.Get("Mcp-Name")

		var rpcReq JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&rpcReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if rpcReq.Meta != nil {
			receivedMeta = *rpcReq.Meta
		}

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "tools/list":
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result: json.RawMessage(`{
					"tools": [
						{
							"name": "query",
							"description": "Execute SQL query",
							"inputSchema": {"type":"object","properties":{"sql":{"type":"string"}},"required":["sql"]}
						}
					]
				}`),
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "tools/call":
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result: json.RawMessage(`{
					"content": [{"type":"text","text":"result: 42"}]
				}`),
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Error:   &JSONRPCError{Code: -32601, Message: "Method not found"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	client, err := ConnectStreamableHTTP(ctx, server.URL, map[string]string{"Authorization": "Bearer token123"}, server.Client())
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP failed: %v", err)
	}
	defer client.CloseSession()

	if client.ProtocolVersion() != ProtocolModern2026 {
		t.Errorf("expected modern 2026-07-28 version, got %s", client.ProtocolVersion())
	}

	// 1. List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "query" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	if receivedMethodHeader != "tools/list" {
		t.Errorf("expected Mcp-Method header 'tools/list', got %q", receivedMethodHeader)
	}
	if receivedMeta.ProtocolVersion != string(ProtocolModern2026) {
		t.Errorf("expected modern protocol version in meta, got %q", receivedMeta.ProtocolVersion)
	}
	if receivedMeta.Capabilities.Roots != nil || receivedMeta.Capabilities.Sampling != nil {
		t.Errorf("expected clamped capabilities, got roots=%+v sampling=%+v", receivedMeta.Capabilities.Roots, receivedMeta.Capabilities.Sampling)
	}

	// 2. Call tool
	res, err := client.CallTool(ctx, "query", json.RawMessage(`{"sql":"SELECT 42"}`))
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "42") {
		t.Fatalf("unexpected tool result: %+v", res)
	}
	if receivedMethodHeader != "tools/call" || receivedNameHeader != "query" {
		t.Errorf("header mirroring mismatch: method=%q, name=%q", receivedMethodHeader, receivedNameHeader)
	}
}

func TestLegacy2025_Handshake(t *testing.T) {
	handshakeDone := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpcReq JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&rpcReq)

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{}}`),
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "notifications/initialized":
			handshakeDone = true
			w.WriteHeader(http.StatusOK)

		case "tools/list":
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result: json.RawMessage(`{
					"tools": [{"name":"ping","inputSchema":{"type":"object"}}]
				}`),
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	client, err := ConnectLegacy(ctx, server.URL, nil, server.Client())
	if err != nil {
		t.Fatalf("ConnectLegacy failed: %v", err)
	}
	defer client.CloseSession()

	if !handshakeDone {
		t.Errorf("legacy handshake notifications/initialized was not received")
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestStdioClient_InPipe(t *testing.T) {
	rClient, wServer := io.Pipe()
	rServer, wClient := io.Pipe()

	// Server goroutine
	go func() {
		dec := json.NewDecoder(rClient)
		enc := json.NewEncoder(wClient)

		for {
			var req JSONRPCRequest
			if err := dec.Decode(&req); err != nil {
				return
			}
			switch req.Method {
			case "initialize":
				_ = enc.Encode(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  json.RawMessage(`{"protocolVersion":"2026-07-28"}`),
				})
			case "tools/list":
				_ = enc.Encode(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  json.RawMessage(`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`),
				})
			}
		}
	}()

	ctx := context.Background()
	client, err := ConnectStdio(ctx, rServer, wServer)
	if err != nil {
		t.Fatalf("ConnectStdio failed: %v", err)
	}
	defer client.CloseSession()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestProbeAndDriftDetection(t *testing.T) {
	schema1 := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	schema2 := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"delete_all":{"type":"boolean"}}}`)

	fp1, err := FingerprintSchema(schema1)
	if err != nil {
		t.Fatalf("FingerprintSchema failed: %v", err)
	}
	fp2, err := FingerprintSchema(schema2)
	if err != nil {
		t.Fatalf("FingerprintSchema failed: %v", err)
	}
	if fp1 == fp2 {
		t.Fatalf("fingerprints must differ for distinct schemas")
	}

	grant := &domain.CapabilityGrant{
		GrantID:             "grant_123",
		CapabilityID:        "cap_read_file",
		SchemaFingerprint:   fp1,
		CASTreeDigest:       "tree_hash_abc",
		EndpointOrigin:      "https://api.example.com",
		ServerVersionDigest: "v1.0.0",
		Status:              "active",
	}

	capClean := DiscoveredCapability{
		NativeName:        "read_file",
		InputSchema:       schema1,
		SchemaFingerprint: fp1,
	}

	// 1. Clean match (no drift)
	repClean := DetectDrift(grant, capClean, "tree_hash_abc", "https://api.example.com", "v1.0.0")
	if repClean.HasDrift != DriftNone {
		t.Errorf("expected no drift, got %s", repClean.HasDrift)
	}

	// 2. Schema drift
	capDrifted := DiscoveredCapability{
		NativeName:        "read_file",
		InputSchema:       schema2,
		SchemaFingerprint: fp2,
	}
	repSchemaDrift := DetectDrift(grant, capDrifted, "tree_hash_abc", "https://api.example.com", "v1.0.0")
	if repSchemaDrift.HasDrift != DriftSchema || repSchemaDrift.Error.Code != domain.CodeProviderSchemaDrift {
		t.Errorf("expected schema drift with code %s, got %+v", domain.CodeProviderSchemaDrift, repSchemaDrift)
	}

	// 3. Local code drift
	repCodeDrift := DetectDrift(grant, capClean, "tree_hash_MODIFIED", "https://api.example.com", "v1.0.0")
	if repCodeDrift.HasDrift != DriftCode || repCodeDrift.Error.Code != domain.CodeProviderCodeDrift {
		t.Errorf("expected code drift with code %s, got %+v", domain.CodeProviderCodeDrift, repCodeDrift)
	}

	// 4. Remote endpoint drift
	repEndpointDrift := DetectDrift(grant, capClean, "tree_hash_abc", "https://malicious.evil.com", "v1.0.0")
	if repEndpointDrift.HasDrift != DriftEndpoint || repEndpointDrift.Error.Code != domain.CodeProviderEndpointDrift {
		t.Errorf("expected endpoint drift with code %s, got %+v", domain.CodeProviderEndpointDrift, repEndpointDrift)
	}
}
