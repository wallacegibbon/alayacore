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
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main event loop. It processes:
//   - Input messages from the user (via inputPump → inputMsgCh)
//   - Task state changes (via task goroutine → taskEventCh)
//   - Task completion signals (via taskResultCh)
//   - System info refresh requests (via taskRefreshCh)
//   - MCP initialization events (via mcpInit.Events())
func (s *Session) run() {
	defer close(s.runDoneCh)
	defer s.sessionCancel()

	// Start MCP initialization — the goroutine sends events that
	// we read from mcpInit.Events() in the main select below.
	if s.mcpInit != nil {
		s.mcpInit.Start(s.sessionCtx)
	}

	// Start the I/O pump goroutine.
	s.inputMsgCh = make(chan inputMsg, 100)
	go s.inputPump()

	// Capture the MCP events channel once. When the channel is closed
	// (init complete), we set mcpEvents to nil to disable the select case.
	var mcpEvents <-chan mcp.InitEvent
	if s.mcpInit != nil {
		mcpEvents = s.mcpInit.Events()
	}

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

		case evt, ok := <-mcpEvents:
			if !ok {
				// Channel closed — disable this case permanently.
				// If we never saw "done" or "canceled", the init was
				// aborted without a clean notification (e.g. context
				// canceled externally). Set mcpReady so the user
				// can proceed.
				mcpEvents = nil
				if !s.mcpReady.Load() {
					s.mcpReady.Store(true)
					s.writeError("MCP initialization canceled.")
					s.sendMCPMsg("done", "", "", "")
				}
				break
			}
			s.handleMCPEvent(&evt)

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// handleMCPEvent processes a single MCP initialization event.
// Called from the main loop when an event arrives on mcpInit.Events().
func (s *Session) handleMCPEvent(evt *mcp.InitEvent) {
	// Ignore stale events after init was canceled or completed.
	if s.mcpReady.Load() {
		return
	}

	switch evt.Type {
	case "connecting", "connected", "failed":
		// Forward to adapter for progress overlay.
		s.sendMCPMsg(evt.Type, evt.Server, "", evt.Error)

	case "auth_confirm":
		// Session must show confirm dialog — forward to adapter.
		s.sendMCPMsg("auth_confirm", evt.Server, evt.URL, "")

	case "auth_running":
		// OAuth progress — forward to adapter.
		s.sendMCPMsg(evt.Type, evt.Server, "", evt.Error)

	case "done":
		// All done — apply final results.
		if evt.Errors != nil {
			for _, e := range evt.Errors {
				s.writeError(fmt.Sprintf("MCP: %v", e))
			}
		}
		if evt.Manager != nil {
			s.MCPManager = evt.Manager
		}
		if evt.Tools != nil {
			s.BaseTools = append(s.BaseTools, evt.Tools...)
		}
		if evt.SysFragment != "" {
			s.SystemPrompt += evt.SysFragment
		}

		// Recreate agent if it was already initialized.
		if s.agent != nil {
			s.agent = nil
			s.provider = nil
		}

		s.mcpReady.Store(true)
		s.sendMCPMsg("done", "", "", "")
		s.writeNotifyf("MCP servers initialized: %d servers, %d tools loaded",
			evt.Manager.ActiveServerCount(), len(evt.Tools))

	case "canceled":
		s.mcpReady.Store(true)
		s.sendMCPMsg("done", "", "", "")
		s.writeError("MCP initialization canceled.")
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
// currently running task finishes. Also drains any remaining MCP events so
// they aren't lost during shutdown.
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
