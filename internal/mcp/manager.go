package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// Manager manages multiple MCP server connections.
// It provides a unified interface for tool discovery across all servers.
//
// CONCURRENCY: clients slice is stored in an atomic.Value and replaced
// atomically on CloseAll. All read paths snapshot the slice and then
// operate on that snapshot, avoiding TOCTOU races and eliminating
// the need for a mutex on the hot path.
type Manager struct {
	clients atomic.Value // stores []*Client or nil
	closed  atomic.Bool
}

// NewManager creates an MCP manager from server configurations.
// It does NOT connect to any servers — call ConnectAll to establish connections.
func NewManager(configs []ServerConfig) *Manager {
	m := &Manager{}
	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		clients = append(clients, NewClient(cfg))
	}
	m.clients.Store(clients)
	return m
}

// loadClients returns a snapshot of the clients slice.
func (m *Manager) loadClients() []*Client {
	v := m.clients.Load()
	if v == nil {
		return nil
	}
	clients, ok := v.([]*Client)
	if !ok {
		return nil
	}
	return clients
}

// ServerInstructions returns a map of server name → initialization
// instructions for all connected servers that provided them.
// These instructions can be injected into the system prompt to improve
// the LLM's understanding of available tools and resources.
func (m *Manager) ServerInstructions() map[string]string {
	result := make(map[string]string)
	for _, c := range m.loadClients() {
		if c.State() != StateReady {
			continue
		}
		if instr := c.Instructions(); instr != "" {
			result[c.Name()] = instr
		}
	}
	return result
}

// ConnectAll connects to all configured MCP servers and performs
// initialization. Servers that fail to connect do not prevent others
// from connecting.
//
// Returns a list of errors for failed connections so callers can
// display warnings without aborting. Callers should display progress
// information themselves.
func (m *Manager) ConnectAll(ctx context.Context) []error {
	var errs []error
	for _, c := range m.loadClients() {
		if err := c.Connect(ctx); err != nil {
			errs = append(errs, fmt.Errorf("mcp server %q: %w", c.Name(), err))
		}
	}
	return errs
}

// DiscoverTools collects all tools from all connected MCP servers.
// Returns a map keyed by server name for disambiguation.
// Failing servers are silently skipped; callers should log errors
// externally if needed.
func (m *Manager) DiscoverTools(ctx context.Context) map[string][]Tool {
	return discoverConcurrent(ctx, m.loadClients(),
		func(c *Client) bool { return c.HasTools() },
		func(ctx context.Context, c *Client) ([]Tool, error) { return c.ListTools(ctx) },
	)
}

// DiscoverResources collects all resources from all connected MCP servers.
// Returns a map keyed by server name for disambiguation.
// Failing servers are silently skipped.
func (m *Manager) DiscoverResources(ctx context.Context) map[string][]Resource {
	return discoverConcurrent(ctx, m.loadClients(),
		func(c *Client) bool { return c.HasResources() },
		func(ctx context.Context, c *Client) ([]Resource, error) { return c.ListResources(ctx) },
	)
}

// DiscoverPrompts collects all prompts from all connected MCP servers.
// Returns a map keyed by server name for disambiguation.
// Failing servers are silently skipped.
func (m *Manager) DiscoverPrompts(ctx context.Context) map[string][]Prompt {
	return discoverConcurrent(ctx, m.loadClients(),
		func(c *Client) bool { return c.HasPrompts() },
		func(ctx context.Context, c *Client) ([]Prompt, error) { return c.ListPrompts(ctx) },
	)
}

