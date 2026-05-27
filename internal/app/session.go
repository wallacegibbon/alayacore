package app

// Shared session loading for adaptors.
// Both terminal and plainio adaptors follow the same bootstrap sequence:
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
// It creates the input pipe internally, returning the write end as
// inputWriter so the adaptor can feed TLV messages to the session.
//
// It handles:
//   - LoadOrNewSession
//   - InitError check (--model flag validation)
//   - Model load error messages (unknown protocol_type, missing fields)
//   - HasModels check (no usable models → fatal)
//   - session.Start()
//
// Returns nil, nil, and an error if the session cannot be used.
func StartSession(cfg *Config, output io.Writer) (*agentpkg.Session, io.WriteCloser, error) {
	input := stream.NewSliceBuffer(100)

	session, _ := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:               input,
		Output:              output,
		SessionFile:         cfg.Cfg.Session,
		ModelConfigPath:     cfg.Cfg.ModelConfig,
		RuntimeConfigPath:   cfg.Cfg.RuntimeConfig,
		BaseTools:           cfg.AgentTools,
		SystemPrompt:        cfg.SystemPrompt,
		ExtraSystemPrompt:   cfg.ExtraSystemPrompt,
		MaxSteps:            cfg.MaxSteps,
		DebugAPI:            cfg.Cfg.DebugAPI,
		AutoSummarize:       cfg.Cfg.AutoSummarize,
		ProxyURL:            cfg.Cfg.Proxy,
		SkillsMgr:           cfg.SkillsMgr,
		OverrideActiveModel: cfg.Cfg.ModelName,
	})

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

	// Check if we have any models available.
	if !session.HasModels() {
		return nil, nil, fmt.Errorf("%s", agentpkg.NoModelsErrorMessage(session.ModelConfigPath(), session.ModelManager.HasRejected()))
	}

	// Start the session's run() goroutine.
	session.Start()

	return session, input, nil
}
