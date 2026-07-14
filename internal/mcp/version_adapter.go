package mcp

import "context"

// ProtocolAdapter abstracts protocol-version-specific behavior,
// allowing the MCP client to support multiple protocol versions
// (e.g. 2025-11-25 and 2026-07-28) through a common interface.
//
// Currently only the 2025-11-25 adapter is implemented. When migrating
// to a newer protocol version, implement this interface and wire it
// into the Client via version negotiation.
type ProtocolAdapter interface {
	// ProtocolVersion returns the protocol version string this adapter
	// implements (e.g. "2025-11-25" or "2026-07-28").
	ProtocolVersion() string

	// Initialize performs the initial handshake with the server.
	// For 2025-11-25 this sends "initialize" and receives InitializeResult.
	// For 2026-07-28 this sends "server/discover" and receives DiscoverResult.
	Initialize(ctx context.Context, c *Client) error

	// BuildRequestMeta constructs the _meta field for a request.
	// In 2025-11-25 this returns nil or a simple map.
	// In 2026-07-28 this returns a structured RequestMetaObject with
	// protocol version, client info, and capabilities.
	BuildRequestMeta(c *Client) any
}

// compile-time check: ensure the concrete adapter implements the interface.
var _ ProtocolAdapter = (*V2025_11_25Adapter)(nil)

// V2025_11_25Adapter implements ProtocolAdapter for MCP spec 2025-11-25.
type V2025_11_25Adapter struct{}

// NewV2025_11_25Adapter creates a new adapter for the 2025-11-25 protocol.
func NewV2025_11_25Adapter() *V2025_11_25Adapter {
	return &V2025_11_25Adapter{}
}

// ProtocolVersion returns "2025-11-25".
func (a *V2025_11_25Adapter) ProtocolVersion() string {
	return protocolVersion
}

// Initialize performs the 2025-11-25 initialize/initialized handshake.
func (a *V2025_11_25Adapter) Initialize(ctx context.Context, c *Client) error {
	return c.doInitialize(ctx)
}

// BuildRequestMeta returns nil — the 2025-11-25 spec does not require
// structured _meta in every request.
func (a *V2025_11_25Adapter) BuildRequestMeta(c *Client) any {
	return nil
}
