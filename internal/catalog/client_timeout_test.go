package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClientUsesDefaultTimeout(t *testing.T) {
	c := NewClient("https://registry.invalid", t.TempDir(), nil)
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("default client timeout = %v, want 30s", c.httpClient.Timeout)
	}
	if c.httpClient.CheckRedirect == nil {
		t.Error("default client must carry the redirect policy")
	}
}

func TestNewClientWithTimeoutAppliesConfiguredBound(t *testing.T) {
	c := NewClientWithTimeout("https://registry.invalid", t.TempDir(), 7*time.Second, nil)
	if c.httpClient.Timeout != 7*time.Second {
		t.Errorf("client timeout = %v, want the configured 7s", c.httpClient.Timeout)
	}
	if c.httpClient.CheckRedirect == nil {
		t.Error("configured client must still carry the redirect policy")
	}
}

func TestNewClientWithTimeoutNonPositiveFallsBackToDefault(t *testing.T) {
	c := NewClientWithTimeout("https://registry.invalid", t.TempDir(), 0, nil)
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("timeout 0 must select the compiled default, got %v", c.httpClient.Timeout)
	}
}

// A caller-supplied transport owns its own timeout; the constructor only
// fills in the redirect policy it is missing.
func TestNewClientWithTimeoutCustomClientKeepsItsTimeout(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	c := NewClientWithTimeout("https://registry.invalid", t.TempDir(), time.Second, custom)
	if c.httpClient.Timeout != 5*time.Second {
		t.Errorf("custom client timeout was overwritten: %v", c.httpClient.Timeout)
	}
	if c.httpClient.CheckRedirect == nil {
		t.Error("redirect policy must be attached to a custom client that has none")
	}
}

// The configured bound has to actually stop a hung origin, not merely be
// recorded on the struct.
func TestConfiguredTimeoutBoundsFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClientWithTimeout(srv.URL, t.TempDir(), 100*time.Millisecond, nil)
	start := time.Now()
	_, err := c.FetchCurrent(context.Background())
	if err == nil {
		t.Fatal("expected the fetch to fail on the configured timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("fetch ran %v, well beyond the 100ms bound", elapsed)
	}
}
