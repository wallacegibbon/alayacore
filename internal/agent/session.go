package agent

// Session manages conversation state and task execution.
//
// ARCHITECTURE:
//   Session uses an actor model: the run() goroutine owns all mutable
//   state, and the task goroutine communicates state changes via typed
//   events on stateCh. All cross-goroutine communication is channel-based.
//
//   Three goroutines:
//     1. inputPump — reads TLV frames from input, sends parsed messages
//        to the main loop. It triggers cancellation via cancelRunningTask().
//     2. run() — main loop that owns Content, task queue, and system
//        info. Processes input messages and task events.
//     3. task goroutine — spawned by run() to execute each task. It
//        receives a snapshot of Messages (derived from Content) at task
//        start and sends state mutations (step progress, new ContentParts,
//        token counts) back to run() via stateCh.
//
//   Cross-goroutine communication:
//     msgCh (inputMsg channel)  — inputPump → run()
//     stateCh (taskEvent)        — task → run()
//     taskCancel                 — inputPump → task (cancellation via cancelRunningTask)
//     taskResult                 — task → run (TaskResult with messages + entries)
//     infoUpdateCh               — task → run() (system-info refresh)
//
// Related files:
//   - session_types.go — type definitions (Task, SessionConfig, etc.)
//   - session_event.go — TaskEvent types for actor model communication
//   - session_model.go — model management, provider creation, reasoning level
//   - session_task.go  — input processing, prompt execution, agent loop
//   - session_io.go    — command handling, summarize, save
//   - session_output.go — TLV write helpers, usage tracking, system info
//   - session_persist.go — session save/load, markdown format

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// Session manages conversation state and task execution.
type Session struct {
	Content        []llm.ContentPart // source of truth — flat, ordered, 1:1 with TLV
	Messages       []llm.Message     // derived from Content for API calls (rebuilt after each task)
	CreatedAt      time.Time
	ContextTokens  atomic.Int64 // read by both goroutines (shouldAutoSummarize, sendSystemInfo)
	ContextLimit   int64        // immutable after construction
	ModelManager   *ModelManager
	RuntimeManager *RuntimeManager
	SkillsManager  *skills.Manager
	SessionConfig        // embedded — immutable config set once at construction
	initError      error // Set during construction if --model refers to a non-existent model

	// sessionMetaModel is the model name stored in the session file's
	// active_model frontmatter. Set by RestoreFromSession; empty for
	// fresh sessions. Updated by handleModelSet whenever the user switches
	// models in a file-backed session (SessionFile != ""). Used by
	// setActiveFromSessionMeta() on reload to re-apply the session's model
	// preference.
	sessionMetaModel string

	// === State owned by run() goroutine, updated via task events ===
	agent          atomic.Pointer[llm.Agent]
	provider       atomic.Pointer[llm.Provider]
	taskQueue      []QueueItem
	currentStep    atomic.Int64
	reasoningLevel atomic.Int64
	reasoningDirty atomic.Bool // true if reasoningLevel changed during task execution

	inProgress    atomic.Bool // set/cleared by run() goroutine only; readable by input pump via cancelRunningTask
	pausedOnError atomic.Bool // set by task goroutine via event

	nextQueueID uint64 // goroutine-local (run() goroutine)

	// history ID generator — atomic counter, safe for concurrent access.
	histCounter atomic.Uint64

	// stateCh carries state mutations from the task goroutine to run().
	stateCh chan TaskEvent

	// taskResult carries the final state from the task goroutine
	// back to run() on completion. Buffered (capacity 1).
	taskResult chan TaskResult

	// confirmCh receives confirm responses from the input pump when the
	// async OnToolConfirm callback is used. Set by the callback, read by
	// handleConfirmCommand. No synchronization needed — the Output/Input
	// channel chain establishes happens-before.
	confirmCh chan<- llm.ToolConfirmResponse

	// toolConfirmSet contains tool names that require user confirmation
	// before execution. If nil, no confirmation is needed for any tool.
	toolConfirmSet map[string]struct{}

	// infoUpdateCh is a buffered channel (capacity 1) used by the task
	// goroutine to request a system-info update from the run() goroutine.
	// The value ("task", "model", "theme", "reasoning", "all") tells the
	// run goroutine which messages to send, avoiding redundant broadcasts.
	// This centralizes all sendSystemInfo calls in one place.
	infoUpdateCh chan string

	// taskCancel holds the cancel function for the currently running task.
	// Set by run() before spawning the task goroutine, cleared by handleTaskDone().
	// Read by cancelRunningTask() via atomic.Value (gated by inProgress atomic).
	// Only one task can run at a time, so a single slot is sufficient.
	taskCancel atomic.Value

	sessionCtx    context.Context    // canceled when input is exhausted
	sessionCancel context.CancelFunc // idempotent cancel
	runDone       chan struct{}      // closed when run() exits
}

