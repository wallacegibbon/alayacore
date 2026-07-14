package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alayacore/alayacore/internal/version"
)

// ProtocolAdapter abstracts protocol-version-specific behavior,
// allowing the MCP client to support multiple protocol versions
// (e.g. 2025-11-25 and 2026-07-28) through a common interface.
type ProtocolAdapter interface {
	// ProtocolVersion returns the protocol version string this adapter
	// implements (e.g. "2025-11-25" or "2026-07-28").
	ProtocolVersion() string

	// Handshake performs the initial handshake with the server.
	// Returns the negotiated protocol version on success.
	Handshake(ctx context.Context, c *Client) (string, error)

	// BuildRequestMeta constructs the _meta field for a JSON-RPC request.
	// Returns nil if no _meta should be injected (2025-11-25 behavior).
	// Returns a structured RequestMetaObject for 2026-07-28+.
	BuildRequestMeta(c *Client) any
}

// compile-time checks.
var (
	_ ProtocolAdapter = (*V2025_11_25Adapter)(nil)
	_ ProtocolAdapter = (*V2026_07_28Adapter)(nil)
)

// ============================================================================
// 2025-11-25 Adapter: initialize handshake, no structured _meta
// ============================================================================

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

// Handshake performs the 2025-11-25 initialize/initialized handshake.
func (a *V2025_11_25Adapter) Handshake(ctx context.Context, c *Client) (string, error) {
	return c.doInitialize(ctx)
}

// BuildRequestMeta returns nil — 2025-11-25 does not require structured _meta
// in every request.
func (a *V2025_11_25Adapter) BuildRequestMeta(_ *Client) any {
	return nil
}

// ============================================================================
// 2026-07-28 Adapter: discover handshake, structured _meta in every request
// ============================================================================

// V2026_07_28Adapter implements ProtocolAdapter for MCP spec 2026-07-28.
type V2026_07_28Adapter struct{}

// NewV2026_07_28Adapter creates a new adapter for the 2026-07-28 protocol.
func NewV2026_07_28Adapter() *V2026_07_28Adapter {
	return &V2026_07_28Adapter{}
}

// ProtocolVersion returns "2026-07-28".
func (a *V2026_07_28Adapter) ProtocolVersion() string {
	return "2026-07-28"
}

// Handshake performs the 2026-07-28 server/discover handshake.
// Does NOT fall back to initialize — if the server doesn't support
// discover, the caller (negotiateAndHandshake) will try a different adapter.
func (a *V2026_07_28Adapter) Handshake(ctx context.Context, c *Client) (string, error) {
	return c.doDiscover(ctx, a.ProtocolVersion())
}

// BuildRequestMeta returns a structured RequestMetaObject for the
// 2026-07-28+ protocol. Every request must carry _meta with the
// protocol version, client identity, and capabilities.
func (a *V2026_07_28Adapter) BuildRequestMeta(_ *Client) any {
	return &RequestMetaObject{
		ProtocolVersion: a.ProtocolVersion(),
		ClientInfo: &ImplementationInfo{
			Name:    "alayacore",
			Version: version.Version,
		},
		ClientCapabilities: &ClientCapabilities{
			Roots:       nil,
			Sampling:    nil,
			Elicitation: nil,
			Extensions:  nil,
		},
	}
}

// ============================================================================
// Shared helpers
// ============================================================================

// doDiscover sends server/discover and stores the server's capabilities.
// preferredVersion is the protocol version the adapter wants to use;
// the method picks the best mutually supported version.
func (c *Client) doDiscover(ctx context.Context, preferredVersion string) (string, error) {
	result, err := c.sendRequest(ctx, methodDiscover, nil)
	if err != nil {
		return "", err
	}

	var discover DiscoverResult
	if err := json.Unmarshal(result, &discover); err != nil {
		return "", fmt.Errorf("parse server/discover result: %w", err)
	}

	c.capabilities = discover.Capabilities
	c.serverInfo = discover.ServerInfo
	c.instructions = discover.Instructions

	// Pick the best mutually supported version.
	// Prefer the adapter's preferred version first.
	for _, v := range discover.SupportedVersions {
		if v == preferredVersion {
			return v, nil
		}
	}
	// Fall back to any version we might know about.
	for _, v := range discover.SupportedVersions {
		if v == protocolVersion { // 2025-11-25
			return v, nil
		}
	}
	// No compatible version found.
	return "", fmt.Errorf("no compatible protocol version: server supports %v",
		discover.SupportedVersions)
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

// injectMeta merges a _meta object into serialized JSON-RPC params.
// If params is nil, creates a new object with just _meta.
// If meta is nil, returns params unchanged.
func injectMeta(params json.RawMessage, meta any) (json.RawMessage, error) {
	if meta == nil {
		return params, nil
	}

	var target map[string]any
	if params != nil {
		if err := json.Unmarshal(params, &target); err != nil {
			return nil, fmt.Errorf("unmarshal params for _meta injection: %w", err)
		}
	} else {
		target = make(map[string]any)
	}

	target["_meta"] = meta
	return json.Marshal(target)
}
