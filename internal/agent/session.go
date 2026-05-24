package agent

// Session manages conversation state and task execution.
//
// ARCHITECTURE:
//   Session uses an actor model: the run() goroutine owns all mutable
//   state, and the task goroutine communicates state changes via typed
//   events on stateCh. All cross-goroutine communication is channel-based
//   (no sync.Mutex anywhere in this package).
//
//   Three goroutines:
//     1. inputPump — reads TLV frames from input, sends parsed messages
//        to the main loop. It has access only to taskCancelCh.
//     2. run() — main loop that owns task queue, messages, and system
//        info. Processes input messages and task events.
//     3. task goroutine — spawned by run() to execute each task. It
//        receives a snapshot of messages at task start and sends state
//        mutations (step progress, new messages, token counts) back to
//        run() via stateCh.
//
//   Cross-goroutine communication:
//     msgCh (inputMsg channel)  — inputPump → run()
//     stateCh (taskEvent)        — task → run()
//     taskCancelCh               — inputPump → task (cancellation)
//     taskDone                   — task → run() (completion signal)
//     infoUpdateCh               — task → run() (system-info refresh)
//
// Related files:
//   - session_types.go — type definitions (Task, SystemInfo, SessionConfig, etc.)
//   - session_event.go — taskEvent types for actor model communication
//   - session_model.go — model management, provider creation, think level
//   - session_task.go  — input processing, prompt execution, agent loop
//   - session_io.go    — command handling, summarize, save
//   - session_output.go — TLV write helpers, usage tracking, system info
//   - session_persist.go — session save/load, markdown format

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// Session manages conversation state and task execution.
type Session struct {
	Messages       []llm.Message // owned by run() goroutine; task goroutine sends updates via stateCh
	CreatedAt      time.Time
	TotalSpent     llm.Usage     // updated by run() from task events
	ContextTokens  atomic.Int64  // read by both goroutines (shouldAutoSummarize, sendSystemInfo)
	ContextLimit   int64         // immutable after construction
	ModelManager   *ModelManager // thread-safe (own RWMutex)
	RuntimeManager *RuntimeManager
	SkillsManager  *skills.Manager
	SessionConfig        // embedded — immutable config set once at construction
	initError      error // Set during construction if --model refers to a non-existent model

	// === State owned by run() goroutine, updated via task events ===
	agent       atomic.Pointer[llm.Agent]
	provider    atomic.Value // stores llm.Provider
	taskQueue   []QueueItem
	currentStep atomic.Int64
	thinkLevel  atomic.Int64
	thinkDirty  atomic.Bool // true if thinkLevel changed during task execution

	inProgress    bool    // set/cleared by run() goroutine only
	pausedOnError atomic.Bool // set by task goroutine via event

	nextPromptID uint64 // goroutine-local (task goroutine)
	nextQueueID  uint64 // goroutine-local (run() goroutine)

	// stateCh carries state mutations from the task goroutine to run().
	stateCh chan taskEvent

	// taskCancelCh is a buffered channel (capacity 1) used by inputPump to
	// signal cancellation of the currently running task. The task goroutine
	// listens on this channel and cancels its context when a signal arrives.
	taskCancelCh chan struct{}

	// taskDone is a buffered channel (capacity 1) that the task goroutine
	// sends on when it completes. The main loop receives on this to know
	// when to start the next task.
	taskDone chan struct{}

	// infoUpdateCh is a buffered channel (capacity 1) used by the task
	// goroutine to request a system-info update from the run() goroutine.
	// This centralizes all sendSystemInfo calls in one place.
	infoUpdateCh chan struct{}

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
		taskQueue:      make([]QueueItem, 0),
		stateCh:        make(chan taskEvent, 64),
		taskCancelCh:   make(chan struct{}, 1),
		taskDone:       make(chan struct{}, 1),
		infoUpdateCh:   make(chan struct{}, 1),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.thinkLevel.Store(int64(config.DefaultThinkLevel))
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
		taskQueue:      make([]QueueItem, 0),
		stateCh:        make(chan taskEvent, 64),
		taskCancelCh:   make(chan struct{}, 1),
		taskDone:       make(chan struct{}, 1),
		infoUpdateCh:   make(chan struct{}, 1),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.thinkLevel.Store(int64(data.ThinkLevel))
	s.ContextTokens.Store(data.ContextTokens)

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
