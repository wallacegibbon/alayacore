package mcp

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestParseAndDispatchJSONRPC_Single(t *testing.T) {
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex
	resultCh := make(chan jsonrpcResponse, 1)
	pending["1"] = resultCh

	data := []byte(`{"jsonrpc":"2.0","id":"1","result":{"hello":"world"}}`)
	err := parseAndDispatchJSONRPC(data, pending, &mu, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseAndDispatchJSONRPC() error = %v", err)
	}

	select {
	case resp := <-resultCh:
		if resp.ID != "1" {
			t.Errorf("resp.ID = %q, want %q", resp.ID, "1")
		}
		var result map[string]string
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result["hello"] != "world" {
			t.Errorf("result = %v, want hello=world", result)
		}
	default:
		t.Fatal("expected response on channel, got nothing")
	}
}

func TestParseAndDispatchJSONRPC_Malformed(t *testing.T) {
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex

	tests := []string{
		`not json`,
	}
	for _, tc := range tests {
		err := parseAndDispatchJSONRPC([]byte(tc), pending, &mu, nil, nil, nil)
		if err == nil {
			t.Errorf("expected error for input %q", tc)
		}
	}

	// Incomplete but valid JSON objects (missing id/result/error) are
	// silently accepted and dispatched to no-one — not an error.
	okCases := []string{
		`{"jsonrpc":"2.0"}`,
	}
	for _, tc := range okCases {
		err := parseAndDispatchJSONRPC([]byte(tc), pending, &mu, nil, nil, nil)
		if err != nil {
			t.Errorf("unexpected error for input %q: %v", tc, err)
		}
	}
}

func TestParseAndDispatchJSONRPC_NoPending(t *testing.T) {
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex

	// Response for an unknown ID should not panic or hang.
	data := []byte(`{"jsonrpc":"2.0","id":"unknown","result":{}}`)
	err := parseAndDispatchJSONRPC(data, pending, &mu, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseAndDispatchJSONRPC() error = %v", err)
	}
}

func TestDispatchResponse_CleanupOnSend(t *testing.T) {
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex

	resp := jsonrpcResponse{JSONRPC: "2.0", ID: "42", Result: json.RawMessage(`{}`)}
	pending["42"] = make(chan jsonrpcResponse, 1)

	dispatchResponse(resp, pending, &mu, nil, nil)

	// Pending entry should be removed after dispatch.
	mu.Lock()
	_, ok := pending["42"]
	mu.Unlock()
	if ok {
		t.Error("pending entry was not cleaned up after dispatch")
	}
}

func TestParseAndDispatchJSONRPC_ServerRequest(t *testing.T) {
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex

	var handledID requestID
	var handledMethod string
	handler := func(id requestID, method string) {
		handledID = id
		handledMethod = method
	}

	// Server sends a ping request.
	data := []byte(`{"jsonrpc":"2.0","id":"srv-1","method":"ping"}`)
	err := parseAndDispatchJSONRPC(data, pending, &mu, nil, handler, nil)
	if err != nil {
		t.Fatalf("parseAndDispatchJSONRPC() error = %v", err)
	}

	if handledID != "srv-1" {
		t.Errorf("handledID = %q, want %q", handledID, "srv-1")
	}
	if handledMethod != "ping" {
		t.Errorf("handledMethod = %q, want %q", handledMethod, "ping")
	}
}

func TestParseAndDispatchJSONRPC_ServerNotification(t *testing.T) {
	// Notifications (no ID) should be silently accepted without calling handler.
	pending := make(map[requestID]chan<- jsonrpcResponse)
	var mu sync.Mutex

	called := false
	handler := func(id requestID, method string) {
		called = true
	}

	data := []byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	err := parseAndDispatchJSONRPC(data, pending, &mu, nil, handler, nil)
	if err != nil {
		t.Fatalf("parseAndDispatchJSONRPC() error = %v", err)
	}

	if called {
		t.Error("handler was called for a notification (no ID)")
	}
}
