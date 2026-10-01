package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// RegistryURL is the canonical ACP registry index endpoint.
const RegistryURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// DefaultMaxRegistryBytes bounds the registry download (it is currently well
// under 1 MiB; this prevents a hostile endpoint from exhausting memory).
const DefaultMaxRegistryBytes = 8 << 20 // 8 MiB

// LoadRegistryFile reads and parses a registry document from disk.
func LoadRegistryFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry file %s: %w", path, err)
	}
	return ParseRegistry(data)
}

// FetchRegistry downloads and parses a registry document over HTTPS.
//
// Only https:// URLs are accepted (http is permitted for loopback so tests and
// local mirrors can run). The response body is bounded by maxBytes.
func FetchRegistry(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (*Registry, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRegistryBytes
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid registry url %q: %w", rawURL, err)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, fmt.Errorf("registry url must use https (got %q)", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build registry request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry request returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read registry body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("registry body exceeds %d bytes", maxBytes)
	}

	return ParseRegistry(body)
}

// FindAgent returns the registry agent with the given id.
func (r *Registry) FindAgent(id string) (*Agent, bool) {
	for i := range r.Agents {
		if strings.EqualFold(r.Agents[i].ID, id) {
			return &r.Agents[i], true
		}
	}
	return nil, false
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
