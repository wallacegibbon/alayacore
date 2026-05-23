package agent

// Session manages conversation state and task execution.
//
// DEPENDENCY FIELDS (Agent, Provider):
//   These are read from the taskRunner goroutine (processPrompt, syncThinkToProvider)
//   and written from immediate-command handlers in the readFromInput goroutine
//   (SwitchModel via handleModelSet). They use atomic.Pointer so both reads and
//   writes are safe without holding s.mu.
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// providerHolder wraps llm.Provider so it can be stored in an atomic.Pointer.
// atomic.Pointer requires a concrete type; interfaces cannot be used directly.
type providerHolder struct {
	provider llm.Provider
}

// Session manages conversation state and task execution.
type Session struct {
	Messages       []llm.Message
	Agent          atomic.Pointer[llm.Agent]
	Provider       atomic.Pointer[providerHolder]
	CreatedAt      time.Time
	TotalSpent     llm.Usage
	ContextTokens  int64
	ContextLimit   int64
	ModelManager   *ModelManager
	RuntimeManager *RuntimeManager
	SkillsManager  *skills.Manager
	SessionConfig        // embedded — immutable config set once at construction
	thinkLevel     int   // mutable — changed by SetThinkLevel
	initError      error // Set during construction if --model refers to a non-existent model

	taskQueue     []QueueItem
	cond          *sync.Cond         // signals when taskQueue becomes non-empty or pausedOnError clears
	sessionCtx    context.Context    // canceled when input is exhausted
	sessionCancel context.CancelFunc // idempotent cancel
	runnerDone    chan struct{}      // closed when taskRunner exits
	inProgress    bool
	pausedOnError bool           // set when a provider/network error occurs; blocks taskRunner until user acts
	taskWg        sync.WaitGroup // tracks in-flight runTask
	cancelCurrent func()
	nextPromptID  uint64
	nextQueueID   uint64
	currentStep   int
	mu            sync.Mutex
}

// WaitDone blocks until the taskRunner has finished processing all queued tasks
// and exited. This should be called after closing the input.
func (s *Session) WaitDone() {
	<-s.runnerDone
}

// Done returns a channel that is closed when the taskRunner has exited.
// Useful in select statements to wait for session completion without a
// wrapper goroutine.
func (s *Session) Done() <-chan struct{} {
	return s.runnerDone
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
func LoadOrNewSession(cfg SessionConfig) (*Session, string) {
	cfg.SessionFile = config.ExpandPath(cfg.SessionFile)
	if cfg.SessionFile != "" {
		if data, err := LoadSession(cfg.SessionFile); err == nil {
			return RestoreFromSession(cfg, data), cfg.SessionFile
		}
	}
	return NewSession(cfg), cfg.SessionFile
}

// NewSession creates a fresh session.
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
		runnerDone:     make(chan struct{}),
	}
	s.initModelManager()
	s.applyModelOverride()
	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()
	return s
}

// RestoreFromSession creates a session from saved data.
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
		runnerDone:     make(chan struct{}),
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

	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()

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
