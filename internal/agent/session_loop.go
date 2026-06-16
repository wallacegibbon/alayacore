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
	"context"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main loop that owns input processing, task queue management,
// and the authoritative copy of session state (s.Content, taskQueue,
// totals, etc.). It runs in a single goroutine (started by Start()).
//
// s.Content is the single source of truth for the conversation history,
// a flat ordered slice of ContentPart where each item has a stable ID
// that matches the adapter's TLV stream IDs. s.Messages is derived from
// s.Content for API calls and rebuilt after each task completes.
// When a task starts, a snapshot of s.Messages (derived from Content)
// is passed to the task goroutine as its local working copy.
// The task goroutine never writes to s.Content directly — it returns the
// final state via taskResultCh channel on completion.
//
// The loop processes four kinds of events:
//   - Input messages from the user (via inputPump → inputMsgCh)
//   - Task state changes (via task goroutine → taskEventCh)
//   - Task completion signals (via taskResultCh)
//   - System info refresh requests (via taskRefreshCh)
func (s *Session) run() {
	defer close(s.runDoneCh)
	defer s.sessionCancel()

	// Start the I/O pump goroutine — it reads TLV from the input and
	// sends parsed messages to run() for processing via inputMsgCh.
	s.inputMsgCh = make(chan inputMsg, 100)
	go s.inputPump()

	for {
		// Check for context cancellation (input closed externally)
		if s.sessionCtx.Err() != nil {
			return
		}

		s.tryStartNextTask()

		// Wait for new input, task events, task completion, or info requests
		select {
		case msg, ok := <-s.inputMsgCh:
			if !ok {
				// Input is closed (EOF on stdin). Drain the currently
				// running task, then process any remaining queued tasks.
				for s.inProgress || s.tryStartNextTask() {
					s.drainUntilTaskDone()
				}
				return
			}
			s.handleInputMsg(msg)

		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)

		case result := <-s.taskResultCh:
			s.handleTaskDone(result)

		case <-s.taskRefreshCh:
			s.sendSystemInfo("task")

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
		if item.Type != TaskTypeCommand {
			return false
		}
	}

	s.taskQueue = s.taskQueue[1:]
	s.inProgress = true

	// Ensure agent is initialized before spawning the task goroutine.
	// This avoids calling ModelManager from the task goroutine and keeps
	// all model state access in the run() goroutine.
	if err := s.ensureAgentInitialized(); err != nil {
		s.writeError(err.Error())
		s.inProgress = false
		s.sendSystemInfo("task")
		return false
	}

	// Create a snapshot of Messages for the task goroutine.
	// Messages is always derived from Content, so this is in sync.
	taskMessages := make([]llm.Message, len(s.Messages))
	copy(taskMessages, s.Messages)

	// Create a per-task context derived from sessionCtx. The cancel
	// function is stored in s.taskCancel so cancelRunningTask() can
	// cancel the task. Cleared in handleTaskDone().
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.taskCancel = taskCancel

	// Reset step counter before starting the task.
	s.currentStep = 0

	go s.runTask(taskCtx, item, taskMessages)
	return true
}

// handleTaskDone processes a task completion signal from the task goroutine.
// result carries the final message state and new ContentParts.
func (s *Session) handleTaskDone(result TaskResult) {
	// Commit state events from the just-finished task before
	// the next task starts. All taskEventCh events are sent before
	// taskResultCh (in defer), so remaining events belong to this task.
	s.flushPendingEvents()

	// Mark the task as finished so the next queue item can start.
	s.inProgress = false
	s.taskCancel = nil

	// Append new ContentParts.
	if len(result.Entries) > 0 {
		s.Content = append(s.Content, result.Entries...)
		// Rebuild Messages from Content.
		s.Messages = result.Messages
	}

	// Auto-save if configured
	if s.SessionFile != "" {
		if err := s.saveContentToFile(s.SessionFile, s.Content); err != nil {
			s.writeNotifyf("Auto-save failed: %v", err)
		}
	}

	s.sendSystemInfo("task")
}

// flushPendingEvents non-blocking drains all remaining events from taskEventCh.
// The task goroutine sends all events before taskResultCh (sent in defer),
// so any events still in the buffer belong to the just-finished task.
// Processing them here ensures state (e.g. ContextTokens) is committed
// before the next task starts.
func (s *Session) flushPendingEvents() {
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		default:
			return
		}
	}
}

// drainUntilTaskDone processes task events and completion signals until the
// currently running task finishes. Called when input is closed but a task is
// still in progress, ensuring final output (prompt echo, assistant response)
// is flushed before the session exits.
func (s *Session) drainUntilTaskDone() {
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		case result := <-s.taskResultCh:
			s.handleTaskDone(result)
			return
		case <-s.taskRefreshCh:
			s.sendSystemInfo("task")
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
		s.currentStep = e.Step
		s.sendSystemInfo("task")

	case StepFinishEvent:
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
			s.ContextTokens = newContext
		}

	case SetContextTokensEvent:
		// Corrects ContextTokens after summarize() to the summary size
		// instead of the full old-context token count.
		if e.Tokens > 0 {
			s.ContextTokens = e.Tokens
		}
	}
}
