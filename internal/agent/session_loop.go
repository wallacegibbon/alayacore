package agent

// Session event loop: task queue management and main select loop.
//
// The run() goroutine owns all mutable state. It processes events from
// the input pump, the task goroutine, and system info requests.
//
// Extracted from session_task.go to separate concerns:
//   - session_task.go:        prompt processing, agent loop, auto-summarization
//   - session_loop.go:        event loop, task queue management
//   - session_io.go:          input pump, command dispatch

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main loop that owns input processing, task queue management,
// and the authoritative copy of session state (s.Messages, taskQueue,
// totals, etc.). It runs in a single goroutine (started by Start()).
//
// s.Messages is the single source of truth for the conversation history,
// owned entirely by the run() goroutine. When a task starts, a snapshot
// of s.Messages is passed to the task goroutine as its local working copy.
// The task goroutine never writes to s.Messages directly — it returns the
// final state via taskResult channel on completion.
//
// The loop processes three kinds of events:
//   - Input messages from the user (via inputPump → msgCh)
//   - Task state changes (via task goroutine → stateCh)
//   - Task completion signals (via taskResult)
//   - System info refresh requests (via infoUpdateCh)
func (s *Session) run() {
	defer close(s.runDone)
	defer s.sessionCancel()

	// Initialize s.Messages if nil (fresh session).
	// When restoring from a session (RestoreFromSession), s.Messages
	// already contains the loaded history.
	if s.Messages == nil {
		s.Messages = make([]llm.Message, 0)
	}

	// Start the I/O pump goroutine — it reads TLV from the input and
	// sends parsed messages to run() for processing. It has NO access
	// to session state except taskCancelCh (for :cancel commands).
	msgCh := make(chan inputMsg, 100)
	go s.inputPump(msgCh)

	for {
		// Check for context cancellation (input closed externally)
		if s.sessionCtx.Err() != nil {
			return
		}

		s.tryStartNextTask()

		// Wait for new input, task events, task completion, or info requests
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// Input is closed (EOF on stdin). Drain the currently
				// running task, then process any remaining queued tasks.
				for s.inProgress || s.tryStartNextTask() {
					s.drainUntilTaskDone()
				}
				return
			}
			s.handleInputMsg(msg)

		case ev := <-s.stateCh:
			s.handleTaskEvent(ev)

		case result := <-s.taskResult:
			s.handleTaskDone(result)

		case kind := <-s.infoUpdateCh:
			s.sendSystemInfo(kind)

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// tryStartNextTask checks whether a new task can be started from the queue
// and launches it if so. When paused on error, only command tasks are
// allowed to run — user prompts must wait for explicit recovery via :continue.
// Returns true if a task was started.
func (s *Session) tryStartNextTask() bool {
	if len(s.taskQueue) == 0 || s.inProgress || s.sessionCtx.Err() != nil {
		return false
	}

	item := s.taskQueue[0]

	// When paused on error, skip user prompts — they need :continue first.
	if s.pausedOnError.Load() {
		if _, ok := item.Task.(CommandPrompt); !ok {
			return false
		}
	}

	s.taskQueue = s.taskQueue[1:]
	s.inProgress = true

	// Ensure agent is initialized before spawning the task goroutine.
	// This avoids calling ModelManager from the task goroutine and keeps
	// all model state access in the run() goroutine.
	if errMsg := s.ensureAgentInitialized(); errMsg != "" {
		s.writeError(errMsg)
		s.inProgress = false
		s.sendSystemInfo("task")
		return false
	}

	// Create a snapshot of s.Messages for the task goroutine.
	// The task goroutine owns this copy and returns the final state
	// via taskResult when done.
	taskMessages := make([]llm.Message, len(s.Messages))
	copy(taskMessages, s.Messages)

	go s.runTask(item, taskMessages)
	return true
}

// handleTaskDone processes a task completion signal from the task goroutine.
// result is the final message state returned by the task goroutine.
func (s *Session) handleTaskDone(result []llm.Message) {
	// Mark the task as finished so the next queue item can start.
	s.inProgress = false

	// Update s.Messages with the final message state from the task goroutine.
	if len(result) > 0 {
		s.Messages = result
	}

	// Auto-save if configured
	if s.SessionFile != "" {
		if err := s.saveSessionToFileWith(s.Messages, s.SessionFile); err != nil {
			s.writeNotifyf("Auto-save failed: %v", err)
		}
	}

	s.sendSystemInfo("task")
}

// drainUntilTaskDone processes task events and completion signals until the
// currently running task finishes. Called when input is closed but a task is
// still in progress, ensuring final output (prompt echo, assistant response)
// is flushed before the session exits.
func (s *Session) drainUntilTaskDone() {
	for {
		select {
		case ev := <-s.stateCh:
			s.handleTaskEvent(ev)
		case result := <-s.taskResult:
			s.handleTaskDone(result)
			return
		case kind := <-s.infoUpdateCh:
			s.sendSystemInfo(kind)
		case <-s.sessionCtx.Done():
			return
		}
	}
}

// handleTaskEvent processes a state change event from the task goroutine.
// Only called from the run() goroutine.
func (s *Session) handleTaskEvent(ev TaskEvent) {
	switch e := ev.(type) {
	case StepStartEvent:
		s.currentStep.Store(int64(e.Step))

	case StepFinishEvent:
		s.TotalSpent.InputTokens += e.InputTokens
		s.TotalSpent.OutputTokens += e.OutputTokens
		// Anthropic's input_tokens excludes cached tokens; sum all
		// four fields for total context. OpenAI-compatible APIs
		// have Cache* = 0, so this collapses to InputTokens+OutputTokens.
		//
		// OutputTokens is included because ContextLimit represents the
		// model's total context window (input+output combined), and the
		// latest assistant response is part of the conversation that
		// will be sent in the next request.
		newContext := e.InputTokens + e.OutputTokens + e.CacheReadTokens + e.CacheCreationTokens
		if newContext > 0 {
			s.ContextTokens.Store(newContext)
		}

	case SetContextTokensEvent:
		// Sets ContextTokens without affecting TotalSpent counters.
		// Used by summarize() to correct the value after summarization.
		if e.Tokens > 0 {
			s.ContextTokens.Store(e.Tokens)
		}
	}
}
