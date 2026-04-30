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
	Config       *app.Config
	ThemesFolder string
}

// NewAdaptor creates a new Terminal adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{
		Config: cfg,
	}
}

// NewAdaptorWithThemes creates a new Terminal adaptor with a custom themes folder.
func NewAdaptorWithThemes(cfg *app.Config, themesFolder string) *Adaptor {
	return &Adaptor{
		Config:       cfg,
		ThemesFolder: themesFolder,
	}
}

// Start runs the Terminal program.
func (a *Adaptor) Start() {
	// Create theme manager
	themeManager := NewThemeManager(a.ThemesFolder)

	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput(NewStyles(DefaultTheme()))

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, _ := agentpkg.LoadOrNewSession(
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		a.Config.ExtraSystemPrompt,
		a.Config.MaxSteps,
		inputStream,
		terminalOutput,
		a.Config.Cfg.Session,
		a.Config.Cfg.ModelConfig,
		a.Config.Cfg.RuntimeConfig,
		a.Config.Cfg.DebugAPI,
		a.Config.Cfg.AutoSummarize,
		a.Config.Cfg.AutoSave,
		a.Config.Cfg.NoCompact,
		a.Config.Cfg.CompactKeepSteps,
		a.Config.Cfg.CompactTruncateLen,
		a.Config.Cfg.Proxy,
		a.Config.SkillsMgr,
	)

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
	modelSnap := terminalOutput.SnapshotModels()
	if !modelSnap.HasModels {
		// Print error to stderr and exit
		modelPath := modelSnap.ConfigPath
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Error: No models configured.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please edit the model config file:")
		fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example format:")
		fmt.Fprintln(os.Stderr, `---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
---
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000`)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// Create terminal with loaded session, initial window size, theme, and theme manager
	t := NewTerminalWithTheme(session, terminalOutput, inputStream, a.Config, initialWidth, initialHeight, theme, themeManager)

	// Determine I/O sources for Bubble Tea.
	// When stdin/stdout are piped (not a TTY), open the terminal directly so
	// that piped data never leaks into the keyboard input and TUI output
	// always reaches the user's terminal.  This is the same pattern used by
	// vim, less, htop, etc.  tea.OpenTTY is cross-platform:
	//   Unix:   /dev/tty
	//   Windows: CONIN$ + CONOUT$
	ttyIn, ttyOut, err := tea.OpenTTY()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: could not open TTY:", err)
		os.Exit(1)
	}
	// NOTE: We intentionally do NOT close ttyIn/ttyOut.
	// Bubbletea restores the console state (raw mode, alt screen, etc.)
	// inside p.Run()'s shutdown sequence.  Closing the CONIN$/CONOUT$
	// handles (Windows) or /dev/tty fd (Unix) immediately after that can
	// race with the OS console subsystem still processing the final
	// escape sequences, which on Windows causes the parent shell's
	// prompt to not appear until the user presses a key.  The OS
	// reclaims these handles on process exit anyway.

	// Create and run the program
	p := tea.NewProgram(t, tea.WithInput(ttyIn), tea.WithOutput(ttyOut))
	_, _ = p.Run() //nolint:errcheck // terminal program run, error not critical
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
