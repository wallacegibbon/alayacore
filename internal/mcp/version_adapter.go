package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

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

	// Handshake performs the initial handshake with the server.
	// The adapter should try newer protocol handshakes first and fall
	// back to older ones if the server doesn't support them.
	// Returns the negotiated protocol version on success.
	Handshake(ctx context.Context, c *Client) (string, error)

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

// Handshake performs initialization with version negotiation.
// Strategy: try server/discover (2026-07-28) first; if the server
// responds with MethodNotFound, fall back to initialize (2025-11-25).
// If discover succeeds, the server supports a newer protocol — but
// this adapter only implements 2025-11-25, so we report the version
// mismatch and let the caller decide how to proceed.
func (a *V2025_11_25Adapter) Handshake(ctx context.Context, c *Client) (string, error) {
	// Step 1: Try server/discover (new protocol).
	supported, err := c.tryDiscover(ctx)
	if err == nil {
		// Discover succeeded — server supports a newer protocol.
		// Check if 2025-11-25 is in its supported list.
		for _, v := range supported {
			if v == a.ProtocolVersion() {
				// Server supports 2025-11-25 alongside newer versions.
				// Fall through to initialize handshake.
				return c.doInitialize(ctx)
			}
		}
		// Server doesn't support 2025-11-25 at all.
		return "", fmt.Errorf("server supports versions %v, but client only supports %q",
			supported, a.ProtocolVersion())
	}

	// Step 2: If discover returned MethodNotFound, try initialize.
	if isMethodNotFound(err) {
		return c.doInitialize(ctx)
	}

	// Some other error — propagate.
	return "", err
}

// BuildRequestMeta returns nil — the 2025-11-25 spec does not require
// structured _meta in every request.
func (a *V2025_11_25Adapter) BuildRequestMeta(_ *Client) any {
	return nil
}

// tryDiscover sends a server/discover request to check if the server
// supports the newer protocol. Returns the list of supported versions
// on success. Returns an error if the method is not found or the
// request fails.
//
// This is used by the adapter during handshake negotiation.
func (c *Client) tryDiscover(ctx context.Context) ([]string, error) {
	result, err := c.sendRequest(ctx, methodDiscover, nil)
	if err != nil {
		return nil, err
	}

	var discover DiscoverResult
	if err := json.Unmarshal(result, &discover); err != nil {
		return nil, fmt.Errorf("parse server/discover result: %w", err)
	}

	c.capabilities = discover.Capabilities
	c.serverInfo = discover.ServerInfo
	c.instructions = discover.Instructions

	return discover.SupportedVersions, nil
}

// isMethodNotFound checks if a JSON-RPC error indicates MethodNotFound.
func isMethodNotFound(err error) bool {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == MethodNotFound
	}
	return false
}

// MethodNotFound is the JSON-RPC error code for method not found.
// Used during handshake negotiation to detect older protocol servers.
const MethodNotFound = -32601
