package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
)

// SSETransport communicates with an MCP server via Server-Sent Events.
//
// Protocol flow (MCP SSE transport):
//  1. Client opens an HTTP GET to the SSE endpoint URL.
//  2. Server sends an "endpoint" event containing the HTTP POST URL for
//     sending JSON-RPC requests.
//  3. Client sends JSON-RPC requests as HTTP POST to that endpoint.
//  4. Responses arrive as SSE "message" events on the same connection.
//
// The SSE reader goroutine handles all incoming events, dispatching
// responses to the correct pending request by JSON-RPC ID.
type SSETransport struct {
	sseURL     string
	messageURL string        // POST endpoint (from endpoint event)
	httpClient *http.Client  // shared HTTP client
	sseBody    io.ReadCloser // SSE response body (for readLoop)

	pending   map[requestID]chan<- jsonrpcResponse
	pendingMu sync.Mutex

	closeOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once // guards close(done), used by both readLoop and Close
	readerWg  sync.WaitGroup

	debugWriter io.Writer
	ready       chan struct{} // closed after endpoint event is received

	// Notification handler for server-to-client notifications.
	notificationHandler NotificationHandler
}

// setupSSERequest creates an HTTP GET request for SSE connection.
func (t *SSETransport) setupSSERequest() (*http.Request, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", t.sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	return req, nil
}

// NewSSETransport creates a new SSE transport.
// It establishes the SSE connection and waits for the endpoint event
// before returning. Returns an error if the connection or endpoint
// discovery fails.
func NewSSETransport(url string, enableDebug bool) (*SSETransport, error) {
	t := &SSETransport{
		sseURL: url,
		httpClient: &http.Client{
			// No timeout for the SSE streaming connection itself.
			// Individual POST requests have caller-provided contexts.
			Timeout: 0,
		},
		pending: make(map[requestID]chan<- jsonrpcResponse),
		done:    make(chan struct{}),
		ready:   make(chan struct{}),
	}

	if enableDebug {
		t.debugWriter = debug.NewDebugWriter("alayacore-debug-mcp")
		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "MCP SSE debug log started for: %s\n", url)
		}
	}

	// Establish the SSE connection.
	req, err := t.setupSSERequest()
	if err != nil {
		return nil, err
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSE connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("SSE connect: unexpected status %d", resp.StatusCode)
	}

	t.sseBody = resp.Body

	// Start reader goroutine that handles all SSE events.
	t.readerWg.Add(1)
	go t.readLoop()

	// Wait for the endpoint event (or timeout/error).
	select {
	case <-t.ready:
		// Got the endpoint event (or connection failed — check messageURL).
	case <-time.After(sseEndpointTimeout):
		t.Close()
		return nil, fmt.Errorf("SSE: timed out waiting for endpoint event")
	}

	// If the connection dropped before we got an endpoint, fail.
	if t.messageURL == "" {
		t.Close()
		return nil, fmt.Errorf("SSE: connection closed before endpoint event")
	}

	return t, nil
}

// readLoop is the single background goroutine that reads all SSE events
// from the HTTP response stream.
func (t *SSETransport) readLoop() {
	defer t.readerWg.Done()
	defer t.doneOnce.Do(func() { close(t.done) })
	defer func() {
		// If we exit without ever getting an endpoint, signal readiness
		// (with failure) so NewSSETransport doesn't hang.
		select {
		case <-t.ready:
		default:
			close(t.ready)
		}
	}()

	scanner := bufio.NewScanner(t.sseBody)
	// Increase buffer for potentially large SSE messages (responses).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var currentData strings.Builder
	endpointReceived := false

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line signals end of an SSE event.
		if line == "" {
			if currentEvent != "" || currentData.Len() > 0 {
				t.handleSSEEvent(currentEvent, currentData.String(), &endpointReceived)
				currentEvent = ""
				currentData.Reset()
			}
			continue
		}

		// SSE field parsing — we care about "event:" and "data:".
		// Lines starting with ":" are comments; "id:" and "retry:" are ignored.
		// The SSE spec allows an optional space after the colon.
		processSSELine(line, &currentEvent, &currentData)
	}

	// Scanner finished (EOF or error).
	if err := scanner.Err(); err != nil {
		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "SSE scanner error: %v\n", err)
		}
	}
}

