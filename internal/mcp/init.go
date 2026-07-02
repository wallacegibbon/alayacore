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
// All MCP init logic is encapsulated here — the session has no state machine.

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

	// Session drives these:
	confirmCh   chan confirmReq // session sends auth decisions here
	skipConnect chan struct{}   // session sends skip during connect phase (buffered=1)

	// OAuth goroutine tracking
	oauthWG    sync.WaitGroup
	running    map[string]context.CancelFunc // per-server OAuth cancel functions
	authTools  map[string][]Tool             // tools collected from OAuth servers
	authErrors []string

	mu           sync.Mutex
	eventsClosed bool // set before closing events channel

	cancel context.CancelFunc // set by Start(), cancels the init context
}

type confirmReq struct {
	Server string
	Allow  bool
}

// NewInit creates an Init from server configurations.
// Call Start() to begin initialization.
func NewInit(configs []ServerConfig) *Init {
	return &Init{
		manager:     NewManager(configs),
		configs:     configs,
		events:      make(chan InitEvent, 64),
		done:        make(chan struct{}),
		confirmCh:   make(chan confirmReq, 1),
		skipConnect: make(chan struct{}, 1),
		running:     make(map[string]context.CancelFunc),
		authTools:   make(map[string][]Tool),
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
// Returns false if no confirmation is pending (init already done
// or the buffer is full).
func (init *Init) Confirm(server string, allow bool) bool {
	select {
	case init.confirmCh <- confirmReq{Server: server, Allow: allow}:
		return true
	default:
		return false
	}
}

// SkipCurrent aborts the current operation:
//   - During connect phase: skips the current server being connected
//   - During OAuth confirm phase: no-op (user presses 'n' on a dialog to skip)
//   - During OAuth execution phase: cancels the first running OAuth server's context
//
// Safe to call concurrently.
func (init *Init) SkipCurrent() {
	// Phase 1 (connecting): abort current connection attempt.
	select {
	case init.skipConnect <- struct{}{}:
	default:
	}

	// Phase 2 (OAuth): cancel the first running server's context.
	init.mu.Lock()
	for name, cancel := range init.running {
		cancel()
		delete(init.running, name)
		init.mu.Unlock()
		return
	}
	init.mu.Unlock()
}

// ============================================================================
// Internal: run() — orchestrates the three phases
// ============================================================================

func (init *Init) run(ctx context.Context) {
	defer close(init.done)
	defer func() {
		// Send a terminal event before closing so the session knows
		// why the channel is being closed.
		if ctx.Err() != nil {
			init.sendEvent(InitEvent{Type: "canceled"})
		}
		init.mu.Lock()
		init.eventsClosed = true
		close(init.events)
		init.mu.Unlock()
	}()

	clients := init.manager.Clients()
	total := len(clients)

	connected, skipped := init.connectPhase(ctx, clients, total)
	init.oauthPhase(ctx, clients, connected, skipped, total)

	// Wait for all OAuth goroutines (best-effort, no grace period on cancel).
	waitDone := make(chan struct{})
	go func() { init.oauthWG.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-ctx.Done():
		return
	}

	init.mu.Lock()
	connected += len(init.authTools)
	init.mu.Unlock()

	init.discoverPhase(ctx, connected, skipped, total)
}

// ============================================================================
// Phase 1: Connect non-OAuth servers (sequential, with skip support)
// ============================================================================

func (init *Init) connectPhase(ctx context.Context, clients []*Client, total int) (int, int) {
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	var connected, skipped int

	for _, c := range clients {
		if c.needsPersistedAuth() {
			continue
		}
		init.sendEvent(InitEvent{
			Type: "connecting", Server: c.Name(),
		})
		if err := init.connectWithSkip(connectCtx, c); err != nil {
			if err == errSkipRequested {
				skipped++
			}
			init.sendEvent(InitEvent{
				Type: "failed", Server: c.Name(), Error: err.Error(),
			})
			continue
		}
		connected++
		init.sendEvent(InitEvent{
			Type: "connected", Server: c.Name(),
		})
	}
	return connected, skipped
}

// ============================================================================
// Phase 2: OAuth servers (parallel confirms, parallel auth execution)
// ============================================================================

func (init *Init) oauthPhase(ctx context.Context, clients []*Client, connected, skipped, total int) {
	// Phase 2a: Identify OAuth servers and send all auth_confirm events at once.
	type pendingServer struct {
		client *Client
	}
	pending := make(map[string]*pendingServer)
	for _, c := range clients {
		if !c.needsPersistedAuth() {
			continue
		}
		pending[c.Name()] = &pendingServer{client: c}
		init.sendEvent(InitEvent{
			Type: "connecting", Server: c.Name(),
		})
		init.sendEvent(InitEvent{
			Type: "auth_confirm", Server: c.Name(), URL: c.config.URL,
		})
	}

	if len(pending) == 0 {
		return
	}

	// Phase 2b: Collect confirm results as they arrive.
	// Adapter may show dialogs sequentially (TUI) or in parallel (GUI).
	for len(pending) > 0 {
		select {
		case req := <-init.confirmCh:
			ps, ok := pending[req.Server]
			if !ok {
				// Stale response for an already-resolved server.
				continue
			}
			delete(pending, req.Server)
			if req.Allow {
				init.launchOAuth(ctx, ps.client, connected, skipped, total)
			} else {
				skipped++
				init.sendEvent(InitEvent{
					Type: "failed", Server: req.Server, Error: "skipped",
				})
			}
		case <-ctx.Done():
			return
		}
	}
}

func (init *Init) launchOAuth(_ context.Context, c *Client, connected, skipped, total int) {
	init.oauthWG.Add(1)
	sa := NewServerAuth(c)
	authCtx, authCancel := context.WithCancel(context.Background())

	init.mu.Lock()
	init.running[c.Name()] = authCancel
	init.mu.Unlock()

	init.sendEvent(InitEvent{
		Type: "auth_running", Server: c.Name(),
	})

	go func(sa *ServerAuth, cancel context.CancelFunc) {
		defer init.oauthWG.Done()
		defer cancel()

		tools, err := sa.Run(authCtx)

		init.mu.Lock()
		delete(init.running, sa.Name())
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			// User pressed Ctrl+G — clean cancel.
			init.mu.Unlock()
			init.sendEvent(InitEvent{
				Type: "failed", Server: sa.Name(), Error: "skipped",
			})
		case err != nil:
			init.authErrors = append(init.authErrors, fmt.Sprintf("MCP auth for %q: %v", sa.Name(), err))
			init.mu.Unlock()
			init.sendEvent(InitEvent{
				Type: "failed", Server: sa.Name(), Error: err.Error(),
			})
		default:
			init.authTools[sa.Name()] = tools
			init.mu.Unlock()
			init.sendEvent(InitEvent{
				Type: "connected", Server: sa.Name(),
			})
		}
	}(sa, authCancel)
}

// ============================================================================
// Phase 3: Discover tools + build system prompt
// ============================================================================

func (init *Init) discoverPhase(ctx context.Context, connected, skipped, total int) {
	init.sendEvent(InitEvent{
		Type: "discovering",
	})

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

	// Check and send under the same lock to prevent a TOCTOU race:
	//   Without the lock, an OAuth goroutine could read eventsClosed=false,
	//   then the run() deferred cleanup sets eventsClosed=true and closes
	//   the channel, then the goroutine sends on a closed channel → panic.
	if init.eventsClosed {
		return
	}
	select {
	case init.events <- evt:
	default:
	}
}

// errSkipRequested is returned by connectWithSkip when the user skips.
var errSkipRequested = fmt.Errorf("skip requested")

func (init *Init) connectWithSkip(ctx context.Context, c *Client) error {
	errCh := make(chan error, 1)
	go func() { errCh <- c.Connect(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-init.skipConnect:
		c.Close()
		return errSkipRequested
	case <-ctx.Done():
		return ctx.Err()
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
