package agent

// Session manages conversation state and task execution.
//
// ARCHITECTURE:
//   Session runs tasks in a separate goroutine so the main loop remains
//   responsive to user input at all times. The run() goroutine owns input
//   processing and task queue management. Tasks execute in their own
//   goroutine via runTask(). Shared state is protected by sync.Mutex.
//
//   Two goroutines for I/O:
//     1. inputPump — reads TLV frames from input, sends parsed messages
//        to the main loop. It has access only to taskCancelCh.
//     2. task goroutine — spawned by run() to execute each task.
//
//   Cross-goroutine communication:
//     1. msgCh (inputMsg channel) — fed by inputPump, drained by run()
//     2. taskCancelCh — inputPump sends to cancel the running task
//     3. taskDone — task goroutine sends on completion
//     4. mu (sync.Mutex) — protects all shared mutable state
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

// Session manages conversation state and task execution.
type Session struct {
	mu sync.Mutex // protects all mutable shared state between run() and task goroutines

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

	// === State shared between run() and task goroutines ===
	agent         *llm.Agent
	provider      llm.Provider
	taskQueue     []QueueItem
	inProgress    atomic.Bool
	pausedOnError atomic.Bool
	currentStep   int
	thinkLevel    int
	nextPromptID  uint64
	nextQueueID   uint64
	thinkDirty    bool // true if thinkLevel changed during task execution

	// taskCancelCh is a buffered channel (capacity 1) used by inputPump to
	// signal cancellation of the currently running task. The task goroutine
	// listens on this channel and cancels its context when a signal arrives.
	taskCancelCh chan struct{}

	// taskDone is a buffered channel (capacity 1) that the task goroutine
	// sends on when it completes. The main loop receives on this to know
	// when to start the next task.
	taskDone chan struct{}

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
		taskCancelCh:   make(chan struct{}, 1),
		taskDone:       make(chan struct{}, 1),
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
		taskCancelCh:   make(chan struct{}, 1),
		taskDone:       make(chan struct{}, 1),
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

// cancelRunningTask sends a cancellation signal to the running task.
// Non-blocking — if nobody is listening (no task running), the signal is lost.
// Returns true if the signal was sent (someone was listening).
func (s *Session) cancelRunningTask() bool {
	select {
	case s.taskCancelCh <- struct{}{}:
		return true
	default:
		return false
	}
}
