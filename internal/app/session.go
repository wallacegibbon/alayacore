package app

// Shared session loading for adapters.
// Both terminal and plainio adapters follow the same bootstrap sequence:
// load session, validate init errors, print config warnings, check models,
// then start the session goroutine.

import (
	"fmt"
	"io"
	"os"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
)

// StartSession loads (or creates) a session, validates it, and starts the
// session goroutine. On success the returned session is ready to use.
//
// It handles:
//   - LoadOrNewSession
//   - InitError check (--model flag validation)
//   - Model load error messages (unknown protocol_type, missing fields)
//   - HasModels check (no usable models → fatal)
//   - session.Start()
//
// input is an optional pre-created input buffer. If nil, a new one is created.
// On success the write end is returned so the adapter can feed TLV messages
// to the session.
//
// Returns nil, nil, and an error if the session cannot be used.
func StartSession(cfg *Config, output io.Writer, input *stream.SliceBuffer) (*agentpkg.Session, io.WriteCloser, error) {
	if input == nil {
		input = stream.NewSliceBuffer(100)
	}

	session, _, err := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:               input,
		Output:              output,
		SessionFile:         cfg.Cfg.Session,
		ModelConfigPath:     cfg.Cfg.ModelConfig,
		RuntimeConfigPath:   cfg.Cfg.RuntimeConfig,
		ThemesFolder:        cfg.Cfg.ThemesFolder,
		BaseTools:           cfg.AgentTools,
		SystemPrompt:        cfg.SystemPrompt,
		ExtraSystemPrompt:   cfg.ExtraSystemPrompt,
		MaxSteps:            cfg.MaxSteps,
		DebugAPI:            cfg.Cfg.DebugAPI,
		AutoSummarize:       cfg.Cfg.AutoSummarize,
		ProxyURL:            cfg.Cfg.Proxy,
		SkillsMgr:           cfg.SkillsMgr,
		OverrideActiveModel: cfg.Cfg.ModelName,
		ToolConfirmTools:    cfg.ToolConfirmTools,
		AsyncInit:           cfg.AsyncMCP,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load session: %w", err)
	}

	// --model CLI flag: fail immediately if the named model doesn't exist.
	if err := session.InitError(); err != nil {
		return nil, nil, err
	}

	// Display config validation messages (unknown protocol_type, missing fields, etc.)
	// Must come before HasModels() check so specific errors are shown even when
	// all models are rejected.
	if msgs := session.ModelManager.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			fmt.Fprintf(os.Stderr, "%s\n", m)
		}
	}

	// Display MCP startup errors through the adapter as system error messages.
	for _, e := range cfg.MCPStartupErrors {
		_ = stream.WriteSystemMsg(output, stream.ErrorMsg{Text: e})
	}

	// Check if we have any models available.
	if !session.HasModels() {
		return nil, nil, fmt.Errorf("%s", agentpkg.NoModelsErrorMessage(session.ModelConfigPath(), session.ModelManager.HasRejected()))
	}

	// Start the session's run() goroutine.
	session.Start()

	return session, input, nil
}
