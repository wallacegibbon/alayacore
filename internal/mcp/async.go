package mcp

// AsyncInit manages background MCP initialization so the UI can start
// immediately without waiting for potentially slow server connections
// (connect, tool discovery, resource/prompt listing).
//
// Usage:
//
//	ai := mcp.NewAsyncInit(configs)
//	ai.Start(ctx)          // launches background goroutine
//	// start UI immediately...
//	<-ai.Done()            // wait for completion
//	tools, frag, errs := ai.Result()
//
// Thread-safe: Result() is safe to call from any goroutine after Done().
// Start() is idempotent.
import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// AsyncProgress reports per-server connection progress during async init.
type AsyncProgress struct {
	Status         string // "connecting", "succeeded", "skipped", "failed"
	Server         string
	ConnectedCount int
	SkippedCount   int
	TotalCount     int
}

// AsyncInit manages background MCP initialization.
type AsyncInit struct {
	manager *Manager
	done    chan struct{}
	once    sync.Once

	mu sync.Mutex
	// Results populated after initialization completes.
	tools []llm.Tool
	// sysFrag is the system prompt fragment to append (instructions +
	// resources context + prompts context).
	sysFrag string
	errs    []string // non-fatal startup errors
	ready   bool

	// saved configs for reconnecting after OAuth auth
	configs []ServerConfig

	skipCh     chan struct{}      // closed to skip the current server during connect phase
	progressCh chan AsyncProgress // per-server progress events (buffered)
}

// NewAsyncInit creates a new AsyncInit from server configurations.
// Does not connect to any servers — call Start() to begin.
func NewAsyncInit(configs []ServerConfig) *AsyncInit {
	return &AsyncInit{
		manager:    NewManager(configs),
		done:       make(chan struct{}),
		configs:    configs,
		skipCh:     make(chan struct{}, 1),
		progressCh: make(chan AsyncProgress, len(configs)*2),
	}
}

// Start begins asynchronous MCP initialization in a background goroutine.
// Idempotent — subsequent calls are no-ops.
func (a *AsyncInit) Start(ctx context.Context) {
	a.once.Do(func() { go a.run(ctx) })
}

// Done returns a channel that is closed when initialization completes.
func (a *AsyncInit) Done() <-chan struct{} {
	return a.done
}

// Manager returns the underlying MCP manager.
// The manager is valid before Done() — it holds the client objects
// even before connections are established.
func (a *AsyncInit) Manager() *Manager {
	return a.manager
}

// Result returns the initialization results, blocking until done.
// Safe to call from any goroutine after Done().
func (a *AsyncInit) Result() (tools []llm.Tool, sysPromptFragment string, errs []string) {
	<-a.done
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tools, a.sysFrag, a.errs
}

// Configs returns the server configurations (for reconnecting after auth).
func (a *AsyncInit) Configs() []ServerConfig {
	return a.configs
}

// ProgressCh returns a channel that receives per-server connection progress.
// The channel is closed when init completes (after Done() is closed).
func (a *AsyncInit) ProgressCh() <-chan AsyncProgress {
	return a.progressCh
}

// SkipCurrent signals the connection loop to skip the current server
// and move to the next one. Has no effect if the connect phase has
// already completed (all servers connected or timed out).
func (a *AsyncInit) SkipCurrent() {
	select {
	case a.skipCh <- struct{}{}:
	default:
	}
}

// errSkipRequested is a sentinel error returned by connectWithSkip when
// the user requests skipping the current server.
var errSkipRequested = fmt.Errorf("skip requested")

