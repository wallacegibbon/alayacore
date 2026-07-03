package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// ============================================================================
// Streamable HTTP Transport
// ============================================================================
//
// StreamableHTTPTransport implements the MCP Streamable HTTP transport
// defined in specification 2025-11-25.
//
// Protocol overview:
//   - Server exposes a single MCP endpoint URL supporting both POST and GET
//   - POST: client sends JSON-RPC messages; server responds with either
//     a JSON response (Content-Type: application/json) or an SSE stream
//     (Content-Type: text/event-stream)
//   - GET: client opens an SSE stream for server-to-client messages
//   - Session management via Mcp-Session-Id header

// StreamableHTTPTransport communicates with an MCP server using the
// Streamable HTTP transport (spec 2025-11-25).
type StreamableHTTPTransport struct {
	endpointURL string
	sessionID   string // Mcp-Session-Id from server, if any
	httpClient  *http.Client

	// authProvider provides OAuth tokens for Authorization header injection.
	authProvider auth.TokenProvider

	// negotiatedVersion is the protocol version negotiated during
	// initialization. It is set after the initialize handshake and is
	// included as the MCP-Protocol-Version header on all subsequent
	// HTTP requests (required by spec 2025-11-25).
	negotiatedVersion string

	// SSE stream from a POST response that's still active.
	// Only one POST-response SSE stream can be active at a time.
	postSSEStream *sseReadCloser
	postSSEMu     sync.Mutex

	// Long-lived GET SSE stream for server-to-client messages.
	getSSEClosed chan struct{} // closed when GET SSE goroutine exits
	getSSEMu     sync.Mutex
	getSSECancel context.CancelFunc // cancels the GET SSE request

	pending   map[requestID]chan<- jsonrpcResponse
	pendingMu sync.Mutex

	closeOnce sync.Once
	done      chan struct{}
	readerWg  sync.WaitGroup

	debugWriter io.WriteCloser

	// Notification handler for server-to-client notifications.
	notificationHandler NotificationHandler
}

// sseReadCloser wraps an io.ReadCloser (HTTP response body) with the
// scanner and SSE parsing state needed to read SSE events from it.
type sseReadCloser struct {
	body     io.ReadCloser
	scanner  *bufio.Scanner
	closed   bool
	closedMu sync.Mutex
}

// newSSEReadCloser creates an sseReadCloser from an HTTP response body.
func newSSEReadCloser(body io.ReadCloser) *sseReadCloser {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseReadCloser{
		body:    body,
		scanner: scanner,
	}
}

// readEvent reads the next complete SSE event (a blank-line-delimited
// sequence of field lines). Returns the event type, data, and any error.
// If the stream is closed cleanly, returns io.EOF.
func (s *sseReadCloser) readEvent() (eventType, data string, err error) {
	s.closedMu.Lock()
	if s.closed {
		s.closedMu.Unlock()
		return "", "", io.EOF
	}
	s.closedMu.Unlock()

	var currentEvent string
	var currentData strings.Builder

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Empty line signals end of an SSE event.
		if line == "" {
			if currentEvent != "" || currentData.Len() > 0 {
				return currentEvent, currentData.String(), nil
			}
			// Consecutive blank lines produce empty events; skip.
			continue
		}

		// Parse SSE field.
		processSSELine(line, &currentEvent, &currentData)
	}

	// Scanner finished.
	if err := s.scanner.Err(); err != nil {
		return "", "", fmt.Errorf("SSE read: %w", err)
	}
	return "", "", io.EOF
}

// Close closes the underlying body.
func (s *sseReadCloser) Close() error {
	s.closedMu.Lock()
	s.closed = true
	s.closedMu.Unlock()
	return s.body.Close()
}

// NewStreamableHTTPTransport creates a new Streamable HTTP transport.
// It does NOT connect immediately; the first Send/SendReceive will POST
// to the endpoint.
func NewStreamableHTTPTransport(endpointURL string, enableDebug bool) *StreamableHTTPTransport {
	t := &StreamableHTTPTransport{
		endpointURL: endpointURL,
		httpClient: &http.Client{
			Timeout: 0, // No timeout; caller contexts control request lifetime.
		},
		pending:      make(map[requestID]chan<- jsonrpcResponse),
		done:         make(chan struct{}),
		getSSEClosed: make(chan struct{}),
	}

	if enableDebug {
		t.debugWriter = debug.NewDebugWriter("alayacore-debug-mcp")
		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "MCP Streamable HTTP debug log started for: %s\n", endpointURL)
		}
	}

	return t
}

