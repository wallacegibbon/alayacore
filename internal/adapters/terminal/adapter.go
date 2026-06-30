package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, session loading, and error handling.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/mcp"
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
	// Handle pending OAuth authorization servers before starting the TUI.
	a.handlePendingAuth()

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

// handlePendingAuth checks for MCP servers that require interactive OAuth
// authorization and handles them one by one with user prompts.
func (a *Adapter) handlePendingAuth() {
	mgr := a.Config.MCPManager
	if mgr == nil {
		return
	}

	pending := mgr.PendingAuthServers()
	if len(pending) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Some MCP servers require OAuth authorization:")

	reader := bufio.NewReader(os.Stdin)

	for _, s := range pending {
		fmt.Fprintf(os.Stderr, "\n  Server: %s (%s)\n", s.Name, s.ServerURL)
		fmt.Fprint(os.Stderr, "  Open browser to authorize? [Y/n]: ")

		line, _ := reader.ReadString('\n') // best-effort read
		line = strings.TrimSpace(line)

		if line == "n" || line == "no" {
			fmt.Fprintf(os.Stderr, "  Skipped %s.\n", s.Name)
			continue
		}

		fmt.Fprintf(os.Stderr, "  Authorizing %s...\n", s.Name)
		authCtx, authCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		tools, err := mgr.AuthorizeServer(authCtx, s.Name)
		authCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Authorization failed: %v\n", err)
			continue
		}

		if len(tools) == 0 {
			// Check if the server supports other features.
			for _, c := range mgr.Clients() {
				if c.Name() == s.Name {
					fmt.Fprintf(os.Stderr, "  ⚠ %s connected but no tools found.", s.Name)
					if c.HasResources() {
						fmt.Fprintf(os.Stderr, " Has resources.")
					}
					if c.HasPrompts() {
						fmt.Fprintf(os.Stderr, " Has prompts.")
					}
					fmt.Fprintf(os.Stderr, "\n")
					break
				}
			}
		} else {
			// Convert and add tools to the agent config.
			serverTools := map[string][]mcp.Tool{s.Name: tools}
			agentTools := mcp.ToolsToAgentTools(serverTools, mgr)
			a.Config.AgentTools = append(a.Config.AgentTools, agentTools...)
			fmt.Fprintf(os.Stderr, "  ✓ %s authorized and connected (%d tools).\n", s.Name, len(tools))
		}

		// Add server instructions to system prompt.
		for _, c := range mgr.Clients() {
			if c.Name() == s.Name {
				if instr := c.Instructions(); instr != "" {
					a.Config.SystemPrompt += fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", s.Name, instr)
				}
				break
			}
		}
	}

	fmt.Fprintln(os.Stderr, "")
}