// connectWithSkip connects a client, but allows the user to skip it
// via the skipCh channel. Returns errSkipRequested if skipped.
func (a *AsyncInit) connectWithSkip(ctx context.Context, c *Client) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Connect(ctx)
	}()
	select {
	case err := <-errCh:
		return err
	case <-a.skipCh:
		// Close the client to free resources, then report skipped.
		c.Close()
		return errSkipRequested
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *AsyncInit) run(ctx context.Context) {
	defer close(a.done)
	defer close(a.progressCh)

	// 1. Connect servers.
	//    Skip servers that truly need interactive OAuth authorization
	//    (no valid token on disk) — they'll be handled by the adapter's
	//    :mcp_auth <name> yes|no flow.
	//    Servers that already have a valid token (loaded from disk by
	//    needsPersistedAuth) are connected normally.
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	var connErrs []error
	var connectedCount, skippedCount int
	clients := a.manager.Clients()
	totalCount := len(clients)
	for _, c := range clients {
		if c.needsPersistedAuth() {
			continue // needs interactive OAuth — skip for now
		}
		a.progressCh <- AsyncProgress{
			Status: "connecting", Server: c.Name(),
			ConnectedCount: connectedCount, SkippedCount: skippedCount, TotalCount: totalCount,
		}
		// Try connecting, but allow user to skip this specific server
		// via SkipCurrent() (e.g., when it hangs).
		if err := a.connectWithSkip(connectCtx, c); err != nil {
			if err == errSkipRequested {
				skippedCount++
				a.progressCh <- AsyncProgress{
					Status: "skipped", Server: c.Name(),
					ConnectedCount: connectedCount, SkippedCount: skippedCount, TotalCount: totalCount,
				}
				connErrs = append(connErrs, fmt.Errorf("skipped server %q", c.Name()))
				continue
			}
			connErrs = append(connErrs, err)
			a.progressCh <- AsyncProgress{
				Status: "failed", Server: c.Name(),
				ConnectedCount: connectedCount, SkippedCount: skippedCount, TotalCount: totalCount,
			}
			continue
		}
		connectedCount++
		a.progressCh <- AsyncProgress{
			Status: "succeeded", Server: c.Name(),
			ConnectedCount: connectedCount, SkippedCount: skippedCount, TotalCount: totalCount,
		}
	}
	connectCancel()

	errs := make([]string, 0, len(connErrs))
	for _, cerr := range connErrs {
		errs = append(errs, fmt.Sprintf("MCP: %v", cerr))
	}

	// 2. Discover tools from connected servers.
	discoverCtx, discoverCancel := context.WithTimeout(ctx, 15*time.Second)
	serverTools := a.manager.DiscoverTools(discoverCtx)
	discoverCancel()

	var tools []llm.Tool
	if len(serverTools) > 0 {
		tools = append(tools, ToolsToAgentTools(serverTools, a.manager)...)
	}

	// Inject read_resource tools for servers that support Resources.
	resourceTools := ResourcesToAgentTools(a.manager.Clients(), a.manager)
	tools = append(tools, resourceTools...)

	// Inject get_prompt tools for servers that support Prompts.
	promptTools := PromptsToAgentTools(a.manager.Clients(), a.manager)
	tools = append(tools, promptTools...)

	// 3. Build system prompt fragments (instructions + resources + prompts).
	var frag strings.Builder

	for name, instr := range a.manager.ServerInstructions() {
		frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", name, instr))
	}

	// Pre-fetch resource and prompt lists.
	listCtx, listCancel := context.WithTimeout(ctx, 15*time.Second)
	defer listCancel()

	if resCtx := buildResourcesContext(listCtx, a.manager); resCtx != "" {
		frag.WriteString(resCtx)
	}
	if promptCtx := buildPromptsContext(listCtx, a.manager); promptCtx != "" {
		frag.WriteString(promptCtx)
	}

	a.mu.Lock()
	a.tools = tools
	a.sysFrag = frag.String()
	a.errs = errs
	a.ready = true
	a.mu.Unlock()
}

// buildResourcesContext fetches the resource list from all connected servers
// and returns a formatted string suitable for injection into the system prompt.
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

// buildPromptsContext fetches the prompt list from all connected servers
// and returns a formatted string suitable for injection into the system prompt.
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
