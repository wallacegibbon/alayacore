package agent

// Session manages conversation state and task execution in a single goroutine.
//
// ARCHITECTURE:
//   Session runs a single goroutine (run()) that owns ALL mutable state.
//   Input is read via ChanInput.Channel() for non-blocking selects — no
//   separate I/O goroutine touches session state, so no mutexes, no atomic
//   pointers, and no condition variables are needed.
//
//   The only cross-goroutine communication is:
//     1. The input channel (ChanInput.Channel) — fed by adaptor, drained by run()
//     2. sessionCancel — cancels the run() goroutine from outside
//     3. cancelCurrent — a context.CancelFunc stored by run() and read by the
//        inputPump goroutine (inputPump ONLY calls it for :cancel commands,
//        never modifies session state)
//
// Related files:
//   - session_types.go — type definitions (Task, SystemInfo, SessionConfig, etc.)
//   - session_model.go — model management, provider creation, think level
//   - session_task.go  — input processing, prompt execution, agent loop
//   - session_io.go    — command handling, summarize, save
//   - session_output.go — TLV write helpers, usage tracking, system info
//   - session_persist.go — session save/load, markdown format

import (
	"context"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// Session manages conversation state and task execution.
type Session struct {
	Messages       []llm.Message
	CreatedAt      time.Time
	TotalSpent     llm.Usage
	ContextTokens  int64
	ContextLimit   int64
	ModelManager   *ModelManager
	RuntimeManager *RuntimeManager
	SkillsManager  *skills.Manager
	SessionConfig        // embedded — immutable config set once at construction
	initError      error // Set during construction if --model refers to a non-existent model

	// === Single-goroutine state (owned by run()) ===
	agent         *llm.Agent
	provider      llm.Provider
	taskQueue     []QueueItem
	inProgress    bool
	pausedOnError bool
	currentStep   int
	thinkLevel    int
	nextPromptID  uint64
	nextQueueID   uint64

	// cancelCurrent is set by run() before starting a task and cleared after.
	// It is read by inputPump (a separate goroutine) ONLY to call it for
	// :cancel / :cancel_all commands — never to modify session state.
	cancelCurrent context.CancelFunc

	sessionCtx    context.Context    // canceled when input is exhausted
	sessionCancel context.CancelFunc // idempotent cancel
	runDone       chan struct{}      // closed when run() exits
}

// WaitDone blocks until run() has finished processing all queued tasks
// and exited. This should be called after closing the input.
func (s *Session) WaitDone() {
	<-s.runDone
}

// Done returns a channel that is closed when run() has exited.
func (s *Session) Done() <-chan struct{} {
	return s.runDone
}

// HasModels returns true if any models are configured.
func (s *Session) HasModels() bool {
	if s.ModelManager == nil {
		return false
	}
	return s.ModelManager.HasModels()
}

// ModelConfigPath returns the path to the model config file.
func (s *Session) ModelConfigPath() string {
	if s.ModelManager == nil {
		return ""
	}
	return s.ModelManager.GetFilePath()
}

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
// The returned session is ready to use but NOT yet started —
// call Start() to begin processing input.
func LoadOrNewSession(cfg SessionConfig) (*Session, string) {
	cfg.SessionFile = config.ExpandPath(cfg.SessionFile)
	if cfg.SessionFile != "" {
		if data, err := LoadSession(cfg.SessionFile); err == nil {
			return RestoreFromSession(cfg, data), cfg.SessionFile
		}
	}
	return NewSession(cfg), cfg.SessionFile
}

// NewSession creates a fresh session. Does NOT start goroutines —
// call Start() to begin processing input.
func NewSession(cfg SessionConfig) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		CreatedAt:      time.Now(),
		ModelManager:   NewModelManager(cfg.ModelConfigPath),
		RuntimeManager: NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:  cfg.SkillsMgr,
		SessionConfig:  cfg,
		thinkLevel:     config.DefaultThinkLevel,
		taskQueue:      make([]QueueItem, 0),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.initModelManager()
	s.applyModelOverride()
	s.sendSystemInfo()
	return s
}

// RestoreFromSession creates a session from saved data.
// Does NOT start goroutines — call Start() to begin processing input.
func RestoreFromSession(cfg SessionConfig, data *SessionData) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		Messages:       data.Messages,
		CreatedAt:      data.CreatedAt,
		ModelManager:   NewModelManager(cfg.ModelConfigPath),
		RuntimeManager: NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:  cfg.SkillsMgr,
		SessionConfig:  cfg,
		thinkLevel:     data.ThinkLevel,
		ContextTokens:  data.ContextTokens,
		taskQueue:      make([]QueueItem, 0),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.initModelManager()

	// Override runtime config default with the model saved in the session file.
	// If the model was removed from config since the session was saved,
	// fall back to whatever initModelManager already set.
	if data.ActiveModel != "" {
		_ = s.ModelManager.SetActiveByName(data.ActiveModel) //nolint:errcheck // best-effort restore, fall back to initModelManager default
	}

	// --model CLI flag takes highest priority: override whatever was resolved above.
	s.applyModelOverride()

	// Apply context limit from the resolved model so the status bar
	// can show "tokens/limit (pct%)" immediately, before any API call.
	if model := s.ModelManager.GetActive(); model != nil {
		s.applyModelContextLimit(model)
	}

	s.sendSystemInfo()

	// Send TLV chunks directly to output (avoids reconstruction)
	for _, chunk := range data.TLVChunks {
		//nolint:errcheck // Best effort write, errors ignored
		_ = stream.WriteTLV(s.Output, chunk.Tag, chunk.Value)
	}
	if len(data.TLVChunks) > 0 {
		s.Output.Flush()
	}
	return s
}

// Start begins processing input in a single goroutine.
// Must be called exactly once after construction.
func (s *Session) Start() {
	go s.run()
}
