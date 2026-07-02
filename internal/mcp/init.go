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
// Each server runs in its own goroutine. Non-OAuth servers go through
// connecting → connected/failed. OAuth servers go through:
// connecting → auth_confirm → (wait for user) → auth_running → connected/failed.
// All events are serialized through a channel so the adapter sees a
// linear sequence.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	// Set by an OAuth server goroutine before blocking on user input,
	// read by Confirm() to route the user's decision.
	confirmChs map[string]chan bool

	authTools  map[string][]Tool // tools collected from OAuth servers
	authErrors []string

	mu           sync.Mutex // guards confirmChs, authTools, authErrors, eventsClosed
	eventsClosed bool       // set before closing events channel

	cancel context.CancelFunc // set by Start(), cancels the init context
}

// NewInit creates an Init from server configurations.
// Call Start() to begin initialization.
func NewInit(configs []ServerConfig) *Init {
	return &Init{
		manager:    NewManager(configs),
		configs:    configs,
		events:     make(chan InitEvent, 64),
		done:       make(chan struct{}),
		confirmChs: make(map[string]chan bool),
		authTools:  make(map[string][]Tool),
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
// Internal: run() — launches one goroutine per server
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
	wg.Wait()

	if ctx.Err() != nil {
		return
	}

	init.discoverPhase(ctx)
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

	init.sendEvent(InitEvent{Type: "connected", Server: c.Name()})
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
			init.mu.Lock()
			init.authErrors = append(init.authErrors, fmt.Sprintf("MCP auth for %q: %v", c.Name(), err))
			init.mu.Unlock()
			init.sendEvent(InitEvent{Type: "failed", Server: c.Name(), Error: err.Error()})
		}
		return
	}

	init.mu.Lock()
	init.authTools[c.Name()] = tools
	init.mu.Unlock()

	init.sendEvent(InitEvent{Type: "connected", Server: c.Name()})
}

// ============================================================================
// Phase 3: Discover tools + build system prompt
// ============================================================================

func (init *Init) discoverPhase(ctx context.Context) {
	init.sendEvent(InitEvent{Type: "discovering"})

	discoverCtx, discoverCancel := context.WithTimeout(ctx, 15*time.Second)
	defer discoverCancel()

	serverTools := init.manager.DiscoverTools(discoverCtx)

	init.mu.Lock()
	for name, tools := range init.authTools {
		serverTools[name] = tools
	}
	errs := append([]string(nil), init.authErrors...)
	init.mu.Unlock()

	var allTools []llm.Tool
	if len(serverTools) > 0 {
		allTools = append(allTools, ToolsToAgentTools(serverTools, init.manager)...)
	}
	allTools = append(allTools, ResourcesToAgentTools(init.manager.Clients(), init.manager)...)
	allTools = append(allTools, PromptsToAgentTools(init.manager.Clients(), init.manager)...)

	listCtx, listCancel := context.WithTimeout(ctx, 15*time.Second)
	defer listCancel()

	var frag strings.Builder
	for name, instr := range init.manager.ServerInstructions() {
		frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", name, instr))
	}
	if resCtx := buildResourcesContext(listCtx, init.manager); resCtx != "" {
		frag.WriteString(resCtx)
	}
	if promptCtx := buildPromptsContext(listCtx, init.manager); promptCtx != "" {
		frag.WriteString(promptCtx)
	}

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

// ============================================================================
// System prompt helpers
// ============================================================================

func buildResourcesContext(ctx context.Context, m *Manager) string {
	serverResources := m.DiscoverResources(ctx)
	if len(serverResources) == 0 {
		return ""
	}
	var b strings.Builder
	for serverName, resources := range serverResources {
		b.WriteString(fmt.Sprintf("\n\nAvailable resources from MCP server %q:", serverName))
		for _, r := range resources {
			b.WriteString(fmt.Sprintf("\n  - %s", r.URI))
			if r.Name != "" {
				b.WriteString(fmt.Sprintf(" (name: %q", r.Name))
				if r.Description != "" {
					b.WriteString(fmt.Sprintf(", description: %q", r.Description))
				}
				if r.MIMEType != "" {
					b.WriteString(fmt.Sprintf(", mimeType: %q", r.MIMEType))
				}
				b.WriteString(")")
			} else if r.Description != "" {
				b.WriteString(fmt.Sprintf(" (description: %q)", r.Description))
			}
		}
	}
	return b.String()
}

func buildPromptsContext(ctx context.Context, m *Manager) string {
	serverPrompts := m.DiscoverPrompts(ctx)
	if len(serverPrompts) == 0 {
		return ""
	}
	var b strings.Builder
	for serverName, prompts := range serverPrompts {
		b.WriteString(fmt.Sprintf("\n\nAvailable prompts from MCP server %q:", serverName))
		for _, p := range prompts {
			b.WriteString(fmt.Sprintf("\n  - %s", p.Name))
			if p.Description != "" {
				b.WriteString(fmt.Sprintf(" (description: %q)", p.Description))
			}
			if len(p.Arguments) > 0 {
				b.WriteString("\n    Arguments:")
				for _, a := range p.Arguments {
					required := ""
					if a.Required {
						required = " (required)"
					}
					b.WriteString(fmt.Sprintf("\n      - %s: %s%s", a.Name, a.Description, required))
				}
			}
		}
	}
	return b.String()
}
