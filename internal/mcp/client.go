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

	// adapter handles protocol-version-specific behavior
	// (handshake, _meta injection, and HTTP transport hooks).
	adapter Adapter

	// Server capabilities reported during initialization.
	capabilities ServerCapabilities
	serverInfo   ImplementationInfo
	instructions string

	// staleReason is set when the server is marked stale (e.g. tool list changed).
	staleReason string

	// Request ID counter.
	reqID atomic.Int32

	// closeDone is set to true when the client is shut down.
	closeDone atomic.Bool
	closedCh  chan struct{}
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
func (c *Client) Instructions() string {
	return c.instructions
}

// MarkStale marks the server as stale, indicating its tool list has changed.
func (c *Client) MarkStale(reason string) {
	c.state.Store(int32(StateStale))
	c.staleReason = reason
}

// Connect establishes the transport and performs MCP initialization.
func (c *Client) Connect(ctx context.Context) error {
	if !c.state.CompareAndSwap(int32(StateDisconnected), int32(StateConnecting)) {
		return fmt.Errorf("%q: already connecting", c.config.Name)
	}
	defer func() {
		c.state.CompareAndSwap(int32(StateConnecting), int32(StateFailed))
		c.state.CompareAndSwap(int32(StateInitializing), int32(StateFailed))
	}()

	// proto-version is required — no default.
	switch c.config.ProtoVersion {
	case "2025-11-25":
		c.adapter = NewAdapterV20251125()
	case "2026-07-28":
		c.adapter = NewAdapterV20260728()
	case "":
		return fmt.Errorf("%q: proto-version is required (e.g. proto-version=2025-11-25)", c.config.Name)
	default:
		return fmt.Errorf("%q: unsupported proto-version %q", c.config.Name, c.config.ProtoVersion)
	}

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
	c.setupAuth(transport)

	c.storeTransport(transport)
	c.setupNotificationHandler(transport)
	c.state.Store(int32(StateInitializing))

	// Negotiate protocol version using the configured adapter.
	// Attach adapter to HTTP transport before handshake so version headers
	// (MCP-Protocol-Version, etc.) are included on all requests.
	if ht, ok := transport.(*HTTPTransport); ok {
		if ha, ok := c.adapter.(HTTPAdapter); ok {
			ht.SetHTTPAdapter(ha)
		}
	}

	_, err = c.negotiateAndHandshake(ctx)
	if err != nil {
		transport.Close()
		return fmt.Errorf("%q: handshake: %w", c.config.Name, err)
	}

	c.state.Store(int32(StateReady))

	// Let the adapter start version-specific resources.
	// (e.g. GET stream for 2025-11-25, no-op for 2026-07-28)
	// Only for HTTP transport; stdio doesn't use OnTransportReady.
	if ht, ok := transport.(*HTTPTransport); ok {
		if ha, ok := c.adapter.(HTTPAdapter); ok {
			if err := ha.OnTransportReady(ctx, ht); err != nil {
				if c.config.Debug {
					if dw := ht.DebugWriter(); dw != nil {
						fmt.Fprintf(dw, "MCP: OnTransportReady failed for %q: %v\n", c.config.Name, err)
					}
				}
				// Non-fatal — the client still works for tool calls via POST.
			}
		}
	}

	c.startMonitor()
	return nil
}

// doInitialize performs the MCP initialize/initialized handshake.
func (c *Client) doInitialize(ctx context.Context) (string, error) {
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
		return "", err
	}

	var result InitializeResult
	if err := json.Unmarshal(initResult, &result); err != nil {
		return "", fmt.Errorf("parse initialize result: %w", err)
	}

	if result.ProtocolVersion != protocolVersion {
		return "", fmt.Errorf("unsupported protocol version %q (client supports %q)",
			result.ProtocolVersion, protocolVersion)
	}

	c.capabilities = result.Capabilities
	c.serverInfo = result.ServerInfo
	c.instructions = result.Instructions

	// Send initialized notification (no response expected).
	_ = c.sendNotification(ctx, methodNotificationsInitialized, nil)

	return result.ProtocolVersion, nil
}

// listAllPages is a generic pagination helper for MCP list methods.
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

// HasTools returns true if the server advertised tool support.
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
func (c *Client) Close() error {
	if !c.closeDone.Swap(true) {
		ch := c.closedCh
		close(ch)

		// Notify adapter before closing transport.
		if c.adapter != nil {
			c.adapter.OnClose()
		}

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
		return NewHTTPTransport(c.config.URL, c.config.Debug), nil
	default:
		return nil, fmt.Errorf("%q: no command or URL specified", c.config.Name)
	}
}

// needsPersistedAuth returns true if the server needs authorization_code
// auth but no token is available (in-memory or on disk).
func (c *Client) needsPersistedAuth() bool {
	if c.config.Auth == nil || c.config.Auth.Type != AuthTypeAuthorizationCode {
		return false
	}
	if c.config.Auth.obtainedToken != nil {
		return false
	}
	if c.tokenStore != nil {
		loaded, loadErr := c.tokenStore.LoadToken(c.config.Name)
		if loadErr == nil && loaded != nil && (loaded.Valid() || loaded.RefreshToken != "") {
			c.config.Auth.obtainedToken = loaded
			return false
		}
	}
	return true
}

// setupAuth creates an auth provider for the transport, if configured.
func (c *Client) setupAuth(transport Transport) {
	if c.config.Auth == nil {
		return
	}

	provider := newAuthProvider(c.config.Auth, c.tokenStore, c.config.Name)
	if provider == nil {
		return
	}

	if ht, ok := transport.(*HTTPTransport); ok {
		ht.SetAuthProvider(provider)
	}
}

// ============================================================================
// Transport Death Monitor
// ============================================================================

