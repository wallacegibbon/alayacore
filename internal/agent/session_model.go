package agent

// Session model management: switching models, creating providers,
// syncing reasoning levels. These methods handle the relationship between
// Session, ModelManager, and the LLM provider.
//
// The run() goroutine owns provider/agent creation (via SwitchModel,
// model_set command) and all ModelManager/RuntimeManager access. Model
// switching is ScheduleIdle, so agent/provider are stable during a task;
// the task goroutine reads them as plain fields. Cross-goroutine
// communication is channel-based (see session_event.go).

import (
	"fmt"
	"net/http"

	debugpkg "github.com/alayacore/alayacore/internal/debug"
	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
)

// SwitchModel switches the session to use a new model.
func (s *Session) SwitchModel(modelConfig *ModelConfig) error {
	if err := s.initAgentFromConfig(modelConfig); err != nil {
		return err
	}
	s.applyModelContextLimit(modelConfig)
	s.sendSystemInfo("model")
	return nil
}

// setActiveFromRuntimeConfig sets the active model from runtime.conf.
// Falls back to the first available model if none is configured.
func (s *Session) setActiveFromRuntimeConfig() {
	if s.ModelManager == nil || s.RuntimeManager == nil {
		return
	}

	activeModelName := s.RuntimeManager.GetActiveModel()
	if activeModelName != "" {
		if err := s.ModelManager.SetActiveByName(activeModelName); err == nil {
			return
		}
	}
	s.ModelManager.SetActiveToFirst()
}

// setActiveFromSessionMeta restores the model saved in the session file's
// frontmatter, if one was set. This is a best-effort override — if the
// model was removed from config since the session was saved, the current
// active model is preserved.
func (s *Session) setActiveFromSessionMeta() {
	if s.sessionMetaModel == "" || s.ModelManager == nil {
		return
	}
	_ = s.ModelManager.SetActiveByName(s.sessionMetaModel) //nolint:errcheck // best-effort; model may no longer exist
}

// setActiveFromCliFlag applies the --model CLI flag override.
// If overrideActiveModel is set and a model with that name exists in the
// model config, it becomes the active model. If the name doesn't match
// any configured model, an error is stored so the caller can report it and exit.
func (s *Session) setActiveFromCliFlag() {
	if s.OverrideActiveModel == "" || s.ModelManager == nil {
		return
	}
	if err := s.ModelManager.SetActiveByName(s.OverrideActiveModel); err != nil {
		s.initError = err
	}
}

// InitError returns a non-nil error if session construction encountered a
// fatal problem (e.g. --model specified a non-existent model).
func (s *Session) InitError() error {
	return s.initError
}

// activeModelName returns the display name of the currently active model.
func (s *Session) activeModelName() string {
	if s.ModelManager == nil {
		return ""
	}
	if model := s.ModelManager.GetActive(); model != nil {
		return model.Name
	}
	return ""
}

// createProviderAndAgent creates a new provider and agent for the given model config.
// This is the single source of truth for provider/agent construction.
func (s *Session) createProviderAndAgent(modelConfig *ModelConfig) (llm.Provider, *llm.Agent, error) {
	provider, err := createProviderFromConfig(modelConfig, s.DebugAPI, s.ProxyURL)
	if err != nil {
		return nil, nil, err
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             s.BaseTools,
		SystemPrompt:      s.SystemPrompt,
		ExtraSystemPrompt: s.ExtraSystemPrompt,
		MaxSteps:          s.MaxSteps,
	})
	return provider, agent, nil
}

func (s *Session) ensureAgentInitialized() error {
	// Fast path: both agent and provider are ready.
	if s.agent != nil && s.provider != nil {
		return nil
	}

	if s.ModelManager == nil {
		return fmt.Errorf("model manager not initialized")
	}
	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		return fmt.Errorf("no model configured; please add a model to model.conf")
	}

	provider, agent, err := s.createProviderAndAgent(activeModel)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	s.agent = agent
	s.provider = provider
	if activeModel.ContextLimit > 0 {
		s.ContextLimit = int64(activeModel.ContextLimit)
	}
	if s.provider != nil {
		s.provider.SetReasoningLevel(s.reasoningLevel)
		s.provider.SetVideoConfig(s.videoFPS, s.videoRes)
	}
	return nil
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, agent, err := s.createProviderAndAgent(modelConfig)
	if err != nil {
		return err
	}

	s.agent = agent
	s.provider = provider
	if s.provider != nil {
		s.provider.SetReasoningLevel(s.reasoningLevel)
		s.provider.SetVideoConfig(s.videoFPS, s.videoRes)
	}
	return nil
}

func (s *Session) applyModelContextLimit(model *ModelConfig) {
	if model == nil || model.ContextLimit <= 0 {
		return
	}
	s.ContextLimit = int64(model.ContextLimit)
}

// SetReasoningLevel sets the reasoning level.
// :reason is ScheduleIdle so this is only called when no task is running.
// The provider is synced immediately.
func (s *Session) SetReasoningLevel(level int) {
	s.reasoningLevel = level
	if s.provider != nil {
		s.provider.SetReasoningLevel(level)
	}
	s.sendSystemInfo("reasoning")
}

// SetVideoConfig sets the default video FPS and resolution.
// :video_config is ScheduleIdle so this is only called when no task is running.
// The provider is synced immediately.
func (s *Session) SetVideoConfig(fps int, resolution int) {
	s.videoFPS = fps
	s.videoRes = resolution
	if s.provider != nil {
		s.provider.SetVideoConfig(fps, resolution)
	}
}

func createProviderFromConfig(config *ModelConfig, debugAPI bool, proxyURL string) (llm.Provider, error) {
	var client *http.Client
	var err error
	if proxyURL != "" {
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, domainerrors.Wrap("provider", fmt.Errorf("failed to create HTTP client with proxy: %w", err))
		}
	} else if debugAPI {
		client = debugpkg.NewHTTPClient()
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:       config.ProtocolType,
		APIKey:     config.APIKey,
		BaseURL:    config.BaseURL,
		Model:      config.ModelName,
		HTTPClient: client,
		MaxTokens:  config.MaxTokens,
	})
}
