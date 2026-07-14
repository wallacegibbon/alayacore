package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alayacore/alayacore/internal/version"
)

// AdapterV20260728 implements the MCP 2026-07-28 protocol.
// This version is stateless — no session, no GET stream.
// Version identity and capabilities are carried in _meta per request.
type AdapterV20260728 struct{}

// NewAdapterV20260728 creates a new adapter for the 2026-07-28 protocol.
func NewAdapterV20260728() *AdapterV20260728 {
	return &AdapterV20260728{}
}

// ProtocolVersion returns "2026-07-28".
func (a *AdapterV20260728) ProtocolVersion() string {
	return "2026-07-28"
}

// Handshake performs the 2026-07-28 server/discover handshake.
func (a *AdapterV20260728) Handshake(ctx context.Context, c *Client) (string, error) {
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

	preferred := a.ProtocolVersion()
	for _, v := range discover.SupportedVersions {
		if v == preferred {
			return v, nil
		}
	}
	return "", fmt.Errorf("unsupported protocol version %q: server supports %v",
		preferred, discover.SupportedVersions)
}

// BuildRequestMeta returns a structured RequestMetaObject for the
// 2026-07-28+ protocol. Every request must carry _meta with the
// protocol version, client identity, and capabilities.
func (a *AdapterV20260728) BuildRequestMeta(_ *Client) any {
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

// ValidateResult checks the resultType field required by 2026-07-28+.
// resultType must be "complete" or absent (for backward compat with
// 2025-11-25 servers). "input_required" is rejected since alayacore
// does not support multi-round-trip requests.
func (a *AdapterV20260728) ValidateResult(method string, result json.RawMessage) error {
	var rt struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(result, &rt); err != nil || rt.ResultType == "" {
		return nil // absent = "complete" (backward compat)
	}

	switch rt.ResultType {
	case "complete":
		return nil
	case "input_required":
		return fmt.Errorf("%s: server requested additional input (resultType=%q) but alayacore does not support multi-round-trip requests",
			method, rt.ResultType)
	default:
		return fmt.Errorf("%s: unrecognized resultType %q", method, rt.ResultType)
	}
}

// EnrichRequest adds the MCP-Protocol-Version header. No session header.
func (a *AdapterV20260728) EnrichRequest(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
}

// HandleResponseHeaders is a no-op — 2026-07-28 has no session concept.
func (a *AdapterV20260728) HandleResponseHeaders(_ *http.Response) {}

// CancelByNotification returns false — 2026-07-28 uses transport-level
// cancellation (closing SSE stream on HTTP) instead of JSON-RPC
// notification. On stdio, the caller falls back to sending the
// notification because there is no per-request stream to close.
func (a *AdapterV20260728) CancelByNotification() bool { return false }

// ServerRequestHandler is a no-op — 2026-07-28 has no server-to-client
// requests on SSE streams (MRTR replaces them).
func (a *AdapterV20260728) ServerRequestHandler(_ requestID, _ string) {}

// OnTransportReady is a no-op — 2026-07-28 has no GET stream.
func (a *AdapterV20260728) OnTransportReady(_ context.Context, _ *HTTPTransport) error {
	return nil
}

// OnClose is a no-op — 2026-07-28 has no session to terminate.
func (a *AdapterV20260728) OnClose() {}
