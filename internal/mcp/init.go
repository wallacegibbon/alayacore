package mcp

// Init manages the entire MCP initialization lifecycle end-to-end.
//
// Usage:
//
//	init := mcp.NewInit(configs)
//	init.Start(ctx)
//	for evt := range init.Events() { … }
//	<-init.Done()
//
// The session drives the flow by:
//  1. Reading events from Events() channel
//  2. For "auth_confirm" events: showing a dialog, calling init.Confirm(name, bool)
//  3. For Ctrl+G: calling init.Cancel()
//  4. For "done"/"canceled" event: applying final results or cleaning up
//
// Each server runs in its own goroutine. After connecting, each server
// discovers tools, resources, and prompts before sending "connected".
// This means "connected" means the server is fully initialized and ready.
//
// After all servers complete, run() collects results in the original
// config order (deterministic for provider caching) and sends "done".

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/llm"
)

// InitEvent covers everything that happens during MCP initialization.
// The session receives these from Events() and reacts accordingly.
type InitEvent struct {
	Type   string // "connecting"|"connected"|"failed"|"auth_confirm"|"auth_running"|"done"
	Server string
	URL    string // set for "auth_confirm"
	Error  string // set for "failed"

	// Set for "done" — fully converted results
	Tools       []llm.Tool
	SysFragment string
	Errors      []string
	Manager     *Manager
}

// Init orchestrates MCP initialization from start to finish.
// Thread-safe: all public methods are safe to call from any goroutine.
type Init struct {
	manager *Manager
	configs []ServerConfig

	events  chan InitEvent // session reads from this
	done    chan struct{}
	started sync.Once

	// Per-server confirmation channels for OAuth.
	confirmChs map[string]chan bool

	// Per-server results populated by goroutines after connection + discovery.
	serverTools      map[string][]Tool
	serverResources  map[string][]Resource
	serverPrompts    map[string][]Prompt
	serverInstrs     map[string]string
	serverErrors     []string

	mu           sync.Mutex // guards confirmChs, server*, eventsClosed
	eventsClosed bool

	cancel context.CancelFunc // set by Start(), cancels the init context
}

// NewInit creates an Init from server configurations.
// Call Start() to begin initialization.
func NewInit(configs []ServerConfig) *Init {
	return &Init{
		manager:          NewManager(configs),
		configs:          configs,
		events:           make(chan InitEvent, 64),
		done:             make(chan struct{}),
		confirmChs:       make(map[string]chan bool),
		serverTools:      make(map[string][]Tool),
		serverResources:  make(map[string][]Resource),
		serverPrompts:    make(map[string][]Prompt),
		serverInstrs:     make(map[string]string),
	}
}

// Events returns a channel of initialization events.
// The session must read from this channel until it's closed.
func (init *Init) Events() <-chan InitEvent { return init.events }

// Done returns a channel that's closed when initialization is complete.
func (init *Init) Done() <-chan struct{} { return init.done }

// Manager returns the underlying MCP Manager.
// Valid before Done() — it holds the client objects even before connections.
func (init *Init) Manager() *Manager { return init.manager }

// Start begins initialization in a background goroutine.
// Idempotent — subsequent calls are no-ops.
func (init *Init) Start(ctx context.Context) {
	init.started.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		init.cancel = cancel
		go init.run(runCtx)
	})
}

// Cancel aborts the entire initialization.
// Safe to call concurrently — cancels the init context, causing run()
// to exit cleanly. Idempotent.
func (init *Init) Cancel() {
	if init.cancel != nil {
		init.cancel()
	}
}

// Confirm responds to an "auth_confirm" event.
// The session calls this when the user decides yes/no for a server.
// Returns true if the response was accepted (init is still running
// and waiting for this server's confirmation).
func (init *Init) Confirm(server string, allow bool) bool {
	init.mu.Lock()
	ch, ok := init.confirmChs[server]
	init.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- allow:
		return true
	default:
		return false
	}
}