// ============================================================================
// Transport Interface
// ============================================================================

// Send sends a JSON-RPC notification (no response expected) via HTTP POST.
// The server MUST respond with 202 Accepted and no body.
func (t *StreamableHTTPTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	resp, err := t.doPOST(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Drain and discard body.
	_, _ = io.Copy(io.Discard, resp.Body) // discard

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("POST notification: unexpected status %d (expected 202)", resp.StatusCode)
	}

	return nil
}

// SendReceive sends a JSON-RPC request and waits for the matching response.
// The server may respond with either:
//   - Content-Type: application/json (immediate JSON response)
//   - Content-Type: text/event-stream (SSE stream containing the response)
//
// If the server opens an SSE stream, this method reads events until the
// matching response is received, dispatching any intermediate server-to-client
// requests/notifications to the handler.
func (t *StreamableHTTPTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	resp, err := t.doPOST(ctx, req)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	contentType = strings.SplitN(contentType, ";", 2)[0] // strip params
	contentType = strings.TrimSpace(contentType)

	switch contentType {
	case "application/json":
		// Immediate JSON response.
		defer resp.Body.Close()
		return t.readJSONResponse(resp.Body)

	case "text/event-stream":
		// SSE stream — read events until we get the matching response.
		return t.readSSEResponse(ctx, resp, req.ID)

	case "text/plain":
		// Plain text response — likely an HTTP-level error message.
		defer resp.Body.Close()
		return t.readTextResponse(resp.Body)

	default:
		resp.Body.Close()
		return nil, fmt.Errorf("POST: unexpected Content-Type %q", contentType)
	}
}

// Close shuts down the transport, including any active SSE connections.
func (t *StreamableHTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		// Cancel GET SSE stream if active.
		t.getSSEMu.Lock()
		if t.getSSECancel != nil {
			t.getSSECancel()
		}
		t.getSSEMu.Unlock()

		// Close POST SSE stream if active.
		t.postSSEMu.Lock()
		if t.postSSEStream != nil {
			t.postSSEStream.Close()
		}
		t.postSSEMu.Unlock()

		// Wait for goroutines.
		t.readerWg.Wait()

		// Close debug log file if open.
		if t.debugWriter != nil {
			t.debugWriter.Close()
		}

		// Signal done.
		close(t.done)
	})
	return nil
}

// Done returns a channel that closes when the transport is shut down.
func (t *StreamableHTTPTransport) Done() <-chan struct{} {
	return t.done
}

// SetNotificationHandler registers a handler for server-to-client notifications.
func (t *StreamableHTTPTransport) SetNotificationHandler(h NotificationHandler) {
	t.notificationHandler = h
}

// SetProtocolVersion sets the protocol version negotiated during initialization.
// This version is included as the MCP-Protocol-Version header on all subsequent
// HTTP requests, as required by the MCP Streamable HTTP specification.
// If not set, the header is omitted.
func (t *StreamableHTTPTransport) SetProtocolVersion(version string) {
	t.negotiatedVersion = version
}

// SetAuthProvider sets the OAuth token provider for this transport.
// If set, an Authorization: Bearer header is injected into every request.
func (t *StreamableHTTPTransport) SetAuthProvider(ap auth.TokenProvider) {
	t.authProvider = ap
}

// DebugWriter returns the debug log writer, or nil if debug is not enabled.
func (t *StreamableHTTPTransport) DebugWriter() io.Writer {
	return t.debugWriter
}

// ============================================================================
// GET SSE Stream (Server-to-Client Messages)
// ============================================================================

