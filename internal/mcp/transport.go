package mcp

import (
	"context"
	"encoding/json"
	"time"
)

// SSE endpoint discovery timeout.
var sseEndpointTimeout = 30 * time.Second

// Transport defines the interface for MCP communication channels.
// MCP uses JSON-RPC 2.0 over stdio, SSE, or other transports.
type Transport interface {
	// Send marshals and sends a JSON-RPC notification (no response expected).
	Send(req jsonrpcRequest) error

	// SendReceive sends a JSON-RPC request and waits for the matching
	// response, matched by request ID. Context cancellation unregisters
	// the pending request without affecting the transport — the response
	// is simply discarded when it arrives.
	SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error)

	// Close shuts down the transport.
	Close() error

	// Done returns a channel that's closed when the transport has
	// encountered a fatal error or been closed.
	Done() <-chan struct{}
}

// ============================================================================
// Shared Helpers
// ============================================================================

// mapToEnvSlice converts a map[string]string to "KEY=VALUE" strings.
func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}

// formatJSON pretty-prints a JSON byte slice for debug logging.
// Falls back to the raw string on error.
func formatJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}
