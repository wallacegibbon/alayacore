package mcp

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// Manager manages multiple MCP server connections.
// It provides a unified interface for tool discovery across all servers.
type Manager struct {
	mu      sync.RWMutex
	clients []*Client
	closed  bool
}

// NewManager creates an MCP manager from server configurations.
// It does NOT connect to any servers — call ConnectAll to establish connections.
func NewManager(configs []ServerConfig) *Manager {
	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		clients = append(clients, NewClient(cfg))
	}
	return &Manager{clients: clients}
}

// ConnectAll connects to all configured MCP servers and performs
// initialization. Servers that fail to connect are logged but do not
// prevent others from connecting.
//
// Returns a list of errors for failed connections so callers can
// display warnings without aborting.
func (m *Manager) ConnectAll(ctx context.Context) []error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, c := range m.clients {
		log.Printf("MCP: connecting to %q...", c.Name())
		if err := c.Connect(ctx); err != nil {
			errs = append(errs, fmt.Errorf("mcp server %q: %w", c.Name(), err))
		} else {
			log.Printf("MCP: connected to %q (%s v%s)",
				c.Name(), c.ServerInfo().Name, c.ServerInfo().Version)
		}
	}
	return errs
}

// DiscoverTools collects all tools from all connected MCP servers.
// Returns a map keyed by server name for disambiguation.
// Errors are logged; failing servers are skipped.
func (m *Manager) DiscoverTools(ctx context.Context) map[string][]Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]Tool)
	for _, c := range m.clients {
		if c.State() != StateReady {
			log.Printf("MCP: skipping %q (state=%d)", c.Name(), c.State())
			continue
		}
		if !c.HasTools() {
			continue
		}
		tools, err := c.ListTools(ctx, false)
		if err != nil {
			log.Printf("MCP: failed to list tools from %q: %v", c.Name(), err)
			continue
		}
		if len(tools) > 0 {
			log.Printf("MCP: %q exposes %d tool(s)", c.Name(), len(tools))
			result[c.Name()] = tools
		}
	}
	return result
}

// CallTool invokes a tool on the specified server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments []byte) (*CallToolResult, error) {
	m.mu.RLock()
	client := m.findClient(serverName)
	m.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}

	return client.CallTool(ctx, toolName, arguments)
}

// CloseAll shuts down all MCP client connections.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return
	}
	m.closed = true

	for _, c := range m.clients {
		if err := c.Close(); err != nil {
			log.Printf("MCP: error closing %q: %v", c.Name(), err)
		}
	}
}

// Clients returns a snapshot of all managed clients.
func (m *Manager) Clients() []*Client {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := make([]*Client, len(m.clients))
	copy(clients, m.clients)
	return clients
}

// findClient looks up a client by server name.
func (m *Manager) findClient(name string) *Client {
	for _, c := range m.clients {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
