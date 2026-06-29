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
// SSE Test Server
// ============================================================================

// sseTestServer simulates an MCP server over SSE for testing.
type sseTestServer struct {
	mu        sync.Mutex
	t         *testing.T
	ssePath   string // Path for SSE endpoint (default: /sse)
	postPath  string // Path for POST endpoint (default: /message)
	listener  net.Listener
	serverURL string // Base URL of the test server

	// Channels for controlling SSE event flow.
	sseConnected chan struct{}    // Closed when an SSE client connects
	postRequests []jsonrpcRequest // All received POST requests
	responses    chan string      // SSE events to send (one per message)
	done         chan struct{}
	wg           sync.WaitGroup
}

// newSSETestServer creates a test SSE server.
func newSSETestServer(t *testing.T) *sseTestServer {
	t.Helper()

	s := &sseTestServer{
		t:            t,
		ssePath:      "/sse",
		postPath:     "/message",
		sseConnected: make(chan struct{}),
		responses:    make(chan string, 100),
		done:         make(chan struct{}),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s.listener = listener
	s.serverURL = "http://" + listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc(s.ssePath, s.handleSSE)
	mux.HandleFunc(s.postPath, s.handlePOST)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		//nolint:errcheck // server closes cleanly via shutdown
		http.Serve(listener, mux)
	}()

	return s
}

// SSEURL returns the full SSE endpoint URL.
func (s *sseTestServer) SSEURL() string {
	return s.serverURL + s.ssePath
}

// PostURL returns the full POST endpoint URL.
func (s *sseTestServer) PostURL() string {
	return s.serverURL + s.postPath
}

// SendEvent sends an SSE event to the connected client.
func (s *sseTestServer) SendEvent(eventType, data string) {
	select {
	case s.responses <- fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data):
	case <-s.done:
	}
}

// SendEventCompact sends an SSE event without a space after "data:".
// Some MCP servers use this format.
func (s *sseTestServer) SendEventCompact(eventType, data string) {
	select {
	case s.responses <- fmt.Sprintf("event: %s\ndata:%s\n\n", eventType, data):
	case <-s.done:
	}
}

// SendJSONRPCResponse sends a JSON-RPC response as an SSE message event.
func (s *sseTestServer) SendJSONRPCResponse(id requestID, result json.RawMessage) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	s.SendEvent("message", string(data))
}

// SendJSONRPCResponseCompact is like SendJSONRPCResponse but uses
// the compact SSE format (no space after "data:").
func (s *sseTestServer) SendJSONRPCResponseCompact(id requestID, result json.RawMessage) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	s.SendEventCompact("message", string(data))
}

// SendJSONRPCError sends a JSON-RPC error response as an SSE message event.
func (s *sseTestServer) SendJSONRPCError(id requestID, code int, message string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonrpcError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	s.SendEvent("message", string(data))
}

// WaitForSSEConnection blocks until an SSE client connects.
func (s *sseTestServer) WaitForSSEConnection(t *testing.T) {
	t.Helper()
	select {
	case <-s.sseConnected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSE client to connect")
	}
}

// LastPostRequest returns the most recent POST request received.
func (s *sseTestServer) LastPostRequest() *jsonrpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.postRequests) == 0 {
		return nil
	}
	return &s.postRequests[len(s.postRequests)-1]
}

// PostRequests returns all POST requests received.
func (s *sseTestServer) PostRequests() []jsonrpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]jsonrpcRequest, len(s.postRequests))
	copy(cp, s.postRequests)
	return cp
}

// Close shuts down the test server.
func (s *sseTestServer) Close() {
	close(s.done)
	s.listener.Close()
	s.wg.Wait()
}

