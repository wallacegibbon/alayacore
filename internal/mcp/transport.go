package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Transport defines the interface for MCP communication channels.
// MCP uses JSON-RPC 2.0 over stdio, Streamable HTTP, or other transports.
type Transport interface {
	// Send marshals and sends a JSON-RPC notification (no response expected).
	// The context is used for cancellation and timeout — particularly for
	// transports where sending may block (e.g. SSE HTTP POST).
	Send(ctx context.Context, req jsonrpcRequest) error

	// SendReceive sends a JSON-RPC request and waits for the matching
	// response, matched by request ID. Context cancellation unregisters
	// the pending request without affecting the transport — the response
	// is simply discarded when it arrives.
	SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error)

	// SendNotification sends a JSON-RPC notification (fire-and-forget)
	// with the given method and optional parameters. It is a convenience
	// wrapper around Send that constructs the proper jsonrpcRequest with
	// an empty ID.
	SendNotification(ctx context.Context, method string, params any) error

	// Close shuts down the transport.
	Close() error

	// Done returns a channel that's closed when the transport has
	// encountered a fatal error or been closed.
	Done() <-chan struct{}
}

// ============================================================================
// Shared Helpers
// ============================================================================

// newNotification builds a JSON-RPC notification request (no ID) from a
// method and optional parameters. The returned request can be sent via
// Transport.Send().
func newNotification(method string, params any) (jsonrpcRequest, error) {
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return jsonrpcRequest{}, fmt.Errorf("marshal notification params: %w", err)
		}
		paramsData = data
	}

	return jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      requestID(""), // notification: no ID (omitempty omits empty string)
		Method:  method,
		Params:  paramsData,
	}, nil
}

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

// ============================================================================
// JSON-RPC Message Dispatch
// ============================================================================

// dispatchResponse sends a JSON-RPC response to the waiting caller by
// matching request ID. If no caller is waiting, the response is discarded.
// The pending map must be protected by mu.
func dispatchResponse(resp jsonrpcResponse, pending map[requestID]chan<- jsonrpcResponse, mu sync.Locker, debugWriter io.Writer, rawData []byte) {
	mu.Lock()
	ch, ok := pending[resp.ID]
	if ok {
		delete(pending, resp.ID)
	}
	mu.Unlock()

	if debugWriter != nil && rawData != nil {
		fmt.Fprintf(debugWriter, "<<< %s\n", formatJSON(rawData))
	}

	if ok {
		// Non-blocking send; channel has buffer 1 and receiver is
		// waiting (unless context was canceled after lookup).
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
	// No pending request for this ID — discard the response.
}

// ServerRequestHandler is a callback for handling server-to-client requests.
// These are JSON-RPC requests sent by the server to the client (e.g. ping,
// sampling/createMessage, roots/list). The handler should respond to the
// request using the transport's Send method.
type ServerRequestHandler func(id requestID, method string)

// NotificationHandler is a callback for handling server-to-client notifications.
// These are JSON-RPC notifications (no ID) sent by the server (e.g.
// notifications/tools/list_changed).
type NotificationHandler func(method string)

// parseAndDispatchJSONRPC parses a JSON-RPC message and dispatches
// responses to waiting callers. Returns nil on success, or an error
// if the data cannot be parsed.
//
// Per the MCP spec (2025-11-25), batch responses are no longer supported.
// Server-to-client requests are detected and forwarded to handleServerReq.
// Server-to-client notifications (no ID) are forwarded to handleNotification.
func parseAndDispatchJSONRPC(data []byte, pending map[requestID]chan<- jsonrpcResponse, mu sync.Locker, debugWriter io.Writer, handleServerReq ServerRequestHandler, handleNotification NotificationHandler) error {
	// Check for server-to-client requests first.
	// A JSON-RPC request has a "method" field and an "id" field.
	var reqFields struct {
		Method string    `json:"method"`
		ID     requestID `json:"id"`
	}
	if err := json.Unmarshal(data, &reqFields); err == nil && reqFields.Method != "" {
		if reqFields.ID != "" && handleServerReq != nil {
			handleServerReq(reqFields.ID, reqFields.Method)
		} else if handleNotification != nil {
			handleNotification(reqFields.Method)
		}
		// Notifications (no ID) are silently accepted even without handler.
		return nil
	}

	// Try single response.
	var resp jsonrpcResponse
	if err := json.Unmarshal(data, &resp); err == nil {
		dispatchResponse(resp, pending, mu, debugWriter, data)
		return nil
	}

	return fmt.Errorf("invalid JSON-RPC message: %s", string(data))
}
