package agent

// Session event loop: main select loop.
//
// The run() goroutine owns all mutable state. It processes events from
// the input pump, the task goroutine, and system info requests.
// There is no task queue — prompts and LLM-requiring commands run
// immediately in a task goroutine.  Input during a running task is
// rejected.
//
// Extracted from session_task.go to separate concerns:
//   - session_task.go:        prompt processing, agent loop, auto-summarization
//   - session_loop.go:        event loop
//   - session_io.go:          input pump, command dispatch

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main event loop. It processes:
//   - Input messages from the user (via inputPump → inputMsgCh)
//   - Task state changes (via task goroutine → taskEventCh)
//   - Task completion signals (via taskResultCh)
//   - System info refresh requests (via taskRefreshCh)
func (s *Session) run() {
	defer close(s.runDoneCh)
	defer s.sessionCancel()

	// Start the I/O pump goroutine.
	s.inputMsgCh = make(chan inputMsg, 100)
	go s.inputPump()

	for {
		if s.sessionCtx.Err() != nil {
			return
		}

		select {
		case msg, ok := <-s.inputMsgCh:
			if !ok {
				// Input closed (EOF). Drain the currently running task.
				if s.activeTask != nil {
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

// handleTaskDone processes a task completion signal from the task goroutine.
func (s *Session) handleTaskDone(contents []llm.ContentPart) {
	s.flushPendingEvents()
	s.activeTask = nil

	if len(contents) > 0 {
		s.Contents = contents
	}

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
// currently running task finishes.
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
func (s *Session) handleTaskEvent(ev TaskEvent) {
	switch e := ev.(type) {
	case StepStartEvent:
		if s.activeTask != nil {
			s.activeTask.step = e.Step
		}
		s.sendSystemInfo("task")

	case StepFinishEvent:
		newContext := e.InputTokens + e.OutputTokens + e.CacheReadTokens + e.CacheCreationTokens
		if newContext > 0 {
			s.ContextTokens = newContext
		}

	case SetContextTokensEvent:
		if e.Tokens > 0 {
			s.ContextTokens = e.Tokens
		}
	}
}