// handleSSE serves the SSE stream.
func (s *sseTestServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Signal that SSE client connected.
	select {
	case <-s.sseConnected:
	default:
		close(s.sseConnected)
	}

	// Send the required endpoint event immediately.
	endpointEvent := fmt.Sprintf("event: endpoint\ndata: %s\n\n", s.PostURL())
	fmt.Fprint(w, endpointEvent)
	flusher.Flush()

	// Send any queued response events.
	for {
		select {
		case evt := <-s.responses:
			fmt.Fprint(w, evt)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
	}
}

// handlePOST handles JSON-RPC requests via HTTP POST.
func (s *sseTestServer) handlePOST(w http.ResponseWriter, r *http.Request) {
	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.postRequests = append(s.postRequests, req)
	s.mu.Unlock()

	// For notifications (empty ID), just acknowledge.
	if req.ID == requestID("") {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// For requests with an ID, the response is sent via SSE.
	// The test sends it separately via SendJSONRPCResponse.
	w.WriteHeader(http.StatusAccepted)
}

// ============================================================================
// SSE Transport Tests
// ============================================================================

func TestSSETransport_Connect(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	if transport.messageURL != server.PostURL() {
		t.Errorf("messageURL = %q, want %q", transport.messageURL, server.PostURL())
	}

	// Verify the transport is ready.
	select {
	case <-transport.ready:
		// OK
	default:
		t.Error("transport.ready should be closed after successful connect")
	}

	// Verify Done is not closed.
	select {
	case <-transport.Done():
		t.Error("transport.Done() should not be closed while running")
	default:
	}
}

func TestSSETransport_SendReceive(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	// Send a request and expect a response via SSE.
	ctx := context.Background()
	resultData := json.RawMessage(`{"data":"hello"}`)

	go func() {
		server.WaitForSSEConnection(t)
		server.SendJSONRPCResponse(requestID("1"), resultData)
	}()

	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["data"] != "hello" {
		t.Errorf("result.data = %q, want %q", result["data"], "hello")
	}
}

func TestSSETransport_SendReceive_Error(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	ctx := context.Background()

	go func() {
		server.WaitForSSEConnection(t)
		server.SendJSONRPCError(requestID("1"), -32601, "Method not found")
	}()

	_, err = transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "unknown_method",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32601 {
		t.Errorf("error code = %d, want %d", rpcErr.Code, -32601)
	}
	if rpcErr.Message != "Method not found" {
		t.Errorf("error message = %q, want %q", rpcErr.Message, "Method not found")
	}
}

func TestSSETransport_SendNotification(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	// Send a notification (ID=0, no response expected).
	err = transport.Send(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID(""),
		Method:  "notifications/initialized",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Wait a bit for the POST to arrive.
	time.Sleep(100 * time.Millisecond)

	reqs := server.PostRequests()
	if len(reqs) != 1 {
		t.Fatalf("got %d POST requests, want 1", len(reqs))
	}
	if reqs[0].Method != "notifications/initialized" {
		t.Errorf("method = %q, want %q", reqs[0].Method, "notifications/initialized")
	}
}

func TestSSETransport_ContextCancellation(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	// Create a context that will be canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	// The error is wrapped: the HTTP POST fails because the context is
	// already canceled, resulting in "POST: Post ...: context canceled".
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected error containing 'context canceled', got: %v", err)
	}
}

func TestSSETransport_TimeoutOnNoEndpoint(t *testing.T) {
	// Speed up the test by temporarily reducing the endpoint timeout.
	oldTimeout := sseEndpointTimeout
	sseEndpointTimeout = 2 * time.Second
	t.Cleanup(func() { sseEndpointTimeout = oldTimeout })

	// Create a server that never sends an endpoint event.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		// Never send endpoint event — just hold the connection open.
		<-r.Context().Done()
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		//nolint:errcheck
		http.Serve(listener, mux)
	}()

	sseURL := "http://" + listener.Addr().String() + "/sse"

	_, err = NewSSETransport(sseURL, false)
	if err == nil {
		listener.Close()
		wg.Wait()
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}

	listener.Close()
	wg.Wait()
}

func TestSSETransport_Close(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}

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

	// SendReceive after close should return error.
	_, err = transport.SendReceive(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err == nil {
		t.Error("expected error after close, got nil")
	}
}

func TestSSETransport_MultipleConcurrentRequests(t *testing.T) {
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	// Send 5 concurrent requests.
	const numRequests = 5
	var wg sync.WaitGroup
	errs := make([]error, numRequests)
	results := make([]string, numRequests)

	for i := range numRequests {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Register a response from the server.
			server.SendJSONRPCResponse(requestID(fmt.Sprintf("%d", idx+1)), json.RawMessage(
				fmt.Sprintf(`{"index":%d}`, idx)))

			resp, err := transport.SendReceive(context.Background(), jsonrpcRequest{
				JSONRPC: "2.0",
				ID:      requestID(fmt.Sprintf("%d", idx+1)),
				Method:  "tool/call",
				Params:  json.RawMessage(fmt.Sprintf(`{"name":"tool%d"}`, idx)),
			})
			if err != nil {
				errs[idx] = err
				return
			}

			var result map[string]int
			if err := json.Unmarshal(resp, &result); err != nil {
				errs[idx] = err
				return
			}
			results[idx] = fmt.Sprintf("index=%d", result["index"])
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
		if results[i] != fmt.Sprintf("index=%d", i) {
			t.Errorf("request %d result = %q, want %q", i, results[i], fmt.Sprintf("index=%d", i))
		}
	}
}

func TestSSETransport_RelativeEndpointURL(t *testing.T) {
	// Test that relative endpoint URLs are resolved correctly.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/custom-sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send a relative endpoint URL.
		fmt.Fprint(w, "event: endpoint\ndata: /custom-message\n\n")
		flusher.Flush()
		<-r.Context().Done()
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		//nolint:errcheck
		http.Serve(listener, mux)
	}()

	sseURL := "http://" + listener.Addr().String() + "/custom-sse"

	transport, err := NewSSETransport(sseURL, false)
	if err != nil {
		listener.Close()
		wg.Wait()
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	// The message URL should be resolved relative to the SSE URL base.
	expectedMsgURL := "http://" + listener.Addr().String() + "/custom-message"
	if transport.messageURL != expectedMsgURL {
		t.Errorf("messageURL = %q, want %q", transport.messageURL, expectedMsgURL)
	}

	listener.Close()
	wg.Wait()
}

func TestSSETransport_AbsoluteEndpointURL(t *testing.T) {
	// Test that absolute endpoint URLs are used as-is.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	absMsgURL := "http://" + listener.Addr().String() + "/msg"

	mux := http.NewServeMux()
	mux.HandleFunc("/sse2", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", absMsgURL)
		flusher.Flush()
		<-r.Context().Done()
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		//nolint:errcheck
		http.Serve(listener, mux)
	}()

	sseURL := "http://" + listener.Addr().String() + "/sse2"

	transport, err := NewSSETransport(sseURL, false)
	if err != nil {
		listener.Close()
		wg.Wait()
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	if transport.messageURL != absMsgURL {
		t.Errorf("messageURL = %q, want %q", transport.messageURL, absMsgURL)
	}

	listener.Close()
	wg.Wait()
}

func TestSSETransport_ServerNonOK(t *testing.T) {
	// Test that a non-200 response from the SSE endpoint fails.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		//nolint:errcheck
		http.Serve(listener, mux)
	}()

	sseURL := "http://" + listener.Addr().String() + "/sse"

	_, err = NewSSETransport(sseURL, false)
	if err == nil {
		listener.Close()
		wg.Wait()
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("expected status error, got: %v", err)
	}

	listener.Close()
	wg.Wait()
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		base string
		rel  string
		want string
	}{
		{
			base: "http://localhost:8080/sse",
			rel:  "/message",
			want: "http://localhost:8080/message",
		},
		{
			base: "https://example.com/path/sse",
			rel:  "/api/message",
			want: "https://example.com/api/message",
		},
		{
			base: "http://localhost:8080/sse",
			rel:  "http://other.com/message",
			want: "http://other.com/message",
		},
		{
			base: "http://localhost:8080/sse?id=1",
			rel:  "message",
			want: "http://localhost:8080/message",
		},
		{
			base: "http://localhost:8080/sse/",
			rel:  "message",
			want: "http://localhost:8080/sse/message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.rel+"@"+tt.base, func(t *testing.T) {
			got := resolveURL(tt.base, tt.rel)
			if got != tt.want {
				t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.base, tt.rel, got, tt.want)
			}
		})
	}
}

