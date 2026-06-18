package agent

// Session manages conversation state and task execution.
//
// ARCHITECTURE:
//   Session uses an actor model: the run() goroutine owns all mutable
//   state, and the task goroutine communicates state changes via typed
//   events on taskEventCh. All cross-goroutine communication is channel-based.
//
//   Three goroutines:
//     1. inputPump — reads TLV frames from input, sends parsed messages
//        to the main loop via s.inputMsgCh.  It has no knowledge of commands
//        and never touches session state.
//     2. run() — main loop that owns Contents, task queue, and system
//        info. Processes input messages, dispatches commands, manages
//        cancellation.
//     3. task goroutine — spawned by run() to execute each task. It
//        receives a taskCtx with a snapshot of Messages at task start,
//        accumulates new Entries, and sends the final state back to
//        run() via taskResultCh on completion.
//
//   Cross-goroutine communication:
//     inputMsgCh (inputMsg channel)  — inputPump → run()
//     taskEventCh (taskEvent)        — task → run()
//     taskCancel (func call)     — run() → task (cancellation via cancelRunningTask)
//     taskResultCh                 — task → run (TaskResult with messages + entries)
//     taskRefreshCh               — task → run() (best-effort system-info refresh; see session_output.go)
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
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// sessionConfig groups fields that are set once at construction and
// never modified thereafter.
type sessionConfig struct {
	ModelManager   *ModelManager
	RuntimeManager *RuntimeManager
	SkillsManager  *skills.Manager

	SessionConfig

	initError        error  // set during construction if --model refers to a non-existent model
	sessionMetaModel string // model name from session file frontmatter
	toolConfirmSet   map[string]struct{}
}

// runState groups fields owned exclusively by the run() goroutine.
// All reads and writes happen in the run() event loop.
type runState struct {
	Contents []llm.ContentPart // flat, ordered, 1:1 with TLV — set from task result Entries
	Messages []llm.Message     // grouped by role for API calls — set from task result Messages

	taskQueue []QueueItem

	currentStep int // set by handleTaskEvent (StepStartEvent); read by sendTaskMsg
	inProgress  bool
	nextQueueID uint64

	taskCancel context.CancelFunc

	inputMsgCh    chan inputMsg // inputPump → run: parsed TLV messages
	taskEventCh   chan TaskEvent
	taskResultCh  chan TaskResult
	taskRefreshCh chan struct{}
}

// sharedState groups fields that are either genuinely cross-goroutine
// (synchronized via atomics) or owned by a single goroutine with
// design guarantees that prevent concurrent access.
type sharedState struct {
	ContextTokens int64 // last-known context token count; updated by run() from task events
	ContextLimit  int64 // maximum context window size (input+output); set from model config

	reasoningLevel int
	histCounter    uint64

	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	agent    *llm.Agent
	provider llm.Provider

	confirmCh atomic.Pointer[chan<- llm.ToolConfirmResponse]

	pausedOnError atomic.Bool
	outputBroken  atomic.Bool
}

// Session manages conversation state and task execution.
// Fields are grouped into embedded sub-structs by ownership:
//   - sessionConfig — immutable after construction
//   - runState      — owned by the run() goroutine
//   - sharedState   — cross-goroutine, synchronized via atomics/channels
type Session struct {
	sessionConfig
	runState
	sharedState

	runDoneCh chan struct{} // closed when run() exits
	CreatedAt time.Time
}

// Done returns a channel that is closed when run() has exited.
func (s *Session) Done() <-chan struct{} {
	return s.runDoneCh
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
			s := RestoreFromSession(cfg, data)
			if replayErr := s.replayContentsToAdapter(); replayErr != nil {
				s.initError = replayErr
			}
			return s, cfg.SessionFile, nil
		} else if errors.Is(err, ErrSessionVersionMismatch) {
			return nil, "", err
		}
	}
	return NewSession(cfg), cfg.SessionFile, nil
}

// NewSession creates a fresh session. Does NOT start goroutines —
// call Start() to begin processing input.
func NewSession(cfg SessionConfig) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		sessionConfig: sessionConfig{
			ModelManager:   NewModelManager(cfg.ModelConfigPath),
			RuntimeManager: NewRuntimeManager(cfg.RuntimeConfigPath),
			SkillsManager:  cfg.SkillsMgr,
			SessionConfig:  cfg,
		},
		runState: runState{
			Contents:      make([]llm.ContentPart, 0),
			Messages:      make([]llm.Message, 0),
			taskQueue:     make([]QueueItem, 0),
			taskEventCh:   make(chan TaskEvent, 64),
			taskResultCh:  make(chan TaskResult, 1),
			taskRefreshCh: make(chan struct{}, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
		},
		runDoneCh: make(chan struct{}),
		CreatedAt: time.Now(),
	}
	s.reasoningLevel = config.DefaultReasoningLevel
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
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		sessionConfig: sessionConfig{
			ModelManager:     NewModelManager(cfg.ModelConfigPath),
			RuntimeManager:   NewRuntimeManager(cfg.RuntimeConfigPath),
			SkillsManager:    cfg.SkillsMgr,
			SessionConfig:    cfg,
			sessionMetaModel: data.ActiveModel,
		},
		runState: runState{
			Contents:      data.Contents,
			Messages:      contentsToMessages(data.Contents),
			taskQueue:     make([]QueueItem, 0),
			taskEventCh:   make(chan TaskEvent, 64),
			taskResultCh:  make(chan TaskResult, 1),
			taskRefreshCh: make(chan struct{}, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
		},
		runDoneCh: make(chan struct{}),
		CreatedAt: data.CreatedAt,
	}
	s.reasoningLevel = data.ReasoningLevel
	s.ContextTokens = data.ContextTokens
	s.histCounter = uint64(len(s.Contents))

	s.initToolConfirmSet(cfg.ToolConfirmTools)
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
	return s
}

// replayContentsToAdapter sends all content parts to the adapter with history IDs,
// so the adapter can reference them by ID even after session reload.
func (s *Session) replayContentsToAdapter() error {
	for _, part := range s.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			return fmt.Errorf("corrupt session file: failed to serialize content part (HistoryID=%d): %w", part.GetHistoryID(), err)
		}
		s.writeTLV(tag, stream.WrapDelta(strconv.FormatUint(part.GetHistoryID(), 10), content))
	}
	return nil
}

// histIncAndGet increments the history counter by 1 and returns the new value.
func (s *Session) histIncAndGet() uint64 {
	s.histCounter++
	return s.histCounter
}

// Start begins processing input in a single goroutine.
// Must be called exactly once after construction.
func (s *Session) Start() {
	go s.run()
}

// cancelRunningTask cancels the currently running task via its per-task
// context. Returns true if a task was actually running and was canceled.
func (s *Session) cancelRunningTask() bool {
	if !s.inProgress {
		return false
	}
	if s.taskCancel != nil {
		s.taskCancel()
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