// discoverConcurrent is a generic helper that runs discovery across all
// clients concurrently. It only considers clients that are StateReady and
// pass the capability check. Each client is listed independently; failures
// are silently skipped.
func discoverConcurrent[T any](ctx context.Context, clients []*Client, hasCapability func(*Client) bool, list func(context.Context, *Client) ([]T, error)) map[string][]T {
	result := make(map[string][]T)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, c := range clients {
		if c.State() != StateReady || !hasCapability(c) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := list(ctx, c)
			if err != nil || len(items) == 0 {
				return
			}
			mu.Lock()
			result[c.Name()] = items
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

// CallTool invokes a tool on the specified server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments []byte) (*CallToolResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.CallTool(ctx, toolName, arguments)
}

// ReadResource reads a resource by URI from the specified server.
func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (*ReadResourceResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.ReadResource(ctx, uri)
}

// GetPrompt fetches a prompt by name from the specified server.
func (m *Manager) GetPrompt(ctx context.Context, serverName, name string, args map[string]string) (*GetPromptResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.GetPrompt(ctx, name, args)
}

// CloseAll shuts down all MCP client connections.
func (m *Manager) CloseAll() {
	if !m.closed.CompareAndSwap(false, true) {
		return
	}

	for _, c := range m.loadClients() {
		c.Close() // errors are intentionally discarded — nothing to report at shutdown
	}
	m.clients.Store([]*Client(nil))
}

// Clients returns a snapshot of all managed clients.
func (m *Manager) Clients() []*Client {
	clients := m.loadClients()
	if clients == nil {
		return nil
	}
	result := make([]*Client, len(clients))
	copy(result, clients)
	return result
}

// PendingAuthServer describes an MCP server waiting for interactive
// OAuth authorization (authorization_code flow).
type PendingAuthServer struct {
	Name      string
	ServerURL string
}

// PendingAuthServers returns servers configured with authorization_code
// that have not yet completed the OAuth flow or have no valid persisted
// token. Servers that have a token loaded from disk do not appear here.
func (m *Manager) PendingAuthServers() []PendingAuthServer {
	var pending []PendingAuthServer
	for _, c := range m.loadClients() {
		if c.config.Auth != nil && c.config.Auth.Type == AuthTypeAuthorizationCode && c.config.Auth.obtainedToken == nil {
			pending = append(pending, PendingAuthServer{
				Name:      c.Name(),
				ServerURL: c.config.URL,
			})
		}
	}
	return pending
}

// AuthorizeServer performs the interactive OAuth authorization code flow
// for a server that requires it.
//
// The ctx should have a timeout of at least 5 minutes for the interactive flow.
func (m *Manager) AuthorizeServer(ctx context.Context, name string) ([]Tool, error) {
	client := m.findClient(name)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", name)
	}

	cfg := client.config.Auth
	if cfg == nil || cfg.Type != AuthTypeAuthorizationCode {
		return nil, fmt.Errorf("mcp server %q does not use authorization_code auth", name)
	}

	// 1. Discover authorization server metadata and resolve client credentials.
	meta, clientID, err := m.resolveAuthConfig(ctx, cfg, client.config.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", name, err)
	}

	// Store discovered endpoint and client_id back into config so that
	// newAuthProvider (called during client.Connect below) can build a
	// proper RefreshConfig for automatic token refresh.
	cfg.TokenEndpoint = meta.TokenEndpoint
	cfg.ClientID = clientID

	// 2. Run the authorization code flow.
	oauthToken, oauthErr := auth.RunAuthCodeFlow(ctx, meta, &auth.AuthCodeConfig{
		ClientID:     clientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
		Resource:     client.config.URL,
	})
	if oauthErr != nil {
		return nil, fmt.Errorf("mcp server %q: auth code flow: %w", name, oauthErr)
	}

	// 3. Store the obtained token and persist to disk.
	if oauthToken.AccessToken == "" {
		return nil, fmt.Errorf("mcp server %q: OAuth returned empty access token", name)
	}
	cfg.obtainedToken = &auth.Token{
		AccessToken:   oauthToken.AccessToken,
		TokenType:     oauthToken.TokenType,
		RefreshToken:  oauthToken.RefreshToken,
		ExpiresAt:     oauthToken.ExpiresAt,
		Scopes:        oauthToken.Scopes,
		TokenEndpoint: meta.TokenEndpoint,
		ClientID:      clientID,
	}

	// Persist the token to disk so it survives restarts.
	if client.tokenStore != nil {
		_ = client.tokenStore.SaveToken(name, cfg.obtainedToken) // non-fatal
	}

	// Reset client state before reconnecting (first attempt returned ErrNeedsAuth).
	client.resetState()

	if connErr := client.Connect(ctx); connErr != nil {
		cfg.obtainedToken = nil
		return nil, fmt.Errorf("mcp server %q: connect after auth: %w", name, connErr)
	}

	// 4. Discover tools.
	if !client.HasTools() {
		return nil, nil
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: discover tools: %w", name, err)
	}

	return tools, nil
}

