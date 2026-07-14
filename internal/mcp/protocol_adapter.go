package mcp

import (
	"context"
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
}