// ============================================================================
// Internal: run() — launches one goroutine per server, then collects results
// ============================================================================

func (init *Init) run(ctx context.Context) {
	defer close(init.done)
	defer func() {
		if ctx.Err() != nil {
			init.sendEvent(InitEvent{Type: "canceled"})
		}
		init.mu.Lock()
		init.eventsClosed = true
		close(init.events)
		init.mu.Unlock()
	}()

	// Launch one goroutine per server.
	var wg sync.WaitGroup
	for _, c := range init.manager.Clients() {
		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			init.runServer(ctx, client)
		}(c)
	}

	// Wait for all goroutines to finish, but don't block on shutdown.
	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-ctx.Done():
		return
	}

	if ctx.Err() != nil {
		return
	}

	// Collect results in config order for deterministic system prompt.
	init.collectResults(ctx)
}

// runServer handles the full lifecycle of a single server.
func (init *Init) runServer(ctx context.Context, c *Client) {
	if c.needsPersistedAuth() {
		init.runOAuthServer(ctx, c)
	} else {
		init.runStdServer(ctx, c)
	}
}

func (init *Init) runStdServer(ctx context.Context, c *Client) {
	init.sendEvent(InitEvent{Type: "connecting", Server: c.Name()})

	if err := c.Connect(ctx); err != nil {
		init.sendEvent(InitEvent{Type: "failed", Server: c.Name(), Error: err.Error()})
		return
	}

	// Discover capabilities after connection.
	init.discoverCapabilities(ctx, c)
}

func (init *Init) runOAuthServer(ctx context.Context, c *Client) {
	init.sendEvent(InitEvent{Type: "connecting", Server: c.Name()})
	init.sendEvent(InitEvent{Type: "auth_confirm", Server: c.Name(), URL: c.config.URL})

	// Register confirmation channel so Confirm() can route the user's decision.
	confirmCh := make(chan bool, 1)
	init.mu.Lock()
	init.confirmChs[c.Name()] = confirmCh
	init.mu.Unlock()

	var allow bool
	select {
	case allow = <-confirmCh:
	case <-ctx.Done():
		return
	}

	init.mu.Lock()
	delete(init.confirmChs, c.Name())
	init.mu.Unlock()

	if !allow {
		init.sendEvent(InitEvent{Type: "failed", Server: c.Name(), Error: "skipped"})
		return
	}

	init.sendEvent(InitEvent{Type: "auth_running", Server: c.Name()})

	sa := NewServerAuth(c)
	tools, err := sa.Run(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			init.sendEvent(InitEvent{Type: "failed", Server: c.Name(), Error: "skipped"})
		} else {
			init.addError(fmt.Sprintf("MCP auth for %q: %v", c.Name(), err))
			init.sendEvent(InitEvent{Type: "failed", Server: c.Name(), Error: err.Error()})
		}
		return
	}

	// Store tools from OAuth flow, then discover remaining capabilities.
	init.mu.Lock()
	init.serverTools[c.Name()] = tools
	init.mu.Unlock()

	init.discoverCapabilities(ctx, c)
}

// discoverCapabilities discovers tools (if not already done), resources,
// and prompts for a connected server, then sends "connected".
func (init *Init) discoverCapabilities(ctx context.Context, c *Client) {
	if c.HasTools() {
		if _, exists := init.serverTools[c.Name()]; !exists {
			if tools, err := c.ListTools(ctx); err != nil {
				init.addError(fmt.Sprintf("MCP tools for %q: %v", c.Name(), err))
			} else {
				init.mu.Lock()
				init.serverTools[c.Name()] = tools
				init.mu.Unlock()
			}
		}
	}
	if c.HasResources() {
		if resources, err := c.ListResources(ctx); err != nil {
			init.addError(fmt.Sprintf("MCP resources for %q: %v", c.Name(), err))
		} else {
			init.mu.Lock()
			init.serverResources[c.Name()] = resources
			init.mu.Unlock()
		}
	}
	if c.HasPrompts() {
		if prompts, err := c.ListPrompts(ctx); err != nil {
			init.addError(fmt.Sprintf("MCP prompts for %q: %v", c.Name(), err))
		} else {
			init.mu.Lock()
			init.serverPrompts[c.Name()] = prompts
			init.mu.Unlock()
		}
	}
	if instr := c.Instructions(); instr != "" {
		init.mu.Lock()
		init.serverInstrs[c.Name()] = instr
		init.mu.Unlock()
	}

	init.sendEvent(InitEvent{Type: "connected", Server: c.Name()})
}

