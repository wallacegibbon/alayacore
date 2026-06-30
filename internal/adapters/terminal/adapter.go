package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, asynchronous session loading,
// and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
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
//
// The TUI starts immediately on slow machines, showing a loading spinner
// while the session is loaded asynchronously. This avoids the long
// "blank terminal" delay between pressing Enter and seeing the TUI.
func (a *Adapter) Start() int {
	// Note: OAuth MCP authorization is handled via the :mcp_auth command
	// in the TUI, not synchronously before startup.

	// Create theme manager
	themeManager := NewThemeManager(a.Config.Cfg.ThemesFolder)

	terminalOutput := NewTerminalOutput(NewStyles(theme.DefaultTheme()))

	// Get terminal size before creating the TUI (so we can size the loading screen)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Create the input buffer BEFORE starting the TUI. The TUI gets the
	// write end (streamInput) immediately; the session will read from
	// the same buffer once loading completes.
	inputBuffer := stream.NewSliceBuffer(100)

	// Create Terminal model in loading mode. The theme/styles here are for
	// the loading screen only — the session's actual theme is applied
	// after async loading completes.
	activeThemeName := defaultThemeName
	theme := themeManager.LoadTheme(activeThemeName)
	styles := NewStyles(theme)
	terminalOutput.SetStyles(styles)

	t := NewTerminalWithTheme(
		terminalOutput, inputBuffer, a.Config,
		initialWidth, initialHeight,
		theme, themeManager, activeThemeName,
	)
	t.loading = true // enter async loading mode

	// Create and run the program.
	// Bubbletea automatically opens the real TTY when stdin is piped
	// (Unix: /dev/tty, Windows: CONIN$ + CONOUT$).
	p := tea.NewProgram(t)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running terminal UI: %v\n", err)
		return 1
	}

	// If the session failed to load asynchronously, report the error now
	// that the terminal is back in cooked mode.
	if term, ok := finalModel.(*Terminal); ok && term.loadingError != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", term.loadingError)
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
