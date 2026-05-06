package terminal

// Entry point for the terminal UI adaptor.
// This file handles application startup, session loading, and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

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

	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput(NewStyles(DefaultTheme()))

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, _ := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:              inputStream,
		Output:             terminalOutput,
		SessionFile:        a.Config.Cfg.Session,
		ModelConfigPath:    a.Config.Cfg.ModelConfig,
		RuntimeConfigPath:  a.Config.Cfg.RuntimeConfig,
		BaseTools:          a.Config.AgentTools,
		SystemPrompt:       a.Config.SystemPrompt,
		ExtraSystemPrompt:  a.Config.ExtraSystemPrompt,
		MaxSteps:           a.Config.MaxSteps,
		DebugAPI:           a.Config.Cfg.DebugAPI,
		AutoSummarize:      a.Config.Cfg.AutoSummarize,
		NoCompact:          a.Config.Cfg.NoCompact,
		CompactKeepSteps:   a.Config.Cfg.CompactKeepSteps,
		CompactTruncateLen: a.Config.Cfg.CompactTruncateLen,
		ProxyURL:           a.Config.Cfg.Proxy,
		SkillsMgr:          a.Config.SkillsMgr,
	})

	// Load active theme from runtime.conf (default to default theme if not set)
	activeThemeName := session.GetRuntimeManager().GetActiveTheme()
	if activeThemeName == "" {
		activeThemeName = defaultThemeName
	}
	theme := themeManager.LoadTheme(activeThemeName)
	styles := NewStyles(theme)

	// Update output with new styles
	terminalOutput.SetStyles(styles)

	// Check if we have any models available.
	if !session.HasModels() {
		fmt.Fprint(os.Stderr, agentpkg.NoModelsErrorMessage(session.ModelConfigPath()))
		return 1
	}

	// Display config validation messages (unknown protocol_type, missing fields, etc.)
	if msgs := session.ModelManager.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			fmt.Fprintf(os.Stderr, "error: %s\n", m)
		}
	}

	// Create terminal with loaded session, initial window size, theme, and theme manager
	t := NewTerminalWithTheme(session, terminalOutput, inputStream, a.Config, initialWidth, initialHeight, theme, themeManager)

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
