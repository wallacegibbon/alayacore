package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, asynchronous session loading,
// and error handling.

import (
	"fmt"
	"io"
	"os"

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
//
// The TUI starts immediately on slow machines, showing a loading spinner
// while the session is loaded asynchronously. This avoids the long
// "blank terminal" delay between pressing Enter and seeing the TUI.
func (a *Adapter) Start() int {
	// Note: OAuth MCP authorization is handled via the :mcp_confirm command
	// in the TUI, not synchronously before startup.

	// Prepare the themes folder: creates it with the built-in themes on first
	// run, and collects what it could not do for the loading screen to show.
	themeManager := theme.NewManager(a.Config.Cfg.ThemesFolder)

	terminalOutput := NewTerminalOutput(NewStyles(theme.DefaultTheme()))

	// Read before the TUI exists: the loading screen is sized from it.
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetScreenWidth(initialWidth)

	// Create the input pipe BEFORE starting the TUI. The TUI gets the
	// write end (pipeWriter) immediately; the session will read from
	// the read end (pipeReader) once loading completes.
	pipeReader, pipeWriter := io.Pipe()

	// Loading mode uses the embedded default theme — no file I/O for the brief
	// startup phase. The session loads the theme files once and broadcasts them;
	// the adapter caches that and applies it when loading completes.
	t := NewTerminal(Options{
		Out:          terminalOutput,
		Input:        pipeWriter,
		AppConfig:    a.Config,
		Width:        initialWidth,
		Height:       initialHeight,
		Theme:        theme.DefaultTheme(),
		ThemeManager: themeManager,
		ThemeName:    defaultThemeName,
	})
	t.loading = true // enter async loading mode
	t.pipeReader = pipeReader

	// Run opens the real TTY when stdin is piped (Unix: /dev/tty,
	// Windows: CONIN$ + CONOUT$).
	finalModel, err := Run(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running terminal UI: %v\n", err)
		return 1
	}

	// If the session failed to load asynchronously, report the error now
	// that the terminal is back in cooked mode.
	if term, ok := finalModel.(Terminal); ok && term.loadingError != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", term.loadingError)
		return 1
	}

	return 0
}

// getTerminalSize returns the current terminal size, or defaults if not a TTY.
//
// It is a pre-TTY guess and nothing more: this runs before Run opens the terminal,
// so it can only read the process's stdout — which on Windows may not be the
// handle the program ends up drawing on (openTTY falls back to CONOUT$ when stdout
// is not a console). The size the layout uses is the one Run applies before the
// first frame, measured on the screen the program draws on, and re-read on every
// tick afterwards (program.go → run, Program.refreshSize). So a piped stdout does
// not cost the loading screen a wrong first frame.
func getTerminalSize() (width, height int) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			return w, h
		}
	}
	return DefaultWidth, DefaultHeight
}
