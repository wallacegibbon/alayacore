package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

// findClient looks up a client by server name from a snapshot.
func (m *Manager) findClient(name string) *Client {
	for _, c := range m.loadClients() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
