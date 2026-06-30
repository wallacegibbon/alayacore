package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, session loading, and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

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
	_, inputWriter, err := app.StartSession(a.Config, terminalOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// If MCP is configured, start background goroutine to wait for async
	// initialization and set up TUI-specific UI state (OAuth confirm dialogs).
	// The session manages async init results internally.
	if a.Config.AsyncMCP != nil {
		go a.waitMCPInit(terminalOutput)
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

// waitMCPInit waits for async MCP initialization to complete and sets up
// TUI-specific UI state (OAuth confirm dialogs). Runs in a background goroutine.
// The session manages async init results internally — this function only
// handles TUI-side concerns.
func (a *Adapter) waitMCPInit(output *outputWriter) {
	<-a.Config.AsyncMCP.Done()

	// Log non-fatal errors (connection failures, etc.) to the output.
	_, _, errs := a.Config.AsyncMCP.Result()
	for _, e := range errs {
		output.WriteError("%s", e)
	}

	// Add pending OAuth servers as confirm dialogs for the TUI.
	mgr := a.Config.AsyncMCP.Manager()
	for _, ps := range mgr.PendingAuthServers() {
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
