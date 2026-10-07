package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

const (
	// DefaultLoopbackTimeout is 120 seconds per security architecture specification.
	DefaultLoopbackTimeout = 120 * time.Second
)

// LoopbackResult delivers the authorization code or error from the callback.
type LoopbackResult struct {
	Code  string
	Error error
}

// LoopbackListener manages an ephemeral HTTP server on 127.0.0.1 for OAuth 2.0 PKCE callbacks.
type LoopbackListener struct {
	listener   net.Listener
	server     *http.Server
	port       int
	stateToken string
	resultChan chan LoopbackResult
	closeOnce  sync.Once
}

// StartLoopbackListener binds exclusively to 127.0.0.1:0 and prepares the callback handler.
func StartLoopbackListener() (*LoopbackListener, error) {
	// Generate 32 bytes cryptographically secure random state token
	stateBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, stateBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random state token: %w", err)
	}
	stateToken := hex.EncodeToString(stateBytes)

	// Bind strictly to 127.0.0.1 with ephemeral port (never 0.0.0.0)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind loopback listener: %w", err)
	}

	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		return nil, fmt.Errorf("expected TCPAddr from listener")
	}

	ll := &LoopbackListener{
		listener:   l,
		port:       tcpAddr.Port,
		stateToken: stateToken,
		resultChan: make(chan LoopbackResult, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", ll.handleCallback)
	mux.HandleFunc("/cb", ll.handleCallback)

	ll.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = ll.server.Serve(l)
	}()

	return ll, nil
}

func (ll *LoopbackListener) Port() int {
	return ll.port
}

func (ll *LoopbackListener) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", ll.port)
}

func (ll *LoopbackListener) StateToken() string {
	return ll.stateToken
}

func (ll *LoopbackListener) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	receivedState := q.Get("state")
	receivedCode := q.Get("code")
	receivedErr := q.Get("error")
	errDesc := q.Get("error_description")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if receivedState != ll.stateToken {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, "<html><body><h2>Access Denied</h2><p>Invalid or expired state parameter. (CSRF protection)</p></body></html>")
		// No waiter signal here: a wrong state is a foreign probe/scan, not
		// the terminal OAuth result. Signalling would let one stray request
		// poison the pending flow (bad-then-good would return the probe's
		// error). The waiter keeps waiting for the real redirect.
		return
	}

	if receivedErr != "" {
		msg := receivedErr
		if errDesc != "" {
			msg = fmt.Sprintf("%s: %s", receivedErr, errDesc)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, "<html><body><h2>Authentication Failed</h2><p>%s</p></body></html>", html.EscapeString(msg))
		ll.sendResult(LoopbackResult{Error: domain.NewError(domain.CodeOAuthStateMismatch, msg, nil)})
		return
	}

	if receivedCode == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, "<html><body><h2>Authentication Error</h2><p>Missing authorization code.</p></body></html>")
		ll.sendResult(LoopbackResult{Error: domain.NewError(domain.CodeOAuthStateMismatch, "missing authorization code", nil)})
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>LiteSPM Authentication</title><style>body{font-family:sans-serif;text-align:center;padding:50px;background:#f9f9fb;color:#222;}h2{color:#10b981;}</style></head>
<body>
  <h2>Authentication Successful!</h2>
  <p>Your authorization has been securely recorded. You may close this window and return to your agent or terminal.</p>
</body>
</html>`)

	ll.sendResult(LoopbackResult{Code: receivedCode})
}

func (ll *LoopbackListener) sendResult(res LoopbackResult) {
	select {
	case ll.resultChan <- res:
	default:
	}
}

// WaitForCallback waits for the authorization code callback with timeout.
func (ll *LoopbackListener) WaitForCallback(ctx context.Context, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = DefaultLoopbackTimeout
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	defer ll.Close()

	select {
	case <-ctxTimeout.Done():
		return "", domain.NewError(domain.CodeOAuthCallbackTimeout, "OAuth authorization callback timed out", nil)
	case res := <-ll.resultChan:
		if res.Error != nil {
			return "", res.Error
		}
		return res.Code, nil
	}
}

// Close shuts down the loopback listener.
func (ll *LoopbackListener) Close() error {
	var err error
	ll.closeOnce.Do(func() {
		if ll.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = ll.server.Shutdown(ctx)
		}
		if ll.listener != nil {
			_ = ll.listener.Close()
		}
	})
	return err
}
