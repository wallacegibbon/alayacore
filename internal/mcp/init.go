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
// Results are collected via a channel. After all servers complete,
// run() builds the final tools list and system prompt in the original
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
type InitEventType string

const (
	InitConnecting  InitEventType = "connecting"
	InitConnected   InitEventType = "connected"
	InitFailed      InitEventType = "failed"
	InitAuthConfirm InitEventType = "auth_confirm"
	InitAuthRunning InitEventType = "auth_running"
	InitDone        InitEventType = "done"
	InitCanceled    InitEventType = "canceled"
)

type InitEvent struct {
	Type   InitEventType
	Server string
	URL    string // set for "auth_confirm"
	Error  string // set for "failed"

	// Set for "done" — fully converted results
	Tools       []llm.Tool
	SysFragment string
	Errors      []string
	Manager     *Manager
}

// serverResult holds all discovery data for one server.
type serverResult struct {
	name      string
	tools     []Tool
	resources []Resource
	prompts   []Prompt
	instrs    string
	errs      []string
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

	mu           sync.Mutex // guards confirmChs and eventsClosed
	eventsClosed bool

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
// Internal: run() — launches per-server goroutines, collects results via channel
// ============================================================================

func (init *Init) run(ctx context.Context) {
	defer close(init.done)
	defer func() {
		if ctx.Err() != nil {
			init.sendEvent(InitEvent{Type: InitCanceled})
		}
		init.mu.Lock()
		init.eventsClosed = true
		close(init.events)
		init.mu.Unlock()
	}()

	clients := init.manager.Clients()
	n := len(clients)
	resultCh := make(chan serverResult, n)

	for _, c := range clients {
		go func(client *Client) {
			resultCh <- init.collectServerResult(ctx, client)
		}(c)
	}

	// Collect all results, but don't block on shutdown.
	results := make(map[string]serverResult, n)
	for i := 0; i < n; i++ {
		select {
		case r := <-resultCh:
			results[r.name] = r
		case <-ctx.Done():
			return
		}
	}

	if ctx.Err() != nil {
		return
	}

	var evt InitEvent
	init.buildFinalResults(results, &evt)
	init.sendEvent(evt)
}

// collectServerResult handles the full lifecycle of a single server and
// returns its discovery results.
func (init *Init) collectServerResult(ctx context.Context, c *Client) serverResult {
	if c.needsPersistedAuth() {
		return init.collectOAuthResult(ctx, c)
	}
	return init.collectStdResult(ctx, c)
}

func (init *Init) collectStdResult(ctx context.Context, c *Client) serverResult {
	var r serverResult
	r.name = c.Name()

	init.sendEvent(InitEvent{Type: InitConnecting, Server: c.Name()})

	if err := c.Connect(ctx); err != nil {
		init.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		return r
	}

	init.discoverCapabilities(ctx, c, &r)
	init.sendEvent(InitEvent{Type: InitConnected, Server: c.Name()})
	return r
}

func (init *Init) collectOAuthResult(ctx context.Context, c *Client) serverResult {
	var r serverResult
	r.name = c.Name()

	init.sendEvent(InitEvent{Type: InitConnecting, Server: c.Name()})
	init.sendEvent(InitEvent{Type: InitAuthConfirm, Server: c.Name(), URL: c.config.URL})

	confirmCh := make(chan bool, 1)
	init.mu.Lock()
	init.confirmChs[c.Name()] = confirmCh
	init.mu.Unlock()

	var allow bool
	select {
	case allow = <-confirmCh:
	case <-ctx.Done():
		return r
	}

	init.mu.Lock()
	delete(init.confirmChs, c.Name())
	init.mu.Unlock()

	if !allow {
		init.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: "skipped"})
		return r
	}

	init.sendEvent(InitEvent{Type: InitAuthRunning, Server: c.Name()})

	sa := NewServerAuth(c)
	tools, err := sa.Run(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			init.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: "skipped"})
		} else {
			r.errs = append(r.errs, fmt.Sprintf("MCP auth for %q: %v", c.Name(), err))
			init.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		}
		return r
	}

	r.tools = tools
	init.discoverCapabilities(ctx, c, &r)
	init.sendEvent(InitEvent{Type: InitConnected, Server: c.Name()})
	return r
}

// discoverCapabilities discovers tools (if not already done), resources,
// and prompts for a connected server. Results are stored in the result
// struct directly — no shared state needed since each goroutine has its own.
func (init *Init) discoverCapabilities(ctx context.Context, c *Client, r *serverResult) {
	if c.HasTools() && len(r.tools) == 0 {
		if tools, err := c.ListTools(ctx); err != nil {
			r.errs = append(r.errs, fmt.Sprintf("MCP tools for %q: %v", c.Name(), err))
		} else {
			r.tools = tools
		}
	}
	if c.HasResources() {
		if resources, err := c.ListResources(ctx); err != nil {
			r.errs = append(r.errs, fmt.Sprintf("MCP resources for %q: %v", c.Name(), err))
		} else {
			r.resources = resources
		}
	}
	if c.HasPrompts() {
		if prompts, err := c.ListPrompts(ctx); err != nil {
			r.errs = append(r.errs, fmt.Sprintf("MCP prompts for %q: %v", c.Name(), err))
		} else {
			r.prompts = prompts
		}
	}
	if instr := c.Instructions(); instr != "" {
		r.instrs = instr
	}
}

// buildFinalResults builds the tools list, system prompt fragment,
// and error list in config order (deterministic for provider caching),
// then writes them into evt.
func (init *Init) buildFinalResults(results map[string]serverResult, evt *InitEvent) {
	var allTools []llm.Tool
	var allErrs []string
	var frag strings.Builder

	for _, cfg := range init.configs {
		r, ok := results[cfg.Name]
		if !ok {
			continue
		}
		allErrs = append(allErrs, r.errs...)

		if len(r.tools) > 0 {
			serverTools := ToolsToAgentTools(map[string][]Tool{cfg.Name: r.tools}, init.manager)
			allTools = append(allTools, serverTools...)
		}

		if len(r.resources) > 0 {
			allTools = append(allTools, newReadResourceTool(cfg.Name, init.manager))
			formatResourceContext(&frag, cfg.Name, r.resources)
		}

		if len(r.prompts) > 0 {
			allTools = append(allTools, newGetPromptTool(cfg.Name, init.manager))
			formatPromptContext(&frag, cfg.Name, r.prompts)
		}

		if r.instrs != "" {
			frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", cfg.Name, r.instrs))
		}
	}

	evt.Type = InitDone
	evt.Tools = allTools
	evt.SysFragment = frag.String()
	evt.Errors = allErrs
	evt.Manager = init.manager
}

func formatResourceContext(frag *strings.Builder, name string, resources []Resource) {
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

func formatPromptContext(frag *strings.Builder, name string, prompts []Prompt) {
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

// ============================================================================
// Helper
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
