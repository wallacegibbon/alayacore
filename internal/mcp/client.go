package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/mcp/auth"
	"github.com/alayacore/alayacore/internal/version"
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
	config     ServerConfig
	tokenStore auth.TokenStore
	transport  atomic.Value // stores Transport or nil
	state      atomic.Int32 // stores ClientState as int32

	// Server capabilities reported during initialization.
	capabilities ServerCapabilities
	serverInfo   ImplementationInfo
	// Instructions from the server's InitializeResult, used by clients
	// to improve the LLM's understanding of available tools/resources.
	instructions string

	// staleReason is set when the server is marked stale (e.g. tool list changed).
	staleReason string

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
		config:     config,
		tokenStore: config.TokenStore,
		closedCh:   make(chan struct{}),
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
}

// Connect establishes the transport and performs MCP initialization.
// Returns an error if the connection or handshake fails.
func (c *Client) Connect(ctx context.Context) error {
	if !c.state.CompareAndSwap(int32(StateDisconnected), int32(StateConnecting)) {
		return fmt.Errorf("%q: already connecting", c.config.Name)
	}
	defer func() {
		c.state.CompareAndSwap(int32(StateConnecting), int32(StateFailed))
		c.state.CompareAndSwap(int32(StateInitializing), int32(StateFailed))
	}()

	transport, err := c.createTransport()
	if err != nil {
		return err
	}

	// For authorization_code, try loading persisted token before skipping.
	if c.needsPersistedAuth() {
		transport.Close()
		return ErrNeedsAuth
	}

	// Set up OAuth auth provider if configured.
	if err := c.setupStreamableAuth(transport); err != nil {
		transport.Close()
		return err
	}

	c.storeTransport(transport)
	c.setupNotificationHandler(transport)
	c.state.Store(int32(StateInitializing))
	if err := c.doInitialize(ctx); err != nil {
		transport.Close()
		return fmt.Errorf("%q: initialize: %w", c.config.Name, err)
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
			Version: version.Version,
		},
	})
	if err != nil {
		return err
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
	_ = c.sendNotification(ctx, methodNotificationsInitialized, nil)

	return nil
}

// listAllPages is a generic pagination helper for MCP list methods.
// It handles cursor-based pagination by repeatedly calling sendRequest
// and extracting items via the callback until nextCursor is empty.
func listAllPages[T any, P any](ctx context.Context, c *Client, op string, method string, extract func(*P) ([]T, string)) ([]T, error) {
	if c.State() != StateReady {
		return nil, c.stateError(op)
	}

	type listParams struct {
		Cursor string `json:"cursor,omitempty"`
	}

	var all []T
	var cursor string

	for {
		var params any
		if cursor != "" {
			params = listParams{Cursor: cursor}
		}

		result, err := c.sendRequest(ctx, method, params)
		if err != nil {
			return nil, err
		}

		var page P
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("parse %s: %w", op, err)
		}

		items, nextCursor := extract(&page)
		all = append(all, items...)

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return all, nil
}

// ListTools fetches the list of available tools from the server.
// Supports cursor-based pagination per the MCP spec.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	return listAllPages(ctx, c, "list tools", methodListTools,
		func(p *ListToolsResult) ([]Tool, string) { return p.Tools, p.NextCursor })
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
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	return listAllPages(ctx, c, "list resources", methodListResources,
		func(p *ListResourcesResult) ([]Resource, string) { return p.Resources, p.NextCursor })
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
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	return listAllPages(ctx, c, "list prompts", methodListPrompts,
		func(p *ListPromptsResult) ([]Prompt, string) { return p.Prompts, p.NextCursor })
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

// createTransport creates a Transport based on the server config.
func (c *Client) createTransport() (Transport, error) {
	switch {
	case c.config.Command != "":
		t, err := NewStdioTransport(c.config.Command, c.config.Args, c.config.Env, c.config.Debug)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", c.config.Name, err)
		}
		return t, nil
	case c.config.URL != "":
		return NewStreamableHTTPTransport(c.config.URL, c.config.Debug), nil
	default:
		return nil, fmt.Errorf("%q: no command or URL specified", c.config.Name)
	}
}

// needsPersistedAuth returns true if the server needs authorization_code
// auth but no token is available (in-memory or on disk).
// If a persisted token is found, it sets obtainedToken and returns false.
func (c *Client) needsPersistedAuth() bool {
	if c.config.Auth == nil || c.config.Auth.Type != AuthTypeAuthorizationCode {
		return false
	}
	if c.config.Auth.obtainedToken != nil {
		return false
	}
	// Try loading from disk.
	if c.tokenStore != nil {
		loaded, loadErr := c.tokenStore.LoadToken(c.config.Name)
		if loadErr == nil && loaded != nil && (loaded.Valid() || loaded.RefreshToken != "") {
			c.config.Auth.obtainedToken = loaded
			return false
		}
	}
	return true
}

// setupStreamableAuth creates an auth provider for Streamable HTTP transport.
func (c *Client) setupStreamableAuth(transport Transport) error {
	ht, ok := transport.(*StreamableHTTPTransport)
	if !ok || c.config.Auth == nil {
		return nil
	}
	provider, err := newAuthProvider(c.config.Auth, c.tokenStore, c.config.Name)
	if err != nil {
		return fmt.Errorf("%q auth: %w", c.config.Name, err)
	}
	if provider != nil {
		ht.SetAuthProvider(provider)
	}
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

	go c.monitorTransport(tp)
}