// collectResults builds the final tools list and system prompt fragment
// in the original config order, then sends "done".
func (init *Init) collectResults(ctx context.Context) {
	init.mu.Lock()
	errs := append([]string(nil), init.serverErrors...)
	allTools := make([]llm.Tool, 0)

	// Collect tools from all servers in config order.
	var frag strings.Builder
	for _, cfg := range init.configs {
		name := cfg.Name

		// Tools (including OAuth-discovered ones).
		if tools, ok := init.serverTools[name]; ok && len(tools) > 0 {
			serverTools := ToolsToAgentTools(map[string][]Tool{name: tools}, init.manager)
			allTools = append(allTools, serverTools...)
		}

		// Resource read tool.
		if resources, ok := init.serverResources[name]; ok && len(resources) > 0 {
			tool := newReadResourceTool(name, init.manager)
			allTools = append(allTools, tool)

			frag.WriteString(fmt.Sprintf("\n\nAvailable resources from MCP server %q:", name))
			for _, r := range resources {
				frag.WriteString(fmt.Sprintf("\n  - %s", r.URI))
				if r.Name != "" {
					frag.WriteString(fmt.Sprintf(" (name: %q", r.Name))
					if r.Description != "" {
						frag.WriteString(fmt.Sprintf(", description: %q", r.Description))
					}
					if r.MIMEType != "" {
						frag.WriteString(fmt.Sprintf(", mimeType: %q", r.MIMEType))
					}
					frag.WriteString(")")
				} else if r.Description != "" {
					frag.WriteString(fmt.Sprintf(" (description: %q)", r.Description))
				}
			}
		}

		// Prompt get tool.
		if prompts, ok := init.serverPrompts[name]; ok && len(prompts) > 0 {
			tool := newGetPromptTool(name, init.manager)
			allTools = append(allTools, tool)

			frag.WriteString(fmt.Sprintf("\n\nAvailable prompts from MCP server %q:", name))
			for _, p := range prompts {
				frag.WriteString(fmt.Sprintf("\n  - %s", p.Name))
				if p.Description != "" {
					frag.WriteString(fmt.Sprintf(" (description: %q)", p.Description))
				}
				if len(p.Arguments) > 0 {
					frag.WriteString("\n    Arguments:")
					for _, a := range p.Arguments {
						required := ""
						if a.Required {
							required = " (required)"
						}
						frag.WriteString(fmt.Sprintf("\n      - %s: %s%s", a.Name, a.Description, required))
					}
				}
			}
		}

		// Instructions.
		if instr, ok := init.serverInstrs[name]; ok {
			frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", name, instr))
		}
	}
	init.mu.Unlock()

	init.sendEvent(InitEvent{
		Type:        "done",
		Tools:       allTools,
		SysFragment: frag.String(),
		Errors:      errs,
		Manager:     init.manager,
	})
}

// ============================================================================
// Helpers
// ============================================================================

func (init *Init) addError(err string) {
	init.mu.Lock()
	init.serverErrors = append(init.serverErrors, err)
	init.mu.Unlock()
}

func (init *Init) sendEvent(evt InitEvent) {
	init.mu.Lock()
	defer init.mu.Unlock()

	if init.eventsClosed {
		return
	}
	select {
	case init.events <- evt:
	default:
	}
}
