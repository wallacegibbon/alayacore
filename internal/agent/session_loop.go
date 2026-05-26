package agent

// Session event loop: task queue management and main select loop.
//
// The run() goroutine owns all mutable state. It processes events from
// the input pump, the task goroutine, and system info requests.
//
// Extracted from session_task.go to separate concerns:
//   - session_task.go:        prompt processing, agent loop
//   - session_loop.go:        event loop, task queue management
//   - session_io.go:          input pump, command dispatch
//   - session_summarize.go:   auto-summarization

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main loop that owns input processing, task queue management,
// and the authoritative copy of session state (runMessages, taskQueue,
// totals, etc.). It runs in a single goroutine (started by Start()).
//
// The loop processes three kinds of events:
//   - Input messages from the user (via inputPump → msgCh)
//   - Task state changes (via task goroutine → stateCh)
//   - Task completion signals (via taskDone)
//   - System info refresh requests (via infoUpdateCh)
func (s *Session) run() {
	defer close(s.runDone)
	defer s.sessionCancel()

	// runMessages is the run() goroutine's copy of the conversation history.
	// The task goroutine has its own working copy (s.Messages). At task
	// start, runMessages is copied to s.Messages. During task execution,
	// OnStepFinish sends the agent's allMessages (full history) via
	// eventStepFinish, which replaces runMessages entirely.
	// Between tasks, runMessages is the source of truth.
	//
	// When restoring from a session (RestoreFromSession), s.Messages
	// already contains the loaded history — initialize runMessages from
	// it so the loaded messages are not lost on the first task.
	runMessages := s.Messages
	if runMessages == nil {
		runMessages = make([]llm.Message, 0)
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

		s.tryStartNextTask(&runMessages)

		// Wait for new input, task events, task completion, or info requests
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// Input is closed (EOF on stdin). If a task is still running,
				// keep processing events until it completes so that output
				// (prompt echo, assistant response) is flushed before exit.
				if s.inProgress {
					s.drainUntilTaskDone(&runMessages)
				}
				return
			}
			s.handleInputMsg(msg)

		case ev := <-s.stateCh:
			s.handleTaskEvent(ev, &runMessages)

		case <-s.taskDone:
			s.handleTaskDone(&runMessages)

		case <-s.infoUpdateCh:
			s.sendSystemInfo()

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// tryStartNextTask checks whether a new task can be started from the queue
// and launches it if so. When paused on error, only command tasks are
// allowed to run — user prompts must wait for explicit recovery via :continue.
// Returns true if a task was started.
func (s *Session) tryStartNextTask(runMessages *[]llm.Message) bool {
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
		s.requestSystemInfo()
		return false
	}

	// Copy runMessages to s.Messages as the task goroutine's working copy.
	s.Messages = make([]llm.Message, len(*runMessages))
	copy(s.Messages, *runMessages)

	go s.runTask(item)
	return true
}

// handleTaskDone processes a task completion signal from the task goroutine.
// It marks the task as finished, syncs messages, and auto-saves if configured.
//
// runMessages is already kept in sync during the task via eventStepFinish
// (which carries allMessages from the agent). The sync from s.Messages
// below serves as a final safety net for error/cancel cases where the
// last OnStepFinish might not have been called.
func (s *Session) handleTaskDone(runMessages *[]llm.Message) {
	// Mark the task as finished so the next queue item can start.
	s.inProgress = false

	// Sync runMessages back from s.Messages (task goroutine's final state)
	if len(s.Messages) > 0 {
		*runMessages = make([]llm.Message, len(s.Messages))
		copy(*runMessages, s.Messages)
	}

	// Auto-save if configured (uses runMessages which is now up-to-date)
	if s.SessionFile != "" {
		if err := s.saveSessionToFileWith(*runMessages, s.SessionFile); err != nil {
			s.writeNotifyf("Auto-save failed: %v", err)
		}
	}

	s.sendSystemInfo()
}

// drainUntilTaskDone processes task events and completion signals until the
// currently running task finishes. Called when input is closed but a task is
// still in progress, ensuring final output (prompt echo, assistant response)
// is flushed before the session exits.
func (s *Session) drainUntilTaskDone(runMessages *[]llm.Message) {
	for {
		select {
		case ev := <-s.stateCh:
			s.handleTaskEvent(ev, runMessages)
		case <-s.taskDone:
			s.handleTaskDone(runMessages)
			return
		case <-s.infoUpdateCh:
			s.sendSystemInfo()
		case <-s.sessionCtx.Done():
			return
		}
	}
}

// handleTaskEvent processes a state change event from the task goroutine.
// Only called from the run() goroutine.
func (s *Session) handleTaskEvent(ev taskEvent, runMessages *[]llm.Message) {
	switch ev.typ {
	case eventStepStart:
		s.currentStep.Store(int64(ev.step))

	case eventStepFinish:
		if len(ev.messages) > 0 {
			*runMessages = ev.messages // allMessages from agent — full history
		}
		s.TotalSpent.InputTokens += ev.inputTokens
		s.TotalSpent.OutputTokens += ev.outputTokens
		newContext := ev.inputTokens + ev.cacheReadTokens + ev.cacheCreationTokens
		if newContext > 0 {
			s.ContextTokens.Store(newContext)
		}

	case eventCleanMessages:
		*runMessages = cleanIncompleteToolCalls(*runMessages)

	case eventSetPaused:
		s.pausedOnError.Store(ev.paused)

	case eventSaveRequest:
		if s.SessionFile != "" {
			if err := s.saveSessionToFileWith(*runMessages, s.SessionFile); err != nil {
				s.writeNotifyf("Auto-save failed: %v", err)
			}
		}

	case eventSyncReason:
		s.reasoningLevel.Store(int64(ev.reasoningLevel))
		if p := s.provider.Load(); p != nil {
			(*p).SetReasoningLevel(ev.reasoningLevel)
		}
	}
}