// monitorTransport waits for the transport to finish and transitions the
// client to StateFailed if it dies unexpectedly (i.e. not via Close()).
func (c *Client) monitorTransport(tp Transport) {
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
}

// setupNotificationHandler registers a notification handler on the transport
// so the client can react to server-to-client notifications (e.g. tool list changes).
func (c *Client) setupNotificationHandler(tp Transport) {
	h := NotificationHandler(c.handleNotification)
	switch t := tp.(type) {
	case *StdioTransport:
		t.SetNotificationHandler(h)
	case *StreamableHTTPTransport:
		t.SetNotificationHandler(h)
	}
}

// handleNotification processes a server-to-client notification.
func (c *Client) handleNotification(method string) {
	switch method {
	case methodNotificationsToolsListChanged:
		c.MarkStale("server tool list changed, restart required")
	case methodNotificationsResourcesListChanged:
		c.MarkStale("server resource list changed, restart required")
	case methodNotificationsPromptsListChanged:
		c.MarkStale("server prompt list changed, restart required")
	}
}

// startGETStream starts a long-lived GET SSE stream for Streamable HTTP transport
// to receive server-to-client messages (e.g. notifications). This is best-effort;
// errors are non-fatal — the MCP client still works for tool calls, resource reads,
// etc. via POST. Unexpected errors are written to the transport's debug log file
// if --debug-mcp is enabled.
func (c *Client) startGETStream() {
	st, ok := c.loadTransport().(*StreamableHTTPTransport)
	if !ok {
		return
	}
	// Use a background context; the stream is managed by the transport's Close().
	if err := st.StartGETStream(context.Background(), st.handleServerRequest); err != nil {
		if c.config.Debug && st.DebugWriter() != nil {
			fmt.Fprintf(st.DebugWriter(), "MCP: GET SSE stream failed for %q: %v\n", c.config.Name, err)
		}
	}
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

// resetState resets the client state to Disconnected, allowing it to be re-connected.
// This is used after ErrNeedsAuth to retry with a token.
// It closes any existing transport to prevent resource leaks from
// partially-established connections.
// resetState resets the client to StateDisconnected and closes any
// lingering transport. Used when reconnecting after OAuth auth —
// Connect leaves the client in StateFailed on ErrNeedsAuth, so we
// must reset to Disconnected before the next Connect attempt.
func (c *Client) resetState() {
	if tp := c.loadTransport(); tp != nil {
		tp.Close()
		c.storeTransport(nil)
	}
	c.state.Store(int32(StateDisconnected))
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
		return fmt.Errorf("%q: server connection lost", c.config.Name)
	case StateStale:
		return fmt.Errorf("%q: %s", c.config.Name, c.staleReason)
	default:
		return fmt.Errorf("%q: not ready (state=%d)", c.config.Name, st)
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
		return nil, fmt.Errorf("%q: no transport", c.config.Name)
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

	_ = c.sendNotification(ctx, methodNotificationsCanceled, CanceledNotificationParams{ // best-effort
		RequestID: id,
		Reason:    reason,
	})
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
	tp := c.loadTransport()
	if tp == nil {
		return fmt.Errorf("%q: no transport", c.config.Name)
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
var _ error = (*RPCError)(nil) // compile-time interface check

// newAuthProvider creates an auth.TokenProvider from the given AuthConfig.
// Returns nil if config is nil or type is empty/unknown.
// If tokenStore is non-nil and the auth type supports persistence, the
// returned provider will persist and load tokens from the store and
// automatically use refresh tokens for renewal.
func newAuthProvider(cfg *AuthConfig, tokenStore auth.TokenStore, serverName string) (auth.TokenProvider, error) { //nolint:unparam // error may be used by future auth types
	if cfg == nil {
		return nil, nil
	}

	switch cfg.Type {
	case AuthTypeStatic:
		if cfg.Token == "" {
			return nil, nil
		}
		return auth.NewCached(&auth.StaticProvider{
			TokenValue: &auth.Token{
				AccessToken: cfg.Token,
				TokenType:   "Bearer",
			},
		}), nil

	case AuthTypeAuthorizationCode:
		if cfg.obtainedToken != nil {
			// We have a token from the interactive flow — wrap in persistent
			// provider so it gets cached and persisted.
			base := auth.NewCached(&auth.StaticProvider{
				TokenValue: cfg.obtainedToken,
			})
			if tokenStore == nil {
				return base, nil
			}
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
			}
			return auth.NewPersistentTokenProvider(base, tokenStore, serverName, refreshCfg), nil
		}
		// No token yet — try loading from disk via persistent provider.
		if tokenStore != nil {
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
			}
			// Inner is nil — we have no way to get a token except from
			// disk or refresh. The caller (AuthorizeServer) will initiate
			// the interactive flow if no token is found.
			return auth.NewPersistentTokenProvider(nil, tokenStore, serverName, refreshCfg), nil
		}
		return nil, nil // no token yet, connect will be skipped by ErrNeedsAuth

	default:
		return nil, nil
	}
}
