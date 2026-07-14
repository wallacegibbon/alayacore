package mcp

import (
	"context"
	"encoding/json"
	"net/http"
)

// Adapter is the common interface that all protocol version adapters
// must implement. These methods are used regardless of transport type.
type Adapter interface {
	// ProtocolVersion returns the protocol version string
	// (e.g. "2025-11-25" or "2026-07-28").
	ProtocolVersion() string

	// Handshake performs the initial handshake with the server.
	Handshake(ctx context.Context, c *Client) (string, error)

	// BuildRequestMeta constructs the _meta field for a JSON-RPC request.
	// Returns nil if no _meta should be injected.
	BuildRequestMeta(c *Client) any

	// ValidateResult checks a JSON-RPC result for protocol-version-specific
	// correctness. Returns nil if OK, error if the result is invalid or
	// requires capabilities the client does not have.
	ValidateResult(method string, result json.RawMessage) error

	// CancelByNotification returns true if this protocol version uses a
	// cancellation notification as the cancellation mechanism.
	// When false, transport-level cancellation is used instead
	// (e.g. closing the SSE response stream for HTTP 2026-07-28+).
	CancelByNotification() bool

	// OnClose is called when the client is shutting down.
	// The adapter can clean up version-specific resources.
	OnClose()
}

// HTTPAdapter extends Adapter with HTTP transport-specific hooks.
// These are only called for HTTP transport; stdio transport does not
// use HTTP headers or GET streams.
type HTTPAdapter interface {
	Adapter

	// EnrichRequest modifies the outgoing HTTP request before it is sent
	// (e.g. add MCP-Protocol-Version, MCP-Session-Id headers).
	EnrichRequest(req *http.Request)

	// HandleResponseHeaders processes HTTP response headers after each
	// response is received (e.g. extract MCP-Session-Id).
	HandleResponseHeaders(resp *http.Response)

	// OnTransportReady is called after the handshake completes and the
	// transport is ready. The adapter can start version-specific
	// resources (e.g. GET SSE stream for 2025-11-25).
	OnTransportReady(ctx context.Context, transport *HTTPTransport) error

	// ServerRequestHandler handles a JSON-RPC request from the server on
	// an SSE stream. 2025-11-25 responds to ping; 2026-07-28 has no
	// server-to-client requests and is a no-op.
	ServerRequestHandler(id requestID, method string)
}

// compile-time checks.
var (
	_ Adapter = (*AdapterV20251125)(nil)
	_ Adapter = (*AdapterV20260728)(nil)
)
