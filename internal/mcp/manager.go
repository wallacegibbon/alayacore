package mcp

import (
	"context"
	"fmt"
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
		c.Close()
	}
	m.clients.Store([]*Client(nil))
}

// ActiveServerCount returns the number of connected (StateReady) servers.
func (m *Manager) ActiveServerCount() int {
	count := 0
	for _, c := range m.loadClients() {
		if c.State() == StateReady {
			count++
		}
	}
	return count
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

// resolveAuthConfig discovers authorization server metadata and resolves
// the OAuth client credentials for a server.
// client_id and client_secret must be configured by the user in mcp.conf
// via auth-client-id and auth-client-secret.
func resolveAuthConfig(ctx context.Context, cfg *AuthConfig, serverURL string) (*auth.ASMetadata, string, error) {
	// Discover authorization server metadata.
	meta, err := discoverASMetadata(ctx, cfg, serverURL)
	if err != nil {
		return nil, "", fmt.Errorf("discover AS: %w", err)
	}

	// Require user-provided client_id for authorization_code flow.
	if cfg.ClientID == "" {
		return nil, "", fmt.Errorf("%s requires auth-client-id in mcp.conf. "+
			"Register an OAuth app with the service and set auth-client-id and "+
			"auth-client-secret (if needed). See docs/oauth.md for details", meta.Issuer)
	}

	return meta, cfg.ClientID, nil
}

// discoverASMetadata discovers the authorization server metadata for an
// MCP server. It follows the MCP OAuth discovery chain:
//  1. If token_endpoint is configured, derive issuer from it and try.
//  2. Try direct well-known discovery from the MCP server URL.
//  3. Discover Protected Resource Metadata (from well-known or 401).
//  4. Extract authorization_servers from resource metadata.
//  5. Try well-known discovery on each authorization server URL.
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
