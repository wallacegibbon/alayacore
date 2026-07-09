// Package platform provides OS-level utilities for adapter implementations
// (e.g., opening a URL in the default browser, starting an OAuth callback
// server on localhost).
package platform

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// CallbackResult holds the result of the OAuth callback HTTP request.
type CallbackResult struct {
	Code string
	Err  error
}

// StartCallbackServer starts a local HTTP server to receive the OAuth
// authorization code callback. It validates the state parameter to prevent
// CSRF attacks.
//
// listenAddr is the TCP address to bind to (e.g., "127.0.0.1:0" for
// loopback with random port, "0.0.0.0:0" for all interfaces).
//
// Returns:
//   - resultCh: channel that receives the callback result (code or error)
//   - redirectURI: the full redirect URI the server is listening on
//   - cleanup: function to stop the server (must be called when done)
func StartCallbackServer(listenAddr, state string) (<-chan CallbackResult, string, func()) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: %w", err)}
		close(ch)
		return ch, "", func() {}
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: unexpected addr type %T", listener.Addr())}
		close(ch)
		return ch, "", func() {}
	}
	port := addr.Port

	// Use the listen host for the redirect URI, but fall back to
	// loopback if bound to all interfaces (0.0.0.0 or empty host).
	redirectHost := addr.IP.String()
	if redirectHost == "" || addr.IP.IsUnspecified() {
		redirectHost = "127.0.0.1"
	}
	redirectURI := fmt.Sprintf("http://%s:%d/callback", redirectHost, port)

	resultCh := make(chan CallbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		returnedState := r.URL.Query().Get("state")
		errStr := r.URL.Query().Get("error")
		errDesc := r.URL.Query().Get("error_description")

		if errStr != "" {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("authorization error: %s: %s", errStr, errDesc)}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		if returnedState != state {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("state mismatch: got %q, expected %q", returnedState, state)}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		if code == "" {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("no authorization code in callback")}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		select {
		case resultCh <- CallbackResult{Code: code}:
		default:
		}
		_, _ = w.Write([]byte("Authorization successful! You can close this window."))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go server.Serve(listener) //nolint:errcheck // server is closed via cleanup

	return resultCh, redirectURI, func() { server.Close() }
}