// StartGETStream starts a long-lived GET SSE connection for receiving
// server-to-client messages (requests and notifications).
// This is optional per the spec; the client MAY do this.
//
// The handler is called for each server-to-client request or notification
// received on the stream. If the handler is nil, server requests are
// responded to with method-not-found errors and notifications are ignored.
//
// The stream runs until the transport is closed or the GET request fails.
func (t *StreamableHTTPTransport) StartGETStream(ctx context.Context, handler ServerRequestHandler) error {
	t.getSSEMu.Lock()
	defer t.getSSEMu.Unlock()

	// Only one GET stream at a time.
	select {
	case <-t.getSSEClosed:
		// Previous stream closed; reset the channel.
		t.getSSEClosed = make(chan struct{})
	default:
		select {
		case <-t.done:
			return io.ErrClosedPipe
		default:
		}
	}

	// Create a cancelable context for this stream.
	var getCtx context.Context
	getCtx, cancel := context.WithCancel(ctx)
	t.getSSECancel = cancel

	// Create request with the cancelable context.
	httpReq, err := http.NewRequestWithContext(getCtx, "GET", t.endpointURL, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("create GET request: %w", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	if t.negotiatedVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
	}
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		cancel()
		return fmt.Errorf("GET SSE: %w", err)
	}

	if resp.StatusCode == http.StatusMethodNotAllowed {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("GET SSE: server returned 405 Method Not Allowed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("GET SSE: unexpected status %d", resp.StatusCode)
	}

	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if contentType != "text/event-stream" {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("GET SSE: expected text/event-stream, got %q", contentType)
	}

	// Start reading in background.
	sr := newSSEReadCloser(resp.Body)
	t.readerWg.Add(1)
	go t.readSSEStream(sr, handler)

	return nil
}

// readSSEStream reads SSE events from a stream in a background goroutine.
// It ensures cleanup of the stream, reader goroutine tracking, and GET
// stream state (cancel func, closed channel) on exit.
func (t *StreamableHTTPTransport) readSSEStream(sr *sseReadCloser, handler ServerRequestHandler) {
	defer t.readerWg.Done()
	defer sr.Close()
	defer func() {
		t.getSSEMu.Lock()
		t.getSSECancel = nil
		t.getSSEMu.Unlock()
		close(t.getSSEClosed)
	}()

	t.readSSELoop(sr, handler, t.notificationHandler)
}

// readSSELoop reads SSE events from a stream and dispatches them.
// Used for both POST-response SSE streams and GET SSE streams.
func (t *StreamableHTTPTransport) readSSELoop(sr *sseReadCloser, handler ServerRequestHandler, notifHandler NotificationHandler) {
	for {
		eventType, data, err := sr.readEvent()
		if err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				if t.debugWriter != nil {
					fmt.Fprintf(t.debugWriter, "SSE read error: %v\n", err)
				}
			}
			return
		}

		switch eventType {
		case "message":
			if err := parseAndDispatchJSONRPC([]byte(data), t.pending, &t.pendingMu, t.debugWriter, handler, notifHandler); err != nil {
				if t.debugWriter != nil {
					fmt.Fprintf(t.debugWriter, "MCP Streamable HTTP: malformed message: %v\n", err)
				}
			}
		default:
			// Unknown event type — ignore per spec.
		}
	}
}

// ============================================================================
// HTTP Request Helpers
// ============================================================================

// doPOST sends an HTTP POST with the JSON-RPC message and returns the response.
// It handles session ID injection, Authorization header injection,
// and debug logging. If the server returns 401 and an AuthProvider is
// configured, the cached token is cleared and the request is retried once.
func (t *StreamableHTTPTransport) doPOST(ctx context.Context, req jsonrpcRequest) (*http.Response, error) {
	resp, err := t.doPOSTOnce(ctx, req)
	if err != nil {
		return nil, err
	}

	// If 401 and we have an auth provider, clear cached token and retry once.
	if resp.StatusCode == http.StatusUnauthorized && t.authProvider != nil {
		resp.Body.Close()
		resp, err = t.doPOSTOnce(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// doPOSTOnce is the inner POST implementation without 401 retry.
func (t *StreamableHTTPTransport) doPOSTOnce(ctx context.Context, req jsonrpcRequest) (*http.Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> %s %s\n", req.Method, formatJSON(data))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.endpointURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.negotiatedVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
	}
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.authProvider != nil {
		tok, tokErr := t.authProvider.Token(ctx)
		if tokErr == nil && tok != nil {
			httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		}
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	// Check for session ID in response.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "Session-Id: %s\n", sid)
		}
	}

	return resp, nil
}

// readJSONResponse reads and parses a JSON-RPC response body.
func (t *StreamableHTTPTransport) readJSONResponse(body io.ReadCloser) (json.RawMessage, error) {
	var resp jsonrpcResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode JSON response: %w", err)
	}

	if resp.Error != nil {
		return nil, &RPCError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}
	}
	return resp.Result, nil
}

