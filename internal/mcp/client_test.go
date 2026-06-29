package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// mockTransport implements Transport for testing.
type mockTransport struct {
	mu        sync.Mutex
	requests  []jsonrpcRequest
	responses []json.RawMessage
	index     atomic.Int32
	done      chan struct{}
}

func newMockTransport(responses []json.RawMessage) *mockTransport {
	return &mockTransport{
		responses: responses,
		done:      make(chan struct{}),
	}
}

func (m *mockTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	_ = ctx
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return nil
}

func (m *mockTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	// Store the request like Send does.
	m.mu.Lock()
	m.requests = append(m.requests, req)
	idx := int(m.index.Add(1) - 1)
	if idx >= len(m.responses) {
		m.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	raw := m.responses[idx]
	m.mu.Unlock()

	// Unwrap JSON-RPC envelope to extract the result.
	var resp jsonrpcResponse
	if err := json.Unmarshal(raw, &resp); err == nil {
		if resp.Error != nil {
			return nil, &RPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		return resp.Result, nil
	}
	return raw, nil
}

func (m *mockTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	idx := int(m.index.Add(1) - 1)
	if idx >= len(m.responses) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return m.responses[idx], nil
}

func (m *mockTransport) Close() error {
	close(m.done)
	return nil
}

func (m *mockTransport) Done() <-chan struct{} {
	return m.done
}

func TestClientInitialize(t *testing.T) {
	// Mock MCP initialize response.
	initResult := InitializeResult{
		ProtocolVersion: "2025-03-26",
		Capabilities: ServerCapabilities{
			Tools: &struct{}{},
		},
		ServerInfo: ImplementationInfo{
			Name:    "test-server",
			Version: "1.0.0",
		},
	}
	initData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  mustMarshal(initResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.storeTransport(newMockTransport([]json.RawMessage{initData}))

	ctx := context.Background()
	if err := client.doInitialize(ctx); err != nil {
		t.Fatalf("doInitialize() error = %v", err)
	}

	if client.serverInfo.Name != "test-server" {
		t.Errorf("ServerInfo.Name = %q, want %q", client.serverInfo.Name, "test-server")
	}
	if client.capabilities.Tools == nil {
		t.Error("capabilities.Tools is nil, expected non-nil")
	}
}

func TestClientListTools(t *testing.T) {
	toolsResult := ListToolsResult{
		Tools: []Tool{
			{Name: "tool1", Description: "First tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "tool2", Description: "Second tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	toolsData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0", ID: requestID("2"),
		Result: mustMarshal(toolsResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{toolsData}))

	// Need to set init state to skip initialize for this test.
	client.capabilities.Tools = &struct{}{}

	ctx := context.Background()
	tools, err := client.ListTools(ctx, false)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "tool1" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "tool1")
	}
	if tools[1].Name != "tool2" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "tool2")
	}
}

func TestClientCallTool(t *testing.T) {
	callResult := CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "result data"},
		},
	}
	callData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0", ID: requestID("1"),
		Result: mustMarshal(callResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{callData}))

	ctx := context.Background()
	result, err := client.CallTool(ctx, "my_tool", json.RawMessage(`{"arg":"val"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "result data" {
		t.Errorf("Content[0].Text = %q, want %q", result.Content[0].Text, "result data")
	}
}

func TestClientStateTransitions(t *testing.T) {
	client := NewClient(ServerConfig{Name: "test"})

	if client.State() != StateDisconnected {
		t.Errorf("initial state = %d, want %d", client.State(), StateDisconnected)
	}

	// Close without connecting.
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestManagerEmpty(t *testing.T) {
	m := NewManager(nil)
	if m == nil {
		t.Fatal("NewManager(nil) returned nil")
	}

	ctx := context.Background()
	errs := m.ConnectAll(ctx)
	if len(errs) != 0 {
		t.Errorf("ConnectAll() errors = %v, want none", errs)
	}

	tools := m.DiscoverTools(ctx)
	if len(tools) != 0 {
		t.Errorf("DiscoverTools() = %v, want empty", tools)
	}

	m.CloseAll()
}

func TestClientInitialize_VersionMismatch(t *testing.T) {
	// Server responds with a different protocol version — client must disconnect.
	initResult := InitializeResult{
		ProtocolVersion: "2024-11-05", // Different from our "2025-03-26"
		Capabilities: ServerCapabilities{
			Tools: &struct{}{},
		},
		ServerInfo: ImplementationInfo{
			Name:    "old-server",
			Version: "1.0.0",
		},
	}
	initData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  mustMarshal(initResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.storeTransport(newMockTransport([]json.RawMessage{initData}))

	ctx := context.Background()
	err := client.doInitialize(ctx)
	if err == nil {
		t.Fatal("expected error for version mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Errorf("expected 'unsupported protocol version' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "2024-11-05") {
		t.Errorf("expected server version in error, got: %v", err)
	}
}

func TestToolsToAgentTools(t *testing.T) {
	// Create a manager (not connected, just for tool adapter).
	m := NewManager([]ServerConfig{
		{Name: "srv1", Command: "echo", Args: []string{"hello"}},
	})

	serverTools := map[string][]Tool{
		"srv1": {
			{Name: "greet", Description: "Say hello", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "farewell", Description: "Say goodbye", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}

	agentTools := ToolsToAgentTools(serverTools, m)
	if len(agentTools) != 2 {
		t.Fatalf("got %d tools, want 2", len(agentTools))
	}

	// Check naming with prefix strategy.
	expectedNames := []string{"srv1_greet", "srv1_farewell"}
	for i, name := range expectedNames {
		if agentTools[i].Definition.Name != name {
			t.Errorf("agentTools[%d].Name = %q, want %q", i, agentTools[i].Definition.Name, name)
		}
	}

	// Check descriptions include server name.
	for _, tool := range agentTools {
		if tool.Definition.Description == "" {
			t.Errorf("tool %q has empty description", tool.Definition.Name)
		}
	}
}

// mustMarshal is a test helper that marshals v to JSON or panics.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
