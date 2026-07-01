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
	"context"
	"errors"
	"fmt"
	"strings"

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
//   - MCP initialization results (via mcpUpdateCh)
//   - MCP OAuth authorization results (via oauthResultCh)
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

		case result := <-s.oauthResultCh:
			s.handleOAuthResult(result)

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// applyMCPUpdate applies MCP initialization results to the session.
// Called from the run() goroutine when the initial MCPUpdateEvent is
// received from startMCPInitWatcher.
func (s *Session) applyMCPUpdate(update MCPUpdateEvent) {
	// 1. Append MCP tools to BaseTools.
	s.BaseTools = append(s.BaseTools, update.Tools...)
	s.mcpToolCount = len(update.Tools)

	// 2. Append MCP system prompt fragments (instructions + resources + prompts).
	s.SystemPrompt += update.SystemPromptSuffix

	// 3. Store MCP manager for lifecycle management.
	s.MCPManager = update.Manager

	// 4. If the agent was already created, recreate it to include updated tools.
	if s.agent != nil {
		s.agent = nil
		s.provider = nil
	}

	// 5. Start OAuth sequence if there are pending servers.
	if len(update.PendingOAuthServers) > 0 {
		s.pendingOAuthServers = update.PendingOAuthServers
		s.pendingOAuthIdx = 0
		s.mcpReady.Store(false)

		// Send the first confirm prompt to the adapter.
		first := s.pendingOAuthServers[0]
		s.sendMCPAuthConfirm(first.Name, first.URL)
		s.writeNotifyf("MCP servers partially initialized. %d server%s need OAuth authorization.",
			len(s.pendingOAuthServers), pluralize(len(s.pendingOAuthServers)))
	} else {
		s.mcpReady.Store(true)
		s.writeNotifyf("MCP servers initialized: %d servers, %d tools loaded",
			update.Manager.ActiveServerCount(), len(update.Tools))
	}
}

// handleOAuthResult processes the result of an OAuth authorization.
// Called from the run() goroutine when an oauthResult is received from
// the OAuth goroutine via oauthResultCh.
func (s *Session) handleOAuthResult(result oauthResult) {
	// Ignore stale results from servers the user already skipped.
	if result.idx < s.pendingOAuthIdx {
		return
	}

	if result.err != nil {
		if errors.Is(result.err, context.Canceled) {
			// User skipped this server — the goroutine was canceled.
			// skipCurrentMCPAuth already advanced the index; nothing to do.
			return
		}
		s.writeError(fmt.Sprintf("MCP auth failed for %q: %v", result.name, result.err))
		s.pendingOAuthIdx++
		s.advanceMCPAuth()
		return
	}

	// Successful authorization — process tools.
	if result.tools != nil {
		s.applyOAuthTools(result.name, result.tools)
	}

	s.pendingOAuthIdx++
	s.advanceMCPAuth()
}

// applyOAuthTools processes tools from a completed OAuth authorization
// and applies them to the session state (same pattern as applyMCPUpdate).
func (s *Session) applyOAuthTools(name string, tools []mcp.Tool) {
	mgr := s.MCPManager
	if mgr == nil {
		return
	}

	// Build system prompt fragment (server instructions).
	var frag strings.Builder
	for _, c := range mgr.Clients() {
		if c.Name() == name {
			if instr := c.Instructions(); instr != "" {
				frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", name, instr))
			}
			break
		}
	}

	// Convert tools.
	serverTools := map[string][]mcp.Tool{name: tools}
	agentTools := mcp.ToolsToAgentTools(serverTools, mgr)
	agentTools = append(agentTools, mcp.ResourcesToAgentTools(mgr.Clients(), mgr)...)
	agentTools = append(agentTools, mcp.PromptsToAgentTools(mgr.Clients(), mgr)...)

	// Apply to session state.
	s.BaseTools = append(s.BaseTools, agentTools...)
	s.mcpToolCount += len(agentTools)
	s.SystemPrompt += frag.String()

	// Recreate agent if it was initialized.
	if s.agent != nil {
		s.agent = nil
		s.provider = nil
	}

	s.writeNotifyf("✓ MCP server %q authorized and connected (%d tools).", name, len(tools))
}

// advanceMCPAuth sends the next auth confirm prompt or marks MCP as ready.
// Called after user action (yes/no) completes for the current server.
func (s *Session) advanceMCPAuth() {
	if s.pendingOAuthIdx < len(s.pendingOAuthServers) {
		next := s.pendingOAuthServers[s.pendingOAuthIdx]
		s.sendMCPAuthConfirm(next.Name, next.URL)
	} else {
		// All servers done.
		s.oauthCancel = nil
		s.mcpReady.Store(true)
		s.sendMCPAuthDone()
		s.sendSystemInfo("all")
		s.writeNotifyf("MCP servers initialized: %d servers, %d tools loaded",
			s.MCPManager.ActiveServerCount(), s.mcpToolCount)
	}
}

// pluralize returns "s" for plural count, empty string for singular.
func pluralize(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
		case <-s.oauthResultCh:
			// OAuth results during shutdown are safe to discard.
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
