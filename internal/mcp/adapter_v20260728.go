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
type AdapterV20260728 struct {
	// toolHeaderMappings caches x-mcp-header annotations from the last
	// ListTools response, keyed by tool name. Used in EnrichRequest to
	// inject Mcp-Param-{Name} headers.
	toolHeaderMappings map[string][]HeaderMapping
}

// NewAdapterV20260728 creates a new adapter for the 2026-07-28 protocol.
func NewAdapterV20260728() *AdapterV20260728 {
	return &AdapterV20260728{
		toolHeaderMappings: make(map[string][]HeaderMapping),
	}
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

// SetToolHeaderMappings caches tool header mappings for Mcp-Param-{Name}
// injection. Called by Client.ListTools after fetching tool definitions.
func (a *AdapterV20260728) SetToolHeaderMappings(tools []Tool) {
	a.toolHeaderMappings = make(map[string][]HeaderMapping, len(tools))
	for i := range tools {
		if len(tools[i].HeaderMappings) > 0 {
			m := make([]HeaderMapping, len(tools[i].HeaderMappings))
			copy(m, tools[i].HeaderMappings)
			a.toolHeaderMappings[tools[i].Name] = m
		}
	}
}

// EnrichRequest adds version-specific HTTP headers required by 2026-07-28:
//   - MCP-Protocol-Version (required on all requests)
//   - Mcp-Method (required on all POST requests)
//   - Mcp-Name (required for tools/call, resources/read, prompts/get)
//   - Mcp-Param-{Name} (required when tool has x-mcp-header annotations)
func (a *AdapterV20260728) EnrichRequest(req *http.Request, method string, params json.RawMessage) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())

	if method == "" {
		// GET request (2025-11-25 GET stream adapter compatibility).
		return
	}

	// Mcp-Method: mirror the JSON-RPC method.
	req.Header.Set("Mcp-Method", method)

	// Mcp-Name: mirror the resource/tool/prompt name for specific methods.
	if name := extractMcpName(method, params); name != "" {
		req.Header.Set("Mcp-Name", name)
	}

	// Mcp-Param-{Name}: mirror x-mcp-header annotated parameters.
	if method == "tools/call" && len(a.toolHeaderMappings) > 0 {
		a.injectParamHeaders(req, params)
	}
}

// injectParamHeaders reads the tool name and arguments from a tools/call
// params body and injects Mcp-Param-{Name} headers for any annotated
// parameters that have values in the arguments.
func (a *AdapterV20260728) injectParamHeaders(req *http.Request, params json.RawMessage) {
	var callReq struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &callReq); err != nil || callReq.Name == "" {
		return
	}

	mappings, ok := a.toolHeaderMappings[callReq.Name]
	if !ok || len(mappings) == 0 {
		return
	}

	headers := buildToolHeadersFromMappings(mappings, callReq.Arguments)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// extractMcpName extracts the resource or tool name from JSON-RPC params
// for the Mcp-Name header. Only applies to tools/call, resources/read,
// and prompts/get methods.
func extractMcpName(method string, params json.RawMessage) string {
	switch method {
	case "tools/call", "resources/read", "prompts/get":
	default:
		return ""
	}

	var fields struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if err := json.Unmarshal(params, &fields); err != nil {
		return ""
	}
	if fields.Name != "" {
		return fields.Name
	}
	return fields.URI
}

// buildToolHeadersFromMappings constructs Mcp-Param-{Name} headers from
// tool header mappings and call arguments.
func buildToolHeadersFromMappings(mappings []HeaderMapping, args json.RawMessage) map[string]string {
	if len(args) == 0 || len(mappings) == 0 {
		return nil
	}

	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return nil
	}

	headers := make(map[string]string, len(mappings))
	for _, m := range mappings {
		value, found := resolveNestedValue(argsMap, m.ParamPath)
		if !found || value == nil {
			continue
		}
		encoded, _ := encodeHeaderValue(value, m.ParamType)
		if encoded == "" {
			continue
		}
		headers["Mcp-Param-"+m.HeaderName] = encoded
	}

	if len(headers) == 0 {
		return nil
	}
	return headers
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
