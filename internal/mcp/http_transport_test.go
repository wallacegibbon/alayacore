package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
)

// ============================================================================
// Streamable HTTP Test Server
// ============================================================================

// streamableTestServer simulates an MCP server using the Streamable HTTP transport.
type streamableTestServer struct {
	mu        sync.Mutex
	t         *testing.T
	listener  net.Listener
	serverURL string // Base URL of the test server (also the MCP endpoint)

	// Configuration set by test via options.
	sessionID       string // If set, returned as Mcp-Session-Id in responses
	requireSession  bool   // If true, reject requests without session ID (except first)
	responseMode    string // "json" (default) or "sse" for SendReceive responses
	sseResponseData string // If set, SSE data sent before the JSON-RPC response
	delayResponse   time.Duration

	// Tracking.
	postRequests   []jsonrpcRequest // All received POST requests
	lastSessionID  string           // Last session ID received in request
	requestCount   int              // Number of POST requests received
	getConnections int              // Count of GET connections

	// Channels for controlling test flow.
	postReceived chan struct{} // Signaled when a POST is received
	done         chan struct{}
	wg           sync.WaitGroup
	closeOnce    sync.Once
}

// streamableTestOption configures the test server.
type streamableTestOption func(*streamableTestServer)

func withSessionID(sid string) streamableTestOption {
	return func(s *streamableTestServer) {
		s.sessionID = sid
	}
}

func withRequireSession() streamableTestOption {
	return func(s *streamableTestServer) {
		s.requireSession = true
	}
}

func withResponseMode(mode string) streamableTestOption {
	return func(s *streamableTestServer) {
		s.responseMode = mode
	}
}

func withSSEResponseData(data string) streamableTestOption {
	return func(s *streamableTestServer) {
		s.sseResponseData = data
	}
}

func withResponseDelay(d time.Duration) streamableTestOption {
	return func(s *streamableTestServer) {
		s.delayResponse = d
	}
}

// newStreamableTestServer creates a test server for Streamable HTTP.
func newStreamableTestServer(t *testing.T, opts ...streamableTestOption) *streamableTestServer {
	t.Helper()

	s := &streamableTestServer{
		t:            t,
		responseMode: "json",
		postReceived: make(chan struct{}, 100),
		done:         make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s.listener = listener
	s.serverURL = "http://" + listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleMCP)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = http.Serve(listener, mux)
	}()

	return s
}

// URL returns the MCP endpoint URL.
func (s *streamableTestServer) URL() string {
	return s.serverURL
}

// Close shuts down the test server.
func (s *streamableTestServer) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.listener.Close()
		s.wg.Wait()
	})
}

// LastPostRequest returns the most recent POST request received.
func (s *streamableTestServer) LastPostRequest() *jsonrpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.postRequests) == 0 {
		return nil
	}
	return &s.postRequests[len(s.postRequests)-1]
}

// PostRequests returns all POST requests received.
func (s *streamableTestServer) PostRequests() []jsonrpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]jsonrpcRequest, len(s.postRequests))
	copy(cp, s.postRequests)
	return cp
}

// WaitForPost blocks until a POST request is received or timeout.
func (s *streamableTestServer) WaitForPost(t *testing.T) {
	t.Helper()
	select {
	case <-s.postReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for POST request")
	}
}