// startMonitor launches a goroutine that watches for transport death.
func (c *Client) startMonitor() {
	tp := c.loadTransport()
	if tp == nil {
		return
	}

	go c.monitorTransport(tp)
}

// monitorTransport waits for the transport to finish and transitions the
// client to StateFailed if it dies unexpectedly.
func (c *Client) monitorTransport(tp Transport) {
	<-tp.Done()

	if !c.state.CompareAndSwap(int32(StateReady), int32(StateFailed)) {
		return
	}

	if !c.closeDone.Swap(true) {
		ch := c.closedCh
		close(ch)
	}
}

// setupNotificationHandler registers a notification handler on the transport.
func (c *Client) setupNotificationHandler(tp Transport) {
	h := NotificationHandler(c.handleNotification)

	// Both transport types support SetNotificationHandler via their
	// Transport interface or type-specific method.
	switch t := tp.(type) {
	case *StdioTransport:
		t.SetNotificationHandler(h)
	case *HTTPTransport:
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

// Ping sends a ping request to check server health.
func (c *Client) Ping(ctx context.Context) error {
	if c.State() != StateReady {
		return c.stateError("ping")
	}
	_, err := c.sendRequest(ctx, methodPing, nil)
	return err
}

// resetState resets the client to StateDisconnected for reconnection.
func (c *Client) resetState() {
	if tp := c.loadTransport(); tp != nil {
		tp.Close()
		c.storeTransport(nil)
	}
	c.state.Store(int32(StateDisconnected))
}

// AuthorizeAndConnect persists the obtained OAuth token and reconnects.
func (c *Client) AuthorizeAndConnect(ctx context.Context, token *auth.Token) error {
	c.config.Auth.obtainedToken = token
	if c.tokenStore != nil {
		_ = c.tokenStore.SaveToken(c.Name(), token)
	}
	c.resetState()
	if err := c.Connect(ctx); err != nil {
		c.config.Auth.obtainedToken = nil
		return fmt.Errorf("%q: connect after auth: %w", c.Name(), err)
	}
	return nil
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
// Protocol Negotiation
// ============================================================================

// negotiateAndHandshake performs the handshake and verifies the server
// supports the configured proto-version.
func (c *Client) negotiateAndHandshake(ctx context.Context) (string, error) {
	version, err := c.adapter.Handshake(ctx, c)
	if err != nil {
		return "", fmt.Errorf("handshake with %s: %w", c.config.ProtoVersion, err)
	}
	if version != c.config.ProtoVersion {
		return "", fmt.Errorf("server does not support protocol version %q (supports %q)",
			c.config.ProtoVersion, version)
	}
	return version, nil
}

// isHandshakeMethod returns true if the method is a handshake method.
func isHandshakeMethod(method string) bool {
	return method == methodInitialize || method == methodDiscover
}

// ============================================================================
// JSON-RPC Request/Response
// ============================================================================

// sendRequest sends a JSON-RPC request and returns the response result.
func (c *Client) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	tp := c.loadTransport()
	if tp == nil {
		return nil, fmt.Errorf("%q: no transport", c.config.Name)
	}

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

	// Inject _meta if the protocol version requires it (2026-07-28+).
	if c.adapter != nil {
		meta := c.adapter.BuildRequestMeta(c)
		if meta != nil {
			var err error
			paramsData, err = injectMeta(paramsData, meta)
			if err != nil {
				return nil, fmt.Errorf("inject _meta: %w", err)
			}
		}
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  paramsData,
	}

	result, err := tp.SendReceive(ctx, req)
	if err != nil {
		if !isHandshakeMethod(method) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			c.sendCanceledNotification(id, err)
		}
		return nil, err
	}
	return result, nil
}

// sendCanceledNotification sends a cancellation notification to the server.
func (c *Client) sendCanceledNotification(id requestID, cause error) {
	reason := "request canceled"
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "timeout"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = c.sendNotification(ctx, methodNotificationsCanceled, CanceledNotificationParams{
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

	// Marshal params first so we can inject _meta.
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal notification params: %w", err)
		}
		paramsData = data
	}

	// Inject _meta if the protocol version requires it (2026-07-28+).
	if c.adapter != nil {
		meta := c.adapter.BuildRequestMeta(c)
		if meta != nil {
			var err error
			paramsData, err = injectMeta(paramsData, meta)
			if err != nil {
				return fmt.Errorf("inject _meta into notification: %w", err)
			}
		}
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      requestID(""), // notification: no ID (omitempty omits empty string)
		Method:  method,
		Params:  paramsData,
	}

	return tp.Send(ctx, req)
}

// compile-time interface checks.
var _ error = (*RPCError)(nil)

// newAuthProvider creates an auth.TokenProvider from the given AuthConfig.
func newAuthProvider(cfg *AuthConfig, tokenStore auth.TokenStore, serverName string) auth.TokenProvider {
	if cfg == nil {
		return nil
	}

	switch cfg.Type {
	case AuthTypeStatic:
		if cfg.Token == "" {
			return nil
		}
		return auth.NewCached(&auth.StaticProvider{
			TokenValue: &auth.Token{
				AccessToken: cfg.Token,
				TokenType:   "Bearer",
			},
		})

	case AuthTypeAuthorizationCode:
		if cfg.obtainedToken != nil {
			base := auth.NewCached(&auth.StaticProvider{
				TokenValue: cfg.obtainedToken,
			})
			if tokenStore == nil {
				return base
			}
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
			}
			return auth.NewPersistentTokenProvider(base, tokenStore, serverName, refreshCfg)
		}
		if tokenStore != nil {
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
			}
			return auth.NewPersistentTokenProvider(nil, tokenStore, serverName, refreshCfg)
		}
		return nil

	default:
		return nil
	}
}
