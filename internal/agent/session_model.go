package agent

// Session model management: switching models, creating providers,
// syncing think levels. These methods handle the relationship between
// Session, ModelManager, and the LLM provider.
//
// These methods may be called from either the run() goroutine or the
// task goroutine. Shared state is protected by sync.Mutex on the
// Session struct. Simple flags (inProgress, pausedOnError) use
// atomic.Bool for lock-free access.

import (
	"fmt"
	"net/http"

	debugpkg "github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
)

// SwitchModel switches the session to use a new model.
func (s *Session) SwitchModel(modelConfig *ModelConfig) error {
	if err := s.initAgentFromConfig(modelConfig); err != nil {
		return err
	}
	s.applyModelContextLimit(modelConfig)
	s.sendSystemInfo()
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

// GetRuntimeManager returns the runtime manager for the session
func (s *Session) GetRuntimeManager() *RuntimeManager {
	return s.RuntimeManager
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agent != nil && s.provider != nil {
		return ""
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

	s.agent = agent
	s.provider = provider
	if activeModel.ContextLimit > 0 {
		s.ContextLimit = int64(activeModel.ContextLimit)
	}
	s.syncThinkToProvider(s.thinkLevel)
	return ""
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, agent, err := s.createProviderAndAgent(modelConfig)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.agent = agent
	s.provider = provider
	s.syncThinkToProvider(s.thinkLevel)
	s.mu.Unlock()

	return nil
}

func (s *Session) applyModelContextLimit(model *ModelConfig) {
	if model == nil || model.ContextLimit <= 0 {
		return
	}
	s.mu.Lock()
	s.ContextLimit = int64(model.ContextLimit)
	s.mu.Unlock()
}

// syncThinkToProvider propagates the session's thinking level to the
// current provider. Caller must hold s.mu.
func (s *Session) syncThinkToProvider(level int) {
	if s.provider != nil {
		s.provider.SetReasoningLevel(level)
	}
}

// SetThinkLevel sets the think level.
// If a task is currently running, the provider is not synced immediately
// (to avoid races). Instead, thinkDirty is set and the sync happens at
// the next step boundary in the task goroutine.
// inProgress is an atomic.Bool so it can be read without the mutex.
// See config.ThinkLevelOff, config.ThinkLevelNormal, config.ThinkLevelMax.
func (s *Session) SetThinkLevel(level int) {
	s.mu.Lock()
	s.thinkLevel = level
	if s.inProgress.Load() {
		// Defer provider sync to next step boundary
		s.thinkDirty = true
		s.mu.Unlock()
	} else {
		s.syncThinkToProvider(level)
		s.mu.Unlock()
	}

	s.sendSystemInfo()
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
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
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