// handleMCP handles the MCP endpoint (both POST and GET).
func (s *streamableTestServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		s.handlePOST(w, r)
	case "GET":
		s.handleGET(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePOST handles JSON-RPC messages via POST.
func (s *streamableTestServer) handlePOST(w http.ResponseWriter, r *http.Request) {
	// Check Content-Type.
	ct := r.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		http.Error(w, "bad content type", http.StatusBadRequest)
		return
	}

	// Check session if required (skip for first request which establishes the session).
	sid := r.Header.Get("Mcp-Session-Id")
	s.mu.Lock()
	s.lastSessionID = sid
	s.requestCount++
	needsSession := s.requireSession && s.requestCount > 1 && sid == ""
	s.mu.Unlock()
	if needsSession {
		http.Error(w, "session required", http.StatusBadRequest)
		return
	}

	// Parse body as JSON-RPC message(s).
	// For simplicity, we handle single messages only.
	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.postRequests = append(s.postRequests, req)
	s.mu.Unlock()

	// Signal that a POST was received.
	select {
	case s.postReceived <- struct{}{}:
	default:
	}

	// Delay if requested.
	if s.delayResponse > 0 {
		time.Sleep(s.delayResponse)
	}

	// Set session ID if configured.
	if s.sessionID != "" {
		w.Header().Set("Mcp-Session-Id", s.sessionID)
	}

	// Notifications (no ID) and responses (has result/error but no method)
	// should get 202 Accepted.
	if req.ID == "" || req.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// For requests with method and ID, determine response mode.
	switch s.responseMode {
	case "sse":
		// Respond with SSE stream.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		flusher.Flush()

		// Send optional SSE data before the response.
		if s.sseResponseData != "" {
			fmt.Fprint(w, s.sseResponseData)
			flusher.Flush()
		}

		// Send the JSON-RPC response as an SSE message.
		resp := jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{"echo":"ok"}`),
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
		flusher.Flush()

	default:
		// Respond with immediate JSON.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		resp := jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{"echo":"ok"}`),
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// handleGET handles GET requests for SSE stream.
func (s *streamableTestServer) handleGET(w http.ResponseWriter, r *http.Request) {
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "text/event-stream") {
		http.Error(w, "not acceptable", http.StatusNotAcceptable)
		return
	}

	s.mu.Lock()
	s.getConnections++
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	// Hold the connection open until server closes.
	<-r.Context().Done()
}

// ============================================================================
// Streamable HTTP Transport Tests
// ============================================================================

func TestStreamableHTTP_SendReceive_JSON(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/method",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo"] != "ok" {
		t.Errorf("result.echo = %q, want %q", result["echo"], "ok")
	}
}

func TestStreamableHTTP_SendReceive_SSE(t *testing.T) {
	server := newStreamableTestServer(t, withResponseMode("sse"))
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/sse",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo"] != "ok" {
		t.Errorf("result.echo = %q, want %q", result["echo"], "ok")
	}
}

func TestStreamableHTTP_Send_Notification(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	err := transport.Send(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/test",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	server.WaitForPost(t)
	reqs := server.PostRequests()
	if len(reqs) != 1 {
		t.Fatalf("got %d POST requests, want 1", len(reqs))
	}
	if reqs[0].Method != "notifications/test" {
		t.Errorf("method = %q, want %q", reqs[0].Method, "notifications/test")
	}
}

func TestStreamableHTTP_SessionManagement(t *testing.T) {
	sessionID := "test-session-123"
	server := newStreamableTestServer(t, withSessionID(sessionID), withRequireSession())
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	// First request should get the session ID from the server.
	ctx := context.Background()
	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/init",
	})
	if err != nil {
		t.Fatalf("first SendReceive() error = %v", err)
	}

	// Verify session ID was stored.
	if transport.sessionID != sessionID {
		t.Errorf("sessionID = %q, want %q", transport.sessionID, sessionID)
	}

	// Second request should include the session ID in headers.
	// The server requires session, so this should succeed.
	_, err = transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("2"),
		Method:  "test/second",
	})
	if err != nil {
		t.Fatalf("second SendReceive() error = %v", err)
	}

	// Verify the server received the session ID.
	if server.lastSessionID != sessionID {
		t.Errorf("server received sessionID = %q, want %q", server.lastSessionID, sessionID)
	}
}