// handleSSEEvent processes a complete SSE event.
func (t *SSETransport) handleSSEEvent(eventType, data string, endpointReceived *bool) {
	switch eventType {
	case "endpoint":
		if *endpointReceived {
			return // ignore duplicate endpoint events
		}
		*endpointReceived = true

		msgURL := strings.TrimSpace(data)
		if msgURL == "" {
			t.Close()
			return
		}

		// Resolve relative URLs against the SSE endpoint base.
		t.messageURL = resolveURL(t.sseURL, msgURL)

		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "SSE endpoint: %s -> message URL: %s\n",
				t.sseURL, t.messageURL)
		}

		// Signal that the transport is ready.
		close(t.ready)

	case "message":
		if !*endpointReceived {
			return // ignore messages before endpoint
		}

		// Parse and dispatch the JSON-RPC message (single or batch).
		// dispatchResponse handles debug logging for each response.
		// Server-to-client requests (e.g. ping) are handled inline.
		if err := parseAndDispatchJSONRPC([]byte(data), t.pending, &t.pendingMu, t.debugWriter, t.handleServerRequest, t.notificationHandler); err != nil {
			log.Printf("MCP SSE: malformed response: %v", err)
		}

	default:
		// Unknown event type — ignore.
	}
}

// Send sends a JSON-RPC notification (no response expected) via HTTP POST.
// The context controls the HTTP request lifetime — if it expires or is
// canceled, the POST is aborted.
func (t *SSETransport) Send(ctx context.Context, req jsonrpcRequest) error {
	return t.sendPOST(ctx, req)
}

// SendReceive sends a JSON-RPC request and waits for the matching response
// via the SSE event stream. Responses are matched by request ID.
//
// On context cancellation, the pending request is unregistered and the
// response is discarded when it arrives — no transport disruption.
func (t *SSETransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	// Wait for transport readiness (endpoint event received).
	select {
	case <-t.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}

	// If we exited readLoop before getting an endpoint, the transport
	// is in a failed state.
	if t.messageURL == "" {
		return nil, io.ErrUnexpectedEOF
	}

	// Create a buffered channel for this request's response.
	respCh := make(chan jsonrpcResponse, 1)

	// Register the pending request before sending.
	t.pendingMu.Lock()
	select {
	case <-t.done:
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}
	t.pending[req.ID] = respCh
	t.pendingMu.Unlock()

	// Remove from pending map on any exit path.
	var success bool
	defer func() {
		if !success {
			t.pendingMu.Lock()
			delete(t.pending, req.ID)
			t.pendingMu.Unlock()
		}
	}()

	// Send the request via HTTP POST.
	if err := t.sendPOST(ctx, req); err != nil {
		return nil, err
	}

	// Wait for the matching response via SSE.
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
	}
}

// sendPOST sends a JSON-RPC request via HTTP POST to the message endpoint.
func (t *SSETransport) sendPOST(ctx context.Context, req jsonrpcRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> %s %s\n", req.Method, formatJSON(data))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.messageURL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// sendResponse sends a JSON-RPC response via HTTP POST to the message endpoint.
func (t *SSETransport) sendResponse(ctx context.Context, resp jsonrpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> (response) %s\n", formatJSON(data))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.messageURL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST: %w", err)
	}
	httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("POST: unexpected status %d", httpResp.StatusCode)
	}

	return nil
}

// Close shuts down the SSE connection.
// Order: close SSE response body (unblocks scanner) → wait for reader →
// close done channel.
func (t *SSETransport) Close() error {
	t.closeOnce.Do(func() {
		// Close the SSE response body — this unblocks the scanner
		// and lets readLoop exit.
		if t.sseBody != nil {
			t.sseBody.Close()
		}

		// Ensure ready is closed so nothing blocks on it.
		select {
		case <-t.ready:
		default:
			close(t.ready)
		}

		// Wait for readLoop to finish.
		t.readerWg.Wait()

		// Signal that transport is done.
		t.doneOnce.Do(func() { close(t.done) })
	})
	return nil
}

// handleServerRequest handles a JSON-RPC request from the server (e.g. ping).
// Responses are sent via HTTP POST to the message endpoint (best-effort).
func (t *SSETransport) handleServerRequest(id requestID, method string) {
	switch method {
	case methodPing:
		t.sendResponse(context.Background(), jsonrpcResponse{ //nolint:errcheck // best-effort
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Result:  json.RawMessage(`{}`),
		})

	default:
		t.sendResponse(context.Background(), jsonrpcResponse{ //nolint:errcheck // best-effort
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Error: &jsonrpcError{
				Code:    -32601,
				Message: "Method not found: " + method,
			},
		})
	}
}

// SetNotificationHandler registers a handler for server-to-client notifications.
func (t *SSETransport) SetNotificationHandler(h NotificationHandler) {
	t.notificationHandler = h
}

// Done returns a channel that closes when the transport is shut down.
func (t *SSETransport) Done() <-chan struct{} {
	return t.done
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

// resolveURL resolves a possibly-relative message URL against the SSE base URL.
func resolveURL(sseBase, messageURL string) string {
	if strings.HasPrefix(messageURL, "http://") || strings.HasPrefix(messageURL, "https://") {
		return messageURL
	}

	// Resolve relative URL.
	base, err := url.Parse(sseBase)
	if err != nil {
		return messageURL
	}

	rel, err := url.Parse(messageURL)
	if err != nil {
		return messageURL
	}

	return base.ResolveReference(rel).String()
}
