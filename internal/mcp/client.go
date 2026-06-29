package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// ClientState represents the state of an MCP client connection.
type ClientState int

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateInitializing
	StateReady
	StateFailed
	StateStale // server tool list changed, needs restart
)

// Client manages a connection to a single MCP server.
//
// CONCURRENCY MODEL:
//   - transport: atomic.Value — safe for concurrent reads (sendRequest) without mutex
//   - closeDone: atomic.Bool — Close() and monitor atomically claim the right
//     to close closedCh via Swap(true). Only one succeeds, no mutex needed.
//   - state: atomic.Int32 — CAS for safe state transitions
//   - toolsCache: atomic.Value — atomic load/store, no lock
//
// A dedicated monitor goroutine watches transport.Done(). If the transport
// dies unexpectedly (process crash, connection drop), it transitions the
// client to StateFailed and signals it via closedCh.
//
// There is no Reconnect — if a server dies, the client stays failed.
// The caller should create a new Client if it needs to reconnect.
//
// The closed channel (closedCh) is NOT replaced after creation — it is
// closed once, either by Close() or by the monitor on unexpected death.
type Client struct {
	config    ServerConfig
	transport atomic.Value // stores Transport or nil
	state     atomic.Int32 // stores ClientState as int32

	// Server capabilities reported during initialization.
	capabilities ServerCapabilities
	serverInfo   ImplementationInfo
	// Instructions from the server's InitializeResult, used by clients
	// to improve the LLM's understanding of available tools/resources.
	instructions string

	// staleReason is set when the server is marked stale (e.g. tool list changed).
	staleReason string

	// toolsCache stores []Tool or nil — atomically loadable/storable.
	toolsCache atomic.Value

	// Request ID counter.
	reqID atomic.Int32

	// closeDone is set to true when the client is shut down (either by
	// Close() or by the monitor on transport death). The Swap(true) atomic
	// ensures only one goroutine ever closes closedCh.
	closeDone atomic.Bool

	// closedCh is closed exactly once: by the first goroutine that
	// sets closeDone to true (Close() or the transport death monitor).
	closedCh chan struct{}
}

// NewClient creates a new MCP client. Call Connect() to establish the connection.
func NewClient(config ServerConfig) *Client {
	return &Client{
		config:   config,
		closedCh: make(chan struct{}),
	}
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

// Instructions returns the server's instructions from initialization, if any.
// These can be used by clients to improve the LLM's understanding of
// available tools, resources, etc.
func (c *Client) Instructions() string {
	return c.instructions
}

// MarkStale marks the server as stale, indicating its tool list has changed
// and a restart is needed. Subsequent tool calls will return an error
// describing the reason.
func (c *Client) MarkStale(reason string) {
	c.state.Store(int32(StateStale))
	c.staleReason = reason
	c.toolsCache.Store(nil)
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

	// Verify protocol version compatibility per spec.
	// If the server responds with a version we don't support, we MUST disconnect.
	if result.ProtocolVersion != protocolVersion {
		return fmt.Errorf("unsupported protocol version %q (client supports %q)",
			result.ProtocolVersion, protocolVersion)
	}

	c.capabilities = result.Capabilities
	c.serverInfo = result.ServerInfo
	c.instructions = result.Instructions

	// Send initialized notification (no response expected).
	_ = c.sendNotification(ctx, methodNotificationsInitialized, nil) //nolint:errcheck

	return nil
}

// ListTools fetches the list of available tools from the server.
// Supports cursor-based pagination per the MCP spec.
// Results are cached; call with force=true to refresh.
func (c *Client) ListTools(ctx context.Context, force bool) ([]Tool, error) {
	if !force {
		if tv := c.toolsCache.Load(); tv != nil {
			if tools, ok := tv.([]Tool); ok {
				return tools, nil
			}
		}
	}

	if c.State() != StateReady {
		return nil, c.stateError("list tools")
	}

	allTools, err := c.listToolsAllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	c.toolsCache.Store(allTools)
	return allTools, nil
}

// listToolsAllPages handles cursor-based pagination for tools/list.
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
		return nil, c.stateError(fmt.Sprintf("call %s", name))
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

// HasResources returns true if the server advertised resource support.
func (c *Client) HasResources() bool {
	return c.capabilities.Resources != nil
}

// ListResources fetches the list of available resources from the server.
//
//nolint:dupl // similar to ListPrompts by spec design
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if c.State() != StateReady {
		return nil, c.stateError("list resources")
	}

	type listResourcesParams struct {
		Cursor string `json:"cursor,omitempty"`
	}

	var all []Resource
	var cursor string

	for {
		var params any
		if cursor != "" {
			params = listResourcesParams{Cursor: cursor}
		}

		result, err := c.sendRequest(ctx, methodListResources, params)
		if err != nil {
			return nil, err
		}

		var page ListResourcesResult
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("parse resources page: %w", err)
		}

		all = append(all, page.Resources...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	return all, nil
}

// ReadResource reads a resource by URI from the server.
func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if c.State() != StateReady {
		return nil, c.stateError("read resource")
	}

	result, err := c.sendRequest(ctx, methodReadResource, ReadResourceRequest{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("read resource %q on %q: %w", uri, c.config.Name, err)
	}

	var readResult ReadResourceResult
	if err := json.Unmarshal(result, &readResult); err != nil {
		return nil, fmt.Errorf("parse resources/read result: %w", err)
	}

	return &readResult, nil
}

// HasPrompts returns true if the server advertised prompt support.
func (c *Client) HasPrompts() bool {
	return c.capabilities.Prompts != nil
}

// ListPrompts fetches the list of available prompts from the server.
//
//nolint:dupl // similar to ListResources by spec design
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	if c.State() != StateReady {
		return nil, c.stateError("list prompts")
	}

	type listPromptsParams struct {
		Cursor string `json:"cursor,omitempty"`
	}

	var all []Prompt
	var cursor string

	for {
		var params any
		if cursor != "" {
			params = listPromptsParams{Cursor: cursor}
		}

		result, err := c.sendRequest(ctx, methodListPrompts, params)
		if err != nil {
			return nil, err
		}

		var page ListPromptsResult
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("parse prompts page: %w", err)
		}

		all = append(all, page.Prompts...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	return all, nil
}

// GetPrompt fetches a prompt by name with optional arguments.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	if c.State() != StateReady {
		return nil, c.stateError("get prompt")
	}

	result, err := c.sendRequest(ctx, methodGetPrompt, GetPromptRequest{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("get prompt %q on %q: %w", name, c.config.Name, err)
	}

	var promptResult GetPromptResult
	if err := json.Unmarshal(result, &promptResult); err != nil {
		return nil, fmt.Errorf("parse prompts/get result: %w", err)
	}

	return &promptResult, nil
}

