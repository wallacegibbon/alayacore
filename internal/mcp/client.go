package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// ClientState represents the state of an MCP client connection.
type ClientState int

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateInitializing
	StateReady
	StateFailed
)

// Client manages a connection to a single MCP server.
// It handles the lifecycle: connect → initialize → list tools → call tools.
//
// The closed channel (accessible via Done) is atomically replaceable so
// that a Client can be closed and reconnected without leaving a
// permanently-closed channel behind.
type Client struct {
	config    ServerConfig
	transport Transport
	state     atomic.Int32 // stores ClientState as int32

	// Server capabilities reported during initialization.
	capabilities ServerCapabilities
	serverInfo   ImplementationInfo

	// Tools cache — populated on initialization, refreshed on demand.
	mu    sync.RWMutex
	tools []Tool

	// Request ID counter.
	reqID atomic.Int32

	// closedCh is atomically replaceable so reconnection produces a
	// fresh channel.  Closed via closeLocked; serialized by closeMu.
	closeMu   sync.Mutex
	closeDone bool
	closedCh  atomic.Pointer[chan struct{}]
}

// NewClient creates a new MCP client. Call Connect() to establish the connection.
func NewClient(config ServerConfig) *Client {
	c := &Client{config: config}
	ch := make(chan struct{})
	c.closedCh.Store(&ch)
	return c
}

// Name returns the human-readable name of this server.
func (c *Client) Name() string {
	return c.config.Name
}

// State returns the current connection state.
func (c *Client) State() ClientState {
	return ClientState(c.state.Load())
}

// ServerInfo returns the server's implementation info from initialization.
func (c *Client) ServerInfo() ImplementationInfo {
	return c.serverInfo
}

// Connect establishes the transport and performs MCP initialization.
// Returns an error if the connection or handshake fails.
func (c *Client) Connect(ctx context.Context) error {
	return c.connectLocked(ctx)
}

// doInitialize performs the MCP initialize/initialized handshake.
func (c *Client) doInitialize(ctx context.Context) error {
	initResult, err := c.sendRequest(ctx, methodInitialize, InitializeRequest{
		ProtocolVersion: protocolVersion,
		Capabilities: ClientCapabilities{
			Roots:    nil,
			Sampling: nil,
		},
		ClientInfo: ImplementationInfo{
			Name:    "alayacore",
			Version: "0.1.0", // TODO: use config.Version
		},
	})
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(initResult, &result); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	c.capabilities = result.Capabilities
	c.serverInfo = result.ServerInfo

	// Send initialized notification (no response expected).
	_ = c.sendNotification(ctx, methodNotificationsInitialized, nil) //nolint:errcheck

	return nil
}

// ListTools fetches the list of available tools from the server.
// Supports cursor-based pagination per the MCP spec.
// Results are cached; call with force=true to refresh.
func (c *Client) ListTools(ctx context.Context, force bool) ([]Tool, error) {
	c.mu.RLock()
	if !force && c.tools != nil {
		tools := c.tools
		c.mu.RUnlock()
		return tools, nil
	}
	c.mu.RUnlock()

	if c.State() != StateReady {
		return nil, fmt.Errorf("mcp client %q: not ready (state=%d)", c.config.Name, c.State())
	}

	allTools, err := c.listToolsAllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	c.mu.Lock()
	c.tools = allTools
	c.mu.Unlock()

	return allTools, nil
}

// listToolsAllPages handles cursor-based pagination for tools/list.
// The MCP protocol allows servers to return a nextCursor when there are
// more tools than fit in a single response.
func (c *Client) listToolsAllPages(ctx context.Context) ([]Tool, error) {
	type listToolsParams struct {
		Cursor string `json:"cursor,omitempty"`
	}

	var allTools []Tool
	var cursor string

	for {
		var params any
		if cursor != "" {
			params = listToolsParams{Cursor: cursor}
		}

		result, err := c.sendRequest(ctx, methodListTools, params)
		if err != nil {
			return nil, err
		}

		var page ListToolsResult
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("parse page: %w", err)
		}

		allTools = append(allTools, page.Tools...)

		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	return allTools, nil
}

