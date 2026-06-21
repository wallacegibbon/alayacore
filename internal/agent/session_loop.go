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

// run is the main event loop. It processes four kinds of events:
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
				for s.activeTask != nil || s.tryStartNextTask() {
					s.drainUntilTaskDone()
				}
				return
			}
			s.handleInputMsg(msg)

		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)

		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)

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
	if len(s.taskQueue) == 0 || s.activeTask != nil || s.sessionCtx.Err() != nil {
		return false
	}

	item := s.taskQueue[0]

	// When paused on error, skip user prompts — they need :continue first.
	if s.pausedOnError.Load() {
		if item.Type != TaskTypeCommand {
			return false
		}
	}

	// Fail before mutating state if agent is not available.
	if err := s.ensureAgentInitialized(); err != nil {
		s.writeError(err.Error())
		return false
	}

	s.taskQueue = s.taskQueue[1:]

	// Create a snapshot of Contents for the task goroutine, seeded as
	// the starting Contents. Task processing appends new contents as they're
	// produced. handleTaskDone replaces s.Contents with the final contents.
	taskContent := make([]llm.ContentPart, len(s.Contents))
	copy(taskContent, s.Contents)

	// Create a per-task context derived from sessionCtx. The cancel
	// function is stored on s.activeTask so cancelRunningTask() can
	// cancel the task. Consumed (set to nil) in handleTaskDone().
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.activeTask = &taskHandle{cancel: taskCancel, step: 0}

	go s.runTask(taskCtx, item, taskContent)
	return true
}

// handleTaskDone processes a task completion signal from the task goroutine.
// contents is the full ContentParts list from the completed task.
func (s *Session) handleTaskDone(contents []llm.ContentPart) {
	// Commit remaining state events before the next task starts.
	s.flushPendingEvents()

	// Mark the task as finished so the next queue item can start.
	s.activeTask = nil

	// Replace s.Contents with the final task state.
	// contents is seeded from s.Contents at task start and accumulated
	// during processing, so it always represents the full content.
	if len(contents) > 0 {
		s.Contents = contents
	}

	// Auto-save if configured
	if s.SessionFile != "" {
		if err := s.saveContentToFile(s.SessionFile, s.Contents); err != nil {
			s.writeNotifyf("Auto-save failed: %v", err)
		}
	}

	s.sendSystemInfo("task")
}

// flushPendingEvents drains remaining taskEventCh events from the
// just-finished task before the next one starts.
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
		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)
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
		if s.activeTask != nil {
			s.activeTask.step = e.Step
		}
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