// resolveAuthConfig discovers authorization server metadata and resolves
// the OAuth client credentials for a server.
// For authorization_code, client_id always comes from the built-in registry.
func (m *Manager) resolveAuthConfig(ctx context.Context, cfg *AuthConfig, serverURL string) (*auth.ASMetadata, string, error) {
	// Discover authorization server metadata.
	meta, err := discoverASMetadata(ctx, cfg, serverURL)
	if err != nil {
		return nil, "", fmt.Errorf("discover AS: %w", err)
	}

	// Resolve client_id from built-in registry only.
	// Users should never need to configure auth-client-id for
	// authorization_code — known services ship with built-in
	// credentials, and unknown services should file an issue.
	clientID, defaultSecret, ok := auth.LookupDefaultClient(meta.Issuer)
	if !ok {
		return nil, "", fmt.Errorf("alayacore does not yet support OAuth for %s. "+
			"Please file an issue at https://github.com/alayacore/alayacore to request support", meta.Issuer)
	}
	if clientID == "" {
		return nil, "", fmt.Errorf("%s requires a different authentication method "+
			"(try auth-type: static with an API key)", meta.Issuer)
	}

	// Use default client_secret if user didn't provide one.
	if cfg.ClientSecret == "" && defaultSecret != "" {
		cfg.ClientSecret = defaultSecret
	}

	return meta, clientID, nil
}

// discoverASMetadata discovers the authorization server metadata for an
// MCP server. It follows the MCP OAuth discovery chain:
//  1. Try direct well-known discovery from the MCP server URL
//  2. Discover Protected Resource Metadata (from well-known or 401)
//  3. Extract authorization_servers from resource metadata
//  4. Try well-known discovery on each authorization server URL
func discoverASMetadata(ctx context.Context, authCfg *AuthConfig, serverURL string) (*auth.ASMetadata, error) {
	// Step 1: If token_endpoint is configured, derive issuer from it and try.
	if authCfg != nil && authCfg.TokenEndpoint != "" {
		issuer := deriveIssuer(authCfg.TokenEndpoint)
		if meta, err := auth.DiscoverASMetadata(ctx, issuer); err == nil {
			return meta, nil
		}
	}

	// Step 2: Try direct well-known discovery from the server URL.
	if meta, err := auth.DiscoverASMetadata(ctx, serverURL); err == nil {
		return meta, nil
	}

	// Step 3: Discover Protected Resource Metadata.
	prm, err := auth.DiscoverProtectedResource(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("discover OAuth for %s: %w", serverURL, err)
	}

	// Step 4: Try each authorization server.
	for _, asURL := range prm.AuthorizationServers {
		if meta, err := auth.DiscoverASMetadata(ctx, asURL); err == nil {
			return meta, nil
		}
	}

	return nil, fmt.Errorf("no authorization server metadata found for %s (discovered servers: %v)",
		serverURL, prm.AuthorizationServers)
}

// deriveIssuer attempts to extract the issuer URL from a token endpoint URL.
// e.g. "https://auth.example.com/token" → "https://auth.example.com"
func deriveIssuer(tokenEndpoint string) string {
	for i := len(tokenEndpoint) - 1; i >= 0; i-- {
		if tokenEndpoint[i] == '/' {
			return tokenEndpoint[:i]
		}
	}
	return tokenEndpoint
}

// findClient looks up a client by server name from a snapshot.
func (m *Manager) findClient(name string) *Client {
	for _, c := range m.loadClients() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
