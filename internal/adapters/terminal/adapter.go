package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, session loading, and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

const defaultThemeName = "theme-dark"

// Adapter starts the TUI; use from main/app.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new Terminal adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the Terminal program. Returns exit code.
func (a *Adapter) Start() int {
	// Note: OAuth MCP authorization is handled via the :mcp_auth command
	// in the TUI, not synchronously before startup.

	// Create theme manager
	themeManager := NewThemeManager(a.Config.Cfg.ThemesFolder)

	terminalOutput := NewTerminalOutput(NewStyles(theme.DefaultTheme()))

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, inputWriter, err := app.StartSession(a.Config, terminalOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// If MCP is configured, start background goroutine to wait for async
	// initialization and forward results to the session.
	if a.Config.AsyncMCP != nil {
		go a.waitMCPInit(session, terminalOutput)
	}

	// The session's first sendSystemInfo("all") has already been written to
	// terminalOutput synchronously during StartSession. Read the active theme
	// from the cached session state (default to default theme if not set).
	activeThemeName := terminalOutput.SnapshotStatus().ActiveTheme
	if activeThemeName == "" {
		activeThemeName = defaultThemeName
	}
	theme := themeManager.LoadTheme(activeThemeName)
	styles := NewStyles(theme)

	// Update output with new styles
	terminalOutput.SetStyles(styles)

	// Create terminal with initial window size, theme, and theme manager
	t := NewTerminalWithTheme(terminalOutput, inputWriter, a.Config, initialWidth, initialHeight, theme, themeManager, activeThemeName)

	// Create and run the program.
	// Bubbletea automatically opens the real TTY when stdin is piped
	// (Unix: /dev/tty, Windows: CONIN$ + CONOUT$).
	p := tea.NewProgram(t)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running terminal UI: %v\n", err)
		return 1
	}

	return 0
}

// waitMCPInit waits for async MCP initialization to complete and forwards
// the results to the session. Runs in a background goroutine.
func (a *Adapter) waitMCPInit(session *agentpkg.Session, output *outputWriter) {
	<-a.Config.AsyncMCP.Done()
	tools, sysFrag, errs := a.Config.AsyncMCP.Result()

	mgr := a.Config.AsyncMCP.Manager()

	// Check if there are OAuth servers still pending.
	pendingOAuth := mgr.PendingAuthServers()

	// Send update to the session's run() goroutine.
	// If OAuth servers are pending (PendingOAuthCount > 0), the session
	// will keep mcpReady=false and reject user messages until the counter
	// reaches zero (each server is either authorized via :mcp_auth <name> yes
	// or skipped via :mcp_auth <name> no).
	session.MCPUpdateChan() <- agentpkg.MCPUpdateEvent{
		Tools:              tools,
		SystemPromptSuffix: sysFrag,
		Manager:            mgr,
		PendingOAuthCount:  int32(len(pendingOAuth)), //nolint:gosec // len(pendingOAuth) is small (<100)
	}

	// Log non-fatal errors (connection failures, etc.) to the output.
	for _, e := range errs {
		output.WriteError("%s", e)
	}

	// Add pending OAuth servers as confirm dialogs for the TUI.
	for _, ps := range pendingOAuth {
		output.SetMCPAuthPending(ps.Name, ps.ServerURL)
	}
}

// getTerminalSize returns the current terminal size, or defaults if not a TTY.
func getTerminalSize() (width, height int) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			return w, h
		}
	}
	return DefaultWidth, DefaultHeight
}
