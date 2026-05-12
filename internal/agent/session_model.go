package agent

// Session model management: switching models, creating providers,
// syncing think levels. These methods handle the relationship between
// Session, ModelManager, and the LLM provider.

import (
	"fmt"
	"net/http"

	debugpkg "github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
)

// SwitchModel switches the session to use a new model.
//
// DEADLOCK GOTCHA: Don't hold mutex while calling methods that may need the same mutex.
// Pattern: lock → update fields → unlock → call methods.
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
	// First check (fast path) — hold lock briefly.
	s.mu.Lock()
	if s.Agent != nil && s.Provider != nil {
		s.mu.Unlock()
		return ""
	}
	if s.ModelManager == nil {
		s.mu.Unlock()
		return "Model manager not initialized"
	}
	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		s.mu.Unlock()
		return "No model configured. Please add a model to ~/.alayacore/model.conf"
	}
	// Copy the config so we can release the lock during construction.
	modelCopy := *activeModel
	s.mu.Unlock()

	// Slow operation (HTTP client creation) — no lock held.
	provider, agent, err := s.createProviderAndAgent(&modelCopy)
	if err != nil {
		return "Failed to create provider: " + err.Error()
	}

	// Second check — another goroutine may have initialized while we were unlocked.
	s.mu.Lock()
	if s.Agent != nil && s.Provider != nil {
		s.mu.Unlock()
		return ""
	}
	s.Agent = agent
	s.Provider = provider
	if modelCopy.ContextLimit > 0 {
		s.ContextLimit = int64(modelCopy.ContextLimit)
	}
	s.syncThinkToProvider()
	s.mu.Unlock()
	return ""
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, agent, err := s.createProviderAndAgent(modelConfig)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.Agent = agent
	s.Provider = provider
	s.mu.Unlock()

	s.syncThinkToProvider()
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
// current provider. Must be called after Provider is set.
func (s *Session) syncThinkToProvider() {
	if s.Provider != nil {
		s.Provider.SetReasoningLevel(s.thinkLevel)
	}
}

// SetThinkLevel sets the think level.
// See config.ThinkLevelOff, config.ThinkLevelNormal, config.ThinkLevelMax.
func (s *Session) SetThinkLevel(level int) {
	s.mu.Lock()
	s.thinkLevel = level
	s.mu.Unlock()

	s.syncThinkToProvider()

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
		Type:        config.ProtocolType,
		APIKey:      config.APIKey,
		BaseURL:     config.BaseURL,
		Model:       config.ModelName,
		HTTPClient:  client,
		PromptCache: config.PromptCache,
		MaxTokens:   config.MaxTokens,
	})
}
