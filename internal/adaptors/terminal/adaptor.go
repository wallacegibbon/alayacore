package terminal

// Entry point for the terminal UI adaptor.
// This file handles application startup, session loading, and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
)

// Compile-time check: Adaptor satisfies app.Adaptor.
var _ app.Adaptor = (*Adaptor)(nil)

const defaultThemeName = "theme-dark"

// Adaptor starts the TUI; use from main/app.
type Adaptor struct {
	Config *app.Config
}

// NewAdaptor creates a new Terminal adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{Config: cfg}
}

// Start runs the Terminal program. Returns exit code.
func (a *Adaptor) Start() int {
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
