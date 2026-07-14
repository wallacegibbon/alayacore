package mcp

import (
	"context"
	"io"
	"net/http"
	"sync"
)

// AdapterV20251125 implements the MCP 2025-11-25 protocol.
// It manages:
//   - initialize/initialized handshake
//   - MCP-Session-Id header lifecycle (receive from server, echo on requests)
//   - GET SSE stream for server-to-client messages
type AdapterV20251125 struct {
	// sessionID is the Mcp-Session-Id received from the server during
	// initialization. It is sent on all subsequent requests. Empty if
	// the server did not assign a session (which is allowed by spec).
	sessionID string

	// getSSECancel cancels the GET SSE stream context.
	getSSECancel context.CancelFunc
	// getSSEClose terminates the GET SSE stream by closing its response body.
	getSSEClose func()
	// getSSEClosed is closed when the GET SSE goroutine exits.
	getSSEClosed chan struct{}
	getSSEMu     sync.Mutex
}

// NewAdapterV20251125 creates a new adapter for the 2025-11-25 protocol.
func NewAdapterV20251125() *AdapterV20251125 {
	return &AdapterV20251125{
		getSSEClosed: make(chan struct{}),
	}
}

// ProtocolVersion returns "2025-11-25".
func (a *AdapterV20251125) ProtocolVersion() string {
	return "2025-11-25"
}

// Handshake performs the 2025-11-25 initialize/initialized handshake.
func (a *AdapterV20251125) Handshake(ctx context.Context, c *Client) (string, error) {
	return c.doInitialize(ctx)
}

// BuildRequestMeta returns nil — 2025-11-25 does not require structured _meta
// in every request.
func (a *AdapterV20251125) BuildRequestMeta(_ *Client) any {
	return nil
}

// EnrichRequest adds MCP-Protocol-Version and (if assigned) MCP-Session-Id
// headers to the outgoing HTTP request.
func (a *AdapterV20251125) EnrichRequest(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
	if a.sessionID != "" {
		req.Header.Set("MCP-Session-Id", a.sessionID)
	}
}

// HandleResponseHeaders extracts MCP-Session-Id from the response headers
// and stores it for subsequent requests. Per the 2025-11-25 spec, the
// server assigns a session ID in its InitializeResult response.
func (a *AdapterV20251125) HandleResponseHeaders(resp *http.Response) {
	if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
		a.sessionID = sid
	}
}

// OnTransportReady starts the GET SSE stream for server-to-client messages.
func (a *AdapterV20251125) OnTransportReady(ctx context.Context, transport *HTTPTransport) error {
	a.getSSEMu.Lock()
	defer a.getSSEMu.Unlock()

	// Reset closed channel if previous GET stream finished.
	select {
	case <-a.getSSEClosed:
		a.getSSEClosed = make(chan struct{})
	default:
	}

	// Check transport is still alive before starting a new stream.
	select {
	case <-transport.Done():
		return io.ErrClosedPipe
	default:
	}

	getCtx, cancel := context.WithCancel(ctx)
	a.getSSECancel = cancel

	closeFn, err := transport.StartGETStream(getCtx, transport.handleServerRequest)
	if err != nil {
		cancel()
		a.getSSECancel = nil
		return err
	}
	a.getSSEClose = closeFn

	return nil
}

// OnClose terminates the session and cleans up the GET stream.
func (a *AdapterV20251125) OnClose() {
	a.getSSEMu.Lock()
	defer a.getSSEMu.Unlock()

	if a.getSSECancel != nil {
		a.getSSECancel()
		a.getSSECancel = nil
	}
	if a.getSSEClose != nil {
		a.getSSEClose()
		a.getSSEClose = nil
	}
	close(a.getSSEClosed)
}
