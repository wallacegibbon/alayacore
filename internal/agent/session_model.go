package agent

// Session model management: switching models, creating providers,
// syncing reasoning levels.
//
// These are thin wrappers over ModelService. The model service
// owns the ModelManager, RuntimeManager, provider/agent instances,
// and model resolution logic.

import (
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Model Switching
// ============================================================================

// SwitchModel switches the session to use a new model.
func (s *Session) SwitchModel(modelConfig *config.ModelConfig) error {
	if err := s.modelService.SwitchModel(modelConfig, s.BaseTools, s.SystemPrompt, s.ExtraSystemPrompt, s.MaxSteps); err != nil {
		return err
	}
	// Sync back context limit from the service.
	s.ContextLimit = s.modelService.ContextLimit()
	s.sendSystemInfo("model")
	return nil
}

// InitError returns a non-nil error if session construction encountered a
// fatal problem (e.g. --model specified a non-existent model).
func (s *Session) InitError() error {
	return s.modelService.InitError()
}

func (s *Session) activeModelName() string {
	if s.modelService == nil {
		return ""
	}
	return s.modelService.ActiveModelName()
}

func (s *Session) ensureAgentInitialized() error {
	return s.modelService.EnsureInitialized(s.BaseTools, s.SystemPrompt, s.ExtraSystemPrompt, s.MaxSteps)
}

// ============================================================================
// Settings
// ============================================================================

// SetReasoningLevel sets the reasoning level.
func (s *Session) SetReasoningLevel(level int) {
	s.modelService.SetReasoningLevel(level)
	s.sendSystemInfo("reasoning")
}

// SetVideoConfig sets the default video FPS and resolution.
func (s *Session) SetVideoConfig(fps int, resolution int) {
	s.modelService.SetVideoConfig(fps, resolution)
	s.sendSystemInfo("video_config")
}

// ============================================================================
// Accessors (for task goroutine)
// ============================================================================

// Agent returns the current LLM agent. Called from the task goroutine.
func (s *Session) Agent() *llm.Agent { return s.modelService.Agent() }