// readTextResponse reads a text/plain response body and returns it as an
// RPCError with code -32000 (server error). Text responses indicate an
// HTTP-level error (e.g. "Forbidden: insufficient scopes").
//
//nolint:unparam // result 0 is always nil — signature matches readJSONResponse
func (t *StreamableHTTPTransport) readTextResponse(body io.ReadCloser) (json.RawMessage, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read text response: %w", err)
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		msg = "empty text/plain response"
	}
	return nil, &RPCError{Code: -32000, Message: msg}
}

// readSSEResponse reads an SSE stream from a POST response, dispatching
// events until the matching response for reqID is received.
func (t *StreamableHTTPTransport) readSSEResponse(ctx context.Context, resp *http.Response, reqID requestID) (json.RawMessage, error) {
	sr := newSSEReadCloser(resp.Body)

	// Store the SSE reader so Close() can abort it.
	t.postSSEMu.Lock()
	t.postSSEStream = sr
	t.postSSEMu.Unlock()

	defer func() {
		t.postSSEMu.Lock()
		if t.postSSEStream == sr {
			t.postSSEStream = nil
		}
		t.postSSEMu.Unlock()
		sr.Close()
	}()

	// Channel for the matching response.
	respCh := make(chan jsonrpcResponse, 1)

	// Register the pending request.
	t.pendingMu.Lock()
	select {
	case <-t.done:
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}
	t.pending[reqID] = respCh
	t.pendingMu.Unlock()

	var success bool
	defer func() {
		if !success {
			t.pendingMu.Lock()
			delete(t.pending, reqID)
			t.pendingMu.Unlock()
		}
	}()

	// Read SSE events in a goroutine so we can select on ctx/t.done.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		t.readSSELoop(sr, t.handleServerRequest, t.notificationHandler)
	}()

	select {
	case respData, ok := <-respCh:
		if !ok {
			return nil, io.EOF
		}
		if respData.Error != nil {
			return nil, &RPCError{
				Code:    respData.Error.Code,
				Message: respData.Error.Message,
				Data:    respData.Error.Data,
			}
		}
		success = true
		return respData.Result, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-t.done:
		return nil, io.EOF

	case <-readDone:
		// SSE stream ended without our response — check once more
		// in case it arrived between the last read and stream close.
		t.pendingMu.Lock()
		delete(t.pending, reqID)
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("SSE stream ended before response for %q was received", reqID)
	}
}

// ============================================================================
// Server-to-Client Request Handling
// ============================================================================

// handleServerRequest handles a JSON-RPC request from the server (e.g. ping).
// Responses are sent via HTTP POST to the endpoint (best-effort).
func (t *StreamableHTTPTransport) handleServerRequest(id requestID, method string) {
	// Use a short timeout for responding to server requests. If the server
	// cannot accept our response within 10 seconds, we drop it — there's
	// nothing more we can do.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch method {
	case methodPing:
		_ = t.sendResponse(ctx, jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Result:  json.RawMessage(`{}`),
		})

	default:
		_ = t.sendResponse(ctx, jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Error: &jsonrpcError{
				Code:    -32601,
				Message: "Method not found: " + method,
			},
		})
	}
}

// sendResponse sends a JSON-RPC response via HTTP POST to the endpoint.
func (t *StreamableHTTPTransport) sendResponse(ctx context.Context, resp jsonrpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> (response) %s\n", formatJSON(data))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.endpointURL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.negotiatedVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
	}
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}

	httpResp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST response: %w", err)
	}
	httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("POST response: unexpected status %d (expected 202)", httpResp.StatusCode)
	}

	// Check for session ID update.
	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}

	return nil
}

// processSSELine parses a single SSE field line and updates event/data state.
// The SSE spec allows an optional space after the "event:" and "data:" field names.
func processSSELine(line string, currentEvent *string, currentData *strings.Builder) {
	switch {
	case strings.HasPrefix(line, "event:"):
		if len(line) > 6 && line[6] == ' ' {
			*currentEvent = line[7:]
		} else {
			*currentEvent = line[6:]
		}

	case strings.HasPrefix(line, "data:"):
		if currentData.Len() > 0 {
			currentData.WriteString("\n")
		}
		if len(line) > 5 && line[5] == ' ' {
			currentData.WriteString(line[6:])
		} else {
			currentData.WriteString(line[5:])
		}

	case len(line) > 0 && line[0] == ':':
		// Comment — ignore.

	default:
		// Unknown field — ignore per SSE spec.
	}
}