func TestSSETransport_DoneOnConnectionDrop(t *testing.T) {
	// Test that Done() is signaled when Close() is called.
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}

	transport.Close()

	select {
	case <-transport.Done():
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Done() after Close()")
	}
}

func TestSSETransport_DebugLogging(t *testing.T) {
	// Test that debug logging doesn't crash when enabled.
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), true)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()
	t.Cleanup(func() { debug.CleanupDebugWriter(transport.debugWriter) })

	ctx := context.Background()
	resultData := json.RawMessage(`{"ok":true}`)

	go func() {
		server.WaitForSSEConnection(t)
		server.SendJSONRPCResponse(requestID("1"), resultData)
	}()

	_, err = transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	// Also test notification with debug logging.
	err = transport.Send(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID(""),
		Method:  "notifications/initialized",
	})
	if err != nil {
		t.Fatalf("Send() with debug error = %v", err)
	}
}

func TestSSETransport_SendReceive_SSEWithoutSpace(t *testing.T) {
	// Some MCP servers send SSE data: lines without a space after the colon
	// (e.g. "data:{"jsonrpc":...}" instead of "data: {"jsonrpc":...}").
	// Verify that both formats work.
	server := newSSETestServer(t)
	defer server.Close()

	transport, err := NewSSETransport(server.SSEURL(), false)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close()

	ctx := context.Background()
	resultData := json.RawMessage(`{"echo":"hello"}`)

	go func() {
		server.WaitForSSEConnection(t)
		server.SendJSONRPCResponseCompact(requestID("1"), resultData)
	}()

	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo"] != "hello" {
		t.Errorf("result.echo = %q, want %q", result["echo"], "hello")
	}
}