// Done returns a channel that is closed when run() has exited.
func (s *Session) Done() <-chan struct{} {
	return s.runDone
}

// TaskError reports whether the last task ended with an error.
func (s *Session) TaskError() bool {
	return s.pausedOnError.Load()
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
// Returns an error if the session file exists but has an incompatible version
// (version must match MessageVersion exactly).
// The returned session is ready to use but NOT yet started —
// call Start() to begin processing input.
func LoadOrNewSession(cfg SessionConfig) (*Session, string, error) {
	cfg.SessionFile = config.ExpandPath(cfg.SessionFile)
	if cfg.SessionFile != "" {
		if data, err := LoadSession(cfg.SessionFile); err == nil {
			return RestoreFromSession(cfg, data), cfg.SessionFile, nil
		} else if errors.Is(err, ErrSessionVersionMismatch) {
			return nil, "", err
		}
	}
	return NewSession(cfg), cfg.SessionFile, nil
}

// NewSession creates a fresh session. Does NOT start goroutines —
// call Start() to begin processing input.
func NewSession(cfg SessionConfig) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		Content:        make([]llm.ContentPart, 0),
		Messages:       make([]llm.Message, 0),
		CreatedAt:      time.Now(),
		ModelManager:   NewModelManager(cfg.ModelConfigPath),
		RuntimeManager: NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:  cfg.SkillsMgr,
		SessionConfig:  cfg,
		taskQueue:      make([]QueueItem, 0),
		stateCh:        make(chan TaskEvent, 64),
		taskResult:     make(chan TaskResult, 1),
		infoUpdateCh:   make(chan string, 1),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.reasoningLevel.Store(int64(config.DefaultReasoningLevel))
	s.initToolConfirmSet(cfg.ToolConfirmTools)
	s.setActiveFromRuntimeConfig()
	s.setActiveFromCliFlag()
	if model := s.ModelManager.GetActive(); model != nil {
		s.applyModelContextLimit(model)
	}
	s.sendSystemInfo("all")
	return s
}

// RestoreFromSession creates a session from saved data.
// Does NOT start goroutines — call Start() to begin processing input.
func RestoreFromSession(cfg SessionConfig, data *SessionData) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		Content:        data.Content,
		Messages:       contentToMessages(data.Content),
		CreatedAt:      data.CreatedAt,
		ModelManager:   NewModelManager(cfg.ModelConfigPath),
		RuntimeManager: NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:  cfg.SkillsMgr,
		SessionConfig:  cfg,
		taskQueue:      make([]QueueItem, 0),
		stateCh:        make(chan TaskEvent, 64),
		taskResult:     make(chan TaskResult, 1),
		infoUpdateCh:   make(chan string, 1),
		sessionCtx:     sessionCtx,
		sessionCancel:  sessionCancel,
		runDone:        make(chan struct{}),
	}
	s.reasoningLevel.Store(int64(data.ReasoningLevel))
	s.ContextTokens.Store(data.ContextTokens)

	s.initToolConfirmSet(cfg.ToolConfirmTools)
	s.sessionMetaModel = data.ActiveModel // used by setActiveFromSessionMeta
	s.setActiveFromRuntimeConfig()
	s.setActiveFromSessionMeta()

	// --model CLI flag takes highest priority: override whatever was resolved above.
	s.setActiveFromCliFlag()

	// Apply context limit from the resolved model so the status bar
	// can show "tokens/limit (pct%)" immediately, before any API call.
	if model := s.ModelManager.GetActive(); model != nil {
		s.applyModelContextLimit(model)
	}

	s.sendSystemInfo("all")

	s.histCounter.Store(uint64(len(s.Content)))

	// Send session content to adapter with IDs so the adapter can reference
	// content by ID even after session reload.
	for _, part := range s.Content {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			continue
		}
		_ = stream.WriteTLV(s.Output, tag, stream.WrapDelta(strconv.FormatUint(part.GetHistoryID(), 10), content)) //nolint:errcheck
	}
	return s
}

// histIncAndGet increments the history counter by 1 and returns the new value.
func (s *Session) histIncAndGet() uint64 {
	return s.histCounter.Add(1)
}

// Start begins processing input in a single goroutine.
// Must be called exactly once after construction.
func (s *Session) Start() {
	go s.run()
}

// cancelRunningTask cancels the currently running task via its per-task
// context. Returns true if a task was actually running and was canceled.
func (s *Session) cancelRunningTask() bool {
	if !s.inProgress.Load() {
		return false
	}
	if cancel, ok := s.taskCancel.Load().(context.CancelFunc); ok && cancel != nil {
		cancel()
		return true
	}
	return false
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
