package agent

// Session model management: switching models, creating providers,
// syncing reasoning levels. These methods handle the relationship between
// Session, ModelManager, and the LLM provider.
//
// The run() goroutine owns provider/agent creation (via SwitchModel,
// model_set command) and all ModelManager/RuntimeManager access. The task
// goroutine reads agent and provider via atomic pointers. Cross-goroutine
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

func (s *Session) initModelManager() {
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

// initToolConfirmSet builds the tool confirmation lookup set from config.
// If ToolConfirmTools is empty, toolConfirmSet stays nil and no tools
// require confirmation.
func (s *Session) initToolConfirmSet(tools []string) {
	if len(tools) == 0 {
		return
	}
	s.toolConfirmSet = make(map[string]struct{}, len(tools))
	for _, name := range tools {
		s.toolConfirmSet[name] = struct{}{}
	}
}

// applyModelOverride applies the --model CLI flag override.
// If overrideActiveModel is set and a model with that name exists in the
// model config, it becomes the active model. If the name doesn't match
// any configured model, an error is stored so the caller can report it and exit.
func (s *Session) applyModelOverride() {
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

func (s *Session) ensureAgentInitialized() string {
	// Fast path: already initialized.
	if s.agent.Load() != nil {
		if s.provider.Load() != nil {
			// provider is set — agent is ready
			return ""
		}
	}

	if s.ModelManager == nil {
		return "Model manager not initialized"
	}
	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		return "No model configured. Please add a model to ~/.alayacore/model.conf"
	}

	provider, agent, err := s.createProviderAndAgent(activeModel)
	if err != nil {
		return "Failed to create provider: " + err.Error()
	}

	s.agent.Store(agent)
	s.provider.Store(&provider)
	if activeModel.ContextLimit > 0 {
		s.ContextLimit = int64(activeModel.ContextLimit)
	}
	if p := s.provider.Load(); p != nil {
		(*p).SetReasoningLevel(int(s.reasoningLevel.Load()))
	}
	return ""
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, agent, err := s.createProviderAndAgent(modelConfig)
	if err != nil {
		return err
	}

	s.agent.Store(agent)
	s.provider.Store(&provider)
	if p := s.provider.Load(); p != nil {
		(*p).SetReasoningLevel(int(s.reasoningLevel.Load()))
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
// If a task is currently running, the provider is not synced immediately
// (to avoid races). Instead, reasoningDirty is set and the sync happens at
// the next step boundary in the task goroutine.
// See config.ReasoningLevelOff, config.ReasoningLevelNormal, config.ReasoningLevelMax.
func (s *Session) SetReasoningLevel(level int) {
	s.reasoningLevel.Store(int64(level))
	if s.inProgress {
		// Defer provider sync to next step boundary
		s.reasoningDirty.Store(true)
	} else {
		if p := s.provider.Load(); p != nil {
			(*p).SetReasoningLevel(level)
		}
	}
	s.sendSystemInfo("reasoning")
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