func TestStreamableHTTP_SendReceive_Error(t *testing.T) {
	// Test that a closed server returns an error.
	server := newStreamableTestServer(t)

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	// Close the server before sending — this ensures the HTTP request fails.
	server.Close()

	_, err := transport.SendReceive(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err == nil {
		t.Fatal("expected error from closed server, got nil")
	}
}

func TestStreamableHTTP_Close(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)

	// Close should not block or error.
	if err := transport.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Double close should be safe.
	if err := transport.Close(); err != nil {
		t.Errorf("Close() second call error = %v", err)
	}

	// Done should be closed.
	select {
	case <-transport.Done():
		// OK
	default:
		t.Error("Done() should be closed after Close()")
	}

	// Send after close should work (POST fails, but the transport
	// doesn't check done in Send).
	// Actually, Send tries to POST which will fail because server is closed.
}

func TestStreamableHTTP_ContextCancellation(t *testing.T) {
	server := newStreamableTestServer(t, withResponseDelay(5*time.Second))
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/slow",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context canceled") && !strings.Contains(err.Error(), "context deadline") {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestStreamableHTTP_GETStream(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	ctx := context.Background()
	err := transport.StartGETStream(ctx, nil)
	if err != nil {
		t.Fatalf("StartGETStream() error = %v", err)
	}

	// Verify the GET connection was made.
	time.Sleep(100 * time.Millisecond)
	server.mu.Lock()
	count := server.getConnections
	server.mu.Unlock()
	if count != 1 {
		t.Errorf("GET connections = %d, want 1", count)
	}

	// Close should terminate the GET stream.
	transport.Close()

	// After close, Done should be signaled.
	select {
	case <-transport.Done():
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Done()")
	}
}

func TestStreamableHTTP_DebugLogging(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), true)
	defer transport.Close()
	t.Cleanup(func() { debug.CleanupDebugWriter(transport.debugWriter) })

	ctx := context.Background()
	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/debug",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	// Notification with debug logging.
	err = transport.Send(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/debug",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestStreamableHTTP_SSEWithIntermediateData(t *testing.T) {
	// Server sends SSE data before the JSON-RPC response.
	// This simulates server-to-client notifications on the same stream.
	server := newStreamableTestServer(t, withResponseMode("sse"),
		withSSEResponseData("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"ping\",\"id\":\"srv-1\"}\n\n"))
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/intermediate",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo"] != "ok" {
		t.Errorf("result.echo = %q, want %q", result["echo"], "ok")
	}
}

func TestStreamableHTTP_MultipleConcurrentRequests(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)
	defer transport.Close()

	const numRequests = 5
	var wg sync.WaitGroup
	errs := make([]error, numRequests)

	for i := range numRequests {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			_, err := transport.SendReceive(context.Background(), jsonrpcRequest{
				JSONRPC: "2.0",
				ID:      requestID(fmt.Sprintf("c%d", idx)),
				Method:  fmt.Sprintf("concurrent/%d", idx),
			})
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}

	// All 5 requests should have been received by the server.
	reqs := server.PostRequests()
	if len(reqs) != numRequests {
		t.Errorf("server received %d requests, want %d", len(reqs), numRequests)
	}
}

func TestStreamableHTTP_NoDoubleClose(t *testing.T) {
	server := newStreamableTestServer(t)
	defer server.Close()

	transport := NewStreamableHTTPTransport(server.URL(), false)

	// Close multiple times concurrently.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			transport.Close()
		}()
	}
	wg.Wait()
}

func TestStreamableHTTP_GETStreamMethodNotAllowed(t *testing.T) {
	// Server returns 405 for GET.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			var req jsonrpcRequest
			json.NewDecoder(r.Body).Decode(&req)
			resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = http.Serve(listener, mux)
	}()

	serverURL := "http://" + listener.Addr().String()

	transport := NewStreamableHTTPTransport(serverURL, false)
	defer transport.Close()

	err = transport.StartGETStream(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for 405 GET, got nil")
	}
	if !strings.Contains(err.Error(), "405") {
		t.Errorf("expected 405 error, got: %v", err)
	}

	listener.Close()
	wg.Wait()
}
