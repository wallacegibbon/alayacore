package mcp

import (
	"context"
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
	return c.doDiscover(ctx, a.ProtocolVersion())
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

// EnrichRequest adds the MCP-Protocol-Version header. No session header.
func (a *AdapterV20260728) EnrichRequest(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
}

// HandleResponseHeaders is a no-op — 2026-07-28 has no session concept.
func (a *AdapterV20260728) HandleResponseHeaders(_ *http.Response) {}

// OnTransportReady is a no-op — 2026-07-28 has no GET stream.
func (a *AdapterV20260728) OnTransportReady(_ context.Context, _ *HTTPTransport) error {
	return nil
}

// OnClose is a no-op — 2026-07-28 has no session to terminate.
func (a *AdapterV20260728) OnClose() {}
