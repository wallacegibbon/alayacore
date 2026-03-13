package terminal

// This file contains the outer TerminalAdaptor used by main/app to
// start the Bubble Tea TUI. It wires the session, TLV streams, and
// terminal program together, leaving the rest of the package focused
// on the Tea model and view logic.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// TerminalAdaptor starts the TUI; use from main/app.
type TerminalAdaptor struct {
	Config      *app.Config
	sessionFile string
}

// NewTerminalAdaptor creates a new Terminal adaptor.
func NewTerminalAdaptor(cfg *app.Config) *TerminalAdaptor {
	return &TerminalAdaptor{
		Config:      cfg,
		sessionFile: "",
	}
}

// SetSessionFile sets the session file path.
func (a *TerminalAdaptor) SetSessionFile(sessionFile string) {
	a.sessionFile = sessionFile
}

// Start runs the Terminal program.
func (a *TerminalAdaptor) Start() {
	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput()

	// Create terminal in loading state first
	t := NewLoadingTerminal(inputStream, terminalOutput, a.sessionFile, a.Config)

	// Create the program
	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))

	// Start async session loading with a reference to the program for quitting on error
	go a.loadSessionAsync(t, inputStream, terminalOutput, p)

	// Run the program (this blocks until quit)
	p.Run()
}

// loadSessionAsync loads the session in the background and sends a message when done.
// If there's an error (e.g., no models configured), it quits the program.
func (a *TerminalAdaptor) loadSessionAsync(t *Terminal, inputStream *stream.ChanInput, terminalOutput *outputWriter, p *tea.Program) {
	session, sessionFile := agentpkg.LoadOrNewSession(
		a.Config.Model,
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		"", // baseURL - loaded from config file
		"", // modelName - loaded from config file
		inputStream,
		terminalOutput,
		a.sessionFile,
		a.Config.Cfg.ContextLimit,
		a.Config.Cfg.ModelConfig,
		a.Config.Cfg.RuntimeConfig,
	)
	a.sessionFile = sessionFile

	// Check if we have any models available.
	if !terminalOutput.HasModels() {
		// Send error message to terminal and quit
		p.Send(sessionLoadedMsg{err: fmt.Errorf("no models configured")})
		// Also print to stderr for visibility
		modelPath := terminalOutput.GetModelConfigPath()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Error: No models configured.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please edit the model config file:")
		fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example format:")
		fmt.Fprintln(os.Stderr, `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768`)
		fmt.Fprintln(os.Stderr, "")
		p.Quit()
		return
	}

	// Get active model for later use
	var activeModel *agentpkg.ModelConfig
	if a.Config.Model == nil {
		activeModel = terminalOutput.GetActiveModel()
	}

	// Send session loaded message to the terminal
	p.Send(sessionLoadedMsg{
		session:       session,
		sessionFile:   sessionFile,
		activeModel:   activeModel,
		models:        terminalOutput.GetModels(),
		activeModelID: terminalOutput.GetActiveModelID(),
	})
}
