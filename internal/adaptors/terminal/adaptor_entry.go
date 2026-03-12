package terminal

// This file contains the outer TerminalAdaptor used by main/app to
// start the Bubble Tea TUI. It wires the session, TLV streams, and
// terminal program together, leaving the rest of the package focused
// on the Tea model and view logic.

import (
	"context"
	"fmt"
	"os"
	"time"

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

	// Wait briefly for initial system info from session.
	// Session will send TagSystem with HasModels, ModelConfigPath,
	// and ActiveModelConfig; the terminal UI relies on this.
	time.Sleep(100 * time.Millisecond)

	// Check if we have any models available.
	if !terminalOutput.HasModels() {
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
		os.Exit(1)
	}

	// If no CLI model was provided, switch to the active model from config.
	// This requires direct session access because we need to create
	// provider/model objects using proxy/debug settings that are only
	// available to the adaptor.
	if a.Config.Model == nil {
		if activeModel := terminalOutput.GetActiveModel(); activeModel != nil {
			provider, err := app.CreateProvider(activeModel.ProtocolType, activeModel.APIKey, activeModel.BaseURL, a.Config.Cfg.DebugAPI, a.Config.Cfg.Proxy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to create provider: %v\n\n", err)
				os.Exit(1)
			}

			newModel, err := provider.LanguageModel(context.Background(), activeModel.ModelName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to create language model: %v\n\n", err)
				os.Exit(1)
			}

			// Switch the session to the active model from config.
			// This direct call is necessary during initialization (before main loop starts).
			session.SwitchModel(newModel, activeModel.BaseURL, activeModel.ModelName, a.Config.AgentTools, a.Config.SystemPrompt)
		}
	}

	t := NewTerminal(session, terminalOutput, inputStream, a.sessionFile, a.Config)

	// Initialize model selector from outputWriter (which gets data from TagSystem).
	if models := terminalOutput.GetModels(); len(models) > 0 {
		t.modelSelector.LoadModels(models, terminalOutput.GetActiveModelID())
	}

	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	p.Run()
}