// Close shuts down the client and its transport.
// The Done channel is closed permanently.
// Safe to call concurrently — only the first call performs the shutdown.
func (c *Client) Close() error {
	// closeDone ensures only one goroutine runs the shutdown.
	if !c.closeDone.Swap(true) {
		ch := c.closedCh
		close(ch)

		if tp := c.loadTransport(); tp != nil {
			err := tp.Close()
			c.state.Store(int32(StateDisconnected))
			return err
		}
		c.state.Store(int32(StateDisconnected))
	}
	return nil
}

// Done returns a channel that closes when the client is shut down.
func (c *Client) Done() <-chan struct{} {
	return c.closedCh
}

// connectLocked is the inner connect logic.
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
		switch c.config.TransportType {
		case TransportSSE:
			transport, err = NewSSETransport(c.config.URL, c.config.Debug)
			if err != nil {
				return fmt.Errorf("mcp client %q: %w", c.config.Name, err)
			}
		case TransportStreamable:
			transport = NewStreamableHTTPTransport(c.config.URL, c.config.Debug)
		default:
			return fmt.Errorf("mcp client %q: unknown transport type %q for URL %s",
				c.config.Name, c.config.TransportType, c.config.URL)
		}
	default:
		return fmt.Errorf("mcp client %q: no command or URL specified", c.config.Name)
	}

	c.storeTransport(transport)
	c.setupNotificationHandler(transport)
	c.state.Store(int32(StateInitializing))
	if err := c.doInitialize(ctx); err != nil {
		transport.Close()
		return fmt.Errorf("mcp client %q initialize: %w", c.config.Name, err)
	}

	// Set the negotiated protocol version on Streamable HTTP transport
	// so it can include the MCP-Protocol-Version header on all subsequent
	// HTTP requests (required by spec 2025-11-25).
	if st, ok := transport.(*StreamableHTTPTransport); ok {
		st.SetProtocolVersion(protocolVersion)
	}

	c.state.Store(int32(StateReady))
	c.startGETStream()
	c.startMonitor()
	return nil
}

// ============================================================================
// Transport Death Monitor
// ============================================================================

// startMonitor launches a goroutine that watches for transport death.
// If the transport dies unexpectedly (process crash, connection drop),
// the client transitions to StateFailed and signals it via Done().
func (c *Client) startMonitor() {
	tp := c.loadTransport()
	if tp == nil {
		return
	}

	go func() {
		<-tp.Done()

		// Only transition from Ready — Close() may have set Disconnected.
		if !c.state.CompareAndSwap(int32(StateReady), int32(StateFailed)) {
			return
		}

		// Claim the right to close closedCh.
		// If Close() ran first (closeDone already true), skip.
		if !c.closeDone.Swap(true) {
			ch := c.closedCh
			close(ch)
		}
	}()
}