// CallTool invokes a tool on the server and returns the result.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*CallToolResult, error) {
	if c.State() != StateReady {
		return nil, fmt.Errorf("mcp client %q: not ready (state=%d)", c.config.Name, c.State())
	}

	result, err := c.sendRequest(ctx, methodCallTool, CallToolRequest{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("call %s on %q: %w", name, c.config.Name, err)
	}

	var callResult CallToolResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &callResult, nil
}

// HasTools returns true if the server advertised tool support during init.
func (c *Client) HasTools() bool {
	return c.capabilities.Tools != nil
}

// Close shuts down the client and its transport.
// The Done channel is closed permanently; to re-use the client call
// Reconnect instead.
func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if c.closeDone {
		return nil
	}
	c.closeDone = true

	ch := c.closedCh.Load()
	close(*ch)

	if c.transport != nil {
		err := c.transport.Close()
		c.state.Store(int32(StateDisconnected))
		return err
	}
	c.state.Store(int32(StateDisconnected))
	return nil
}

// Done returns a channel that closes when the client is shut down.
func (c *Client) Done() <-chan struct{} {
	return *c.closedCh.Load()
}

// Reconnect closes any existing connection and establishes a new one.
// This allows re-using a Client after a connection failure without
// creating a new instance.
func (c *Client) Reconnect(ctx context.Context) error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	// Close old transport.
	if c.transport != nil {
		c.transport.Close()
	}
	c.transport = nil

	// Create a fresh closed channel for the new lifecycle.
	ch := make(chan struct{})
	c.closedCh.Store(&ch)
	c.closeDone = false
	c.state.Store(int32(StateDisconnected))
	c.capabilities = ServerCapabilities{}
	c.serverInfo = ImplementationInfo{}
	c.tools = nil

	return c.connectLocked(ctx)
}

// connectLocked is the inner connect logic, called with closeMu held.
func (c *Client) connectLocked(ctx context.Context) error {
	if !c.state.CompareAndSwap(int32(StateDisconnected), int32(StateConnecting)) {
		return fmt.Errorf("mcp client %q: already connecting", c.config.Name)
	}
	defer func() {
		if c.state.Load() == int32(StateConnecting) {
			c.state.Store(int32(StateFailed))
		}
	}()

	var transport Transport
	var err error

	switch {
	case c.config.Command != "":
		transport, err = NewStdioTransport(c.config.Command, c.config.Args, c.config.Env, c.config.Debug)
		if err != nil {
			return fmt.Errorf("mcp client %q: %w", c.config.Name, err)
		}
	case c.config.URL != "":
		transport = NewSSETransport(c.config.URL)
	default:
		return fmt.Errorf("mcp client %q: no command or URL specified", c.config.Name)
	}

	c.transport = transport
	c.state.Store(int32(StateInitializing))
	if err := c.doInitialize(ctx); err != nil {
		transport.Close()
		return fmt.Errorf("mcp client %q initialize: %w", c.config.Name, err)
	}

	c.state.Store(int32(StateReady))
	return nil
}

// sendRequest sends a JSON-RPC request and returns the response result.
// Request/response matching is handled by the transport layer via request ID.
func (c *Client) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.transport == nil {
		return nil, fmt.Errorf("mcp client %q: no transport", c.config.Name)
	}

	// Check context before doing any work.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	id := int(c.reqID.Add(1))
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsData = data
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  paramsData,
	}

	return c.transport.SendReceive(ctx, req)
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *Client) sendNotification(_ context.Context, method string, params any) error {
	if c.transport == nil {
		return fmt.Errorf("mcp client %q: no transport", c.config.Name)
	}

	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsData = data
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      0, // notification: no ID
		Method:  method,
		Params:  paramsData,
	}

	return c.transport.Send(req)
}

// Ensure interfaces are satisfied.
var _ error = (*RPCError)(nil) //nolint:errcheck // compile-time interface check
