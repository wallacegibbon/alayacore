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
//   - MCP initialization & OAuth authorization results (via mcpUpdateCh)
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

		case update := <-s.mcpUpdateCh:
			s.applyMCPUpdate(update)

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// applyMCPUpdate applies MCP initialization results to the session.
// Called from the run() goroutine when an MCPUpdateEvent is received
// (either from startMCPInitWatcher or from a completed OAuth flow).
func (s *Session) applyMCPUpdate(update MCPUpdateEvent) {
	// 1. Append MCP tools to BaseTools.
	s.BaseTools = append(s.BaseTools, update.Tools...)

	// 2. Append MCP system prompt fragments (instructions + resources + prompts).
	s.SystemPrompt += update.SystemPromptSuffix

	// 3. Store MCP manager for lifecycle management.
	s.MCPManager = update.Manager

	// 4. If the agent was already created, recreate it to include updated tools.
	if s.agent != nil {
		s.agent = nil
		s.provider = nil
	}

	// 5. Track pending OAuth count.
	//
	//    The initial event from waitMCPInit carries PendingOAuthCount = N
	//    (number of servers needing OAuth). Each server subsequently gets
	//    either skipMCPAuth (user said "no") or a successful OAuth flow
	//    (handleMCPAuth goroutine completes) — both decrement the counter.
	//
	//    When the counter reaches zero, mcpReady is set and user messages
	//    are accepted.
	if update.PendingOAuthCount > 0 {
		s.pendingOAuth.Add(update.PendingOAuthCount)
		// Reset ready flag in case skipMCPAuth raced ahead and set it.
		s.mcpReady.Store(false)
	}

	// 6. Mark MCP as ready only when there are no pending OAuth servers.
	if s.pendingOAuth.Load() == 0 {
		s.mcpReady.Store(true)
	}

	// 7. Notify the user.
	if update.PendingOAuthCount > 0 {
		s.writeNotifyf("MCP servers partially initialized. %d OAuth %s need authorization — use :mcp_auth <name> yes|no.",
			s.pendingOAuth.Load(), pluralizeServer(s.pendingOAuth.Load()))
	} else if s.pendingOAuth.Load() == 0 {
		s.writeNotifyf("MCP servers initialized: %d servers, %d tools loaded",
			update.Manager.ActiveServerCount(), len(update.Tools))
	}
}

// pluralizeServer returns "server" or "servers" based on count.
func pluralizeServer(n int32) string {
	if n == 1 {
		return "server"
	}
	return "servers"
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
// currently running task finishes. Also drains any pending MCP updates so
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
		case update := <-s.mcpUpdateCh:
			s.applyMCPUpdate(update)
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
