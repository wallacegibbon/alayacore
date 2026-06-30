package rawio

// Package rawio provides a raw TLV stdin/stdout adapter for AlayaCore.
// It pipes raw bytes between stdin/stdout and the agent session -
// no parsing, no formatting, no interpretation.

import (
	"fmt"
	"io"
	"os"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter pipes raw bytes between stdin/stdout and the agent session.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new rawio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// forwardMCPInit waits for async MCP initialization and forwards results to the session.
// Runs in a background goroutine.
func (a *Adapter) forwardMCPInit(session *agentpkg.Session) {
	<-a.Config.AsyncMCP.Done()
	tools, sysFrag, _ := a.Config.AsyncMCP.Result()

	mgr := a.Config.AsyncMCP.Manager()
	pendingOAuth := mgr.PendingAuthServers()

	session.MCPUpdateChan() <- agentpkg.MCPUpdateEvent{
		Tools:              tools,
		SystemPromptSuffix: sysFrag,
		Manager:            mgr,
		PendingOAuthCount:  int32(len(pendingOAuth)), //nolint:gosec // len(pendingOAuth) is small (<100)
	}
}

// Start runs the rawio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on any error (startup or task failure).
// The controlling process reads stdout and handles TLV itself.
// Ctrl-C (SIGINT) terminates immediately with default signal handling.
func (a *Adapter) Start() int {
	session, inputWriter, err := app.StartSession(a.Config, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Forward async MCP initialization results to the session.
	if a.Config.AsyncMCP != nil {
		go a.forwardMCPInit(session)
	}

	// Pipe stdin to the session.
	go func() {
		_, _ = io.Copy(inputWriter, os.Stdin) // stdin EOF is normal termination
		inputWriter.Close()
	}()

	// Wait for the session to finish.
	<-session.Done()

	return 0
}