// setupNotificationHandler registers a notification handler on the transport
// so the client can react to server-to-client notifications (e.g. tool list changes).
func (c *Client) setupNotificationHandler(tp Transport) {
	h := NotificationHandler(c.handleNotification)
	switch t := tp.(type) {
	case *StdioTransport:
		t.SetNotificationHandler(h)
	case *SSETransport:
		t.SetNotificationHandler(h)
	case *StreamableHTTPTransport:
		t.SetNotificationHandler(h)
	}
}

// handleNotification processes a server-to-client notification.
func (c *Client) handleNotification(method string) {
	if method == "notifications/tools/list_changed" {
		c.MarkStale("server tool list changed, restart required")
	}
}

// startGETStream starts a long-lived GET SSE stream for Streamable HTTP transport
// to receive server-to-client messages (e.g. notifications). This is best-effort;
// if the server does not support GET streams (405), it's silently ignored.
func (c *Client) startGETStream() {
	st, ok := c.loadTransport().(*StreamableHTTPTransport)
	if !ok {
		return
	}
	// Use a background context; the stream is managed by the transport's Close().
	_ = st.StartGETStream(context.Background(), st.handleServerRequest) //nolint:errcheck
}

// Ping sends a ping request to check server health.
// Returns nil if the server is alive and responsive.
func (c *Client) Ping(ctx context.Context) error {
	if c.State() != StateReady {
		return c.stateError("ping")
	}
	_, err := c.sendRequest(ctx, methodPing, nil)
	return err
}

// ============================================================================
// Transport access helpers
// ============================================================================

// loadTransport returns the current transport, or nil.
func (c *Client) loadTransport() Transport {
	v := c.transport.Load()
	if v == nil {
		return nil
	}
	tp, ok := v.(Transport)
	if !ok {
		return nil
	}
	return tp
}

// storeTransport sets the current transport.
func (c *Client) storeTransport(t Transport) {
	if t == nil {
		c.transport.Store(nil)
	} else {
		c.transport.Store(t)
	}
}

// stateError returns a descriptive error for the current client state.
func (c *Client) stateError(string) error {
	st := c.State()
	switch st {
	case StateFailed:
		return fmt.Errorf("mcp client %q: server connection lost", c.config.Name)
	case StateStale:
		return fmt.Errorf("mcp client %q: %s", c.config.Name, c.staleReason)
	default:
		return fmt.Errorf("mcp client %q: not ready (state=%d)", c.config.Name, st)
	}
}

// ============================================================================
// JSON-RPC Request/Response
// ============================================================================

// sendRequest sends a JSON-RPC request and returns the response result.
// Request/response matching is handled by the transport layer via request ID.
//
// If the context is canceled while waiting for a response, a best-effort
// cancellation notification is sent to the server so it can abort
// processing early.
func (c *Client) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	tp := c.loadTransport()
	if tp == nil {
		return nil, fmt.Errorf("mcp client %q: no transport", c.config.Name)
	}

	// Check context before doing any work.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	id := requestID(fmt.Sprintf("%d", c.reqID.Add(1)))
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

	result, err := tp.SendReceive(ctx, req)
	if err != nil {
		// If the request was canceled (user :cancel, timeout), send a
		// best-effort cancellation notification so the server can abort
		// processing. This is a notification (fire-and-forget), so we
		// don't wait for it.
		// Per spec, the initialize request MUST NOT be canceled.
		if method != methodInitialize && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			c.sendCanceledNotification(id, err)
		}
		return nil, err
	}
	return result, nil
}

// sendCanceledNotification sends a cancellation notification to the
// server as a best-effort hint that it should abort processing of the given
// request. Uses a short timeout context so it doesn't block indefinitely.
func (c *Client) sendCanceledNotification(id requestID, cause error) {
	reason := "request canceled"
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "timeout"
	}

	// Use a short timeout so a slow/hung server doesn't block shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = c.sendNotification(ctx, methodNotificationsCanceled, CanceledNotificationParams{ //nolint:errcheck // best-effort
		RequestID: id,
		Reason:    reason,
	})
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
	tp := c.loadTransport()
	if tp == nil {
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
		ID:      requestID(""), // notification: no ID (omitempty omits empty string)
		Method:  method,
		Params:  paramsData,
	}

	return tp.Send(ctx, req)
}

// Ensure interfaces are satisfied.
var _ error = (*RPCError)(nil) //nolint:errcheck // compile-time interface check
