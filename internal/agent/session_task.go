package agent

// Session task execution: reading input, processing prompts,
// executing the agent loop with streaming callbacks.
//
// The main loop (run()) owns all mutable state. The task goroutine
// communicates state changes via stateCh events. No sync.Mutex needed.

import (
	"context"
	"encoding/json"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
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

	// runMessages is the run() goroutine's authoritative copy of messages.
	// The task goroutine has its own working copy (s.Messages). At task
	// start, runMessages is copied to s.Messages. During task execution,
	// the task goroutine sends state changes via stateCh, which update
	// runMessages. Between tasks, runMessages is the source of truth.
	var runMessages []llm.Message

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

		// Start next task if queue is non-empty and no task is running.
		if len(s.taskQueue) > 0 && !s.inProgress.Load() && s.sessionCtx.Err() == nil {
			item := s.taskQueue[0]
			s.taskQueue = s.taskQueue[1:]
			s.inProgress.Store(true)

			// Copy runMessages to s.Messages as the task goroutine's working copy.
			s.Messages = make([]llm.Message, len(runMessages))
			copy(s.Messages, runMessages)

			go s.runTask(item)
		}

		// Wait for new input, task events, task completion, or info requests
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			s.handleInputMsg(msg)

		case ev := <-s.stateCh:
			s.handleTaskEvent(ev, &runMessages)

		case <-s.taskDone:
			s.inProgress.Store(len(s.taskQueue) > 0)

			// Sync runMessages back from s.Messages (task goroutine's final state)
			if len(s.Messages) > 0 {
				runMessages = make([]llm.Message, len(s.Messages))
				copy(runMessages, s.Messages)
			}

			// Auto-save if configured (uses runMessages which is now up-to-date)
			if s.SessionFile != "" {
				if err := s.saveSessionToFileWith(runMessages, s.SessionFile); err != nil {
					s.writeNotifyf("Auto-save failed: %v", err)
				}
			}

			s.sendSystemInfo()

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
			*runMessages = append(*runMessages, ev.messages...)
		}
		s.TotalSpent.InputTokens += ev.inputTokens
		s.TotalSpent.OutputTokens += ev.outputTokens
		newContext := ev.inputTokens + ev.cacheReadTokens + ev.cacheCreationTokens
		if newContext > 0 {
			s.ContextTokens.Store(newContext)
		}

	case eventAppendMessages:
		if len(ev.appendMsgs) > 0 {
			*runMessages = append(*runMessages, ev.appendMsgs...)
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

	case eventSyncThink:
		s.thinkLevel.Store(int64(ev.thinkLevel))
		if p := s.provider.Load(); p != nil {
			if prov, ok := p.(llm.Provider); ok {
				prov.SetReasoningLevel(ev.thinkLevel)
			}
		}
	}
}

// inputMsg carries a parsed input message from the I/O pump to run().
type inputMsg struct {
	text  string // the raw user text or command text (without ':')
	isCmd bool   // true if text starts with ':'
}

// inputPump runs in its own goroutine and reads TLV frames from the
// input stream. It sends parsed messages to msgCh. It does NOT access
// any session state directly; for :cancel / :cancel_all commands it sends
// to taskCancelCh (a buffered channel) which the task goroutine listens on.
func (s *Session) inputPump(msgCh chan<- inputMsg) {
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			close(msgCh)
			return
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag, "invalid input tag: %s", tag).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			if cmd == commandNameCancel || cmd == commandNameCancelAll {
				// Signal cancellation to the running task (non-blocking).
				// If no task is running, the signal is lost and the command
				// is forwarded to msgCh so run() can handle queue clearing
				// or report "nothing to cancel".
				canceled := s.cancelRunningTask()
				if canceled && cmd == commandNameCancel {
					// Task was running and was canceled — don't send to msgCh
					// to avoid a spurious "nothing to cancel" message.
					continue
				}
				// For cancel_all, always send to msgCh for queue clearing.
				// For cancel (when no task running), forward for error reporting.
			}
			msgCh <- inputMsg{text: cmd, isCmd: true}
		} else {
			msgCh <- inputMsg{text: value, isCmd: false}
		}
	}
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.isCmd {
		cmd := msg.text
		// Immediate commands are handled directly; deferred commands
		// go through the task queue.
		if isCommandImmediate(cmd) {
			s.handleCommand(context.Background(), cmd)
		} else {
			s.submitDeferredCommand(cmd)
		}
	} else {
		s.submitTask(UserPrompt{Text: msg.text})
	}
}

// ============================================================================
// Input Processing (immediate command dispatch)
// ============================================================================

// isCommandImmediate returns true if the command should be handled immediately
// without queuing. Immediate commands are those that control task execution
// (cancel, continue) or query/modify session state (model_load, taskqueue operations).
func isCommandImmediate(cmd string) bool {
	// Extract the command name (first word) for commands that accept arguments.
	name := cmd
	if idx := strings.IndexByte(cmd, ' '); idx >= 0 {
		name = cmd[:idx]
	}
	switch name {
	case commandNameCancel, commandNameCancelAll, commandNameModelLoad, commandNameTaskQueueGetAll, commandNameTaskQueueEdit, commandNameThink, commandNameSave:
		return true
	}
	return strings.HasPrefix(cmd, commandNameTaskQueueDel+" ") || strings.HasPrefix(cmd, commandNameModelSet+" ")
}

// ============================================================================
// Prompt Processing
// ============================================================================

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx)
	}

	if len(s.Messages) > 0 && s.Messages[len(s.Messages)-1].Role == llm.RoleUser {
		s.Messages[len(s.Messages)-1].Content = append(
			s.Messages[len(s.Messages)-1].Content,
			llm.TextPart{Type: "text", Text: prompt},
		)
	} else {
		s.Messages = append(s.Messages, llm.NewUserMessage(prompt))
	}

	_, err := s.processPrompt(ctx, s.Messages)

	s.Messages = cleanIncompleteToolCalls(s.Messages)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		return
	}
}

func (s *Session) shouldAutoSummarize() bool {
	return s.AutoSummarize && s.ContextLimit > 0 && s.ContextTokens.Load() > 0 &&
		s.ContextTokens.Load() >= s.ContextLimit*65/100
}

func (s *Session) doAutoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens.Load()) * 100 / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens.Load(), s.ContextLimit, usage)
	s.summarize(ctx)
}

func (s *Session) processPrompt(ctx context.Context, history []llm.Message) (int64, error) {
	// nextPromptID is goroutine-local (only accessed from the task goroutine),
	// so it's updated outside the mutex.
	s.nextPromptID++
	promptID := s.nextPromptID - 1

	var stepCount int
	var outputTokens int64

	assembleID := func(id string) string {
		return stream.NewStreamID(promptID, stepCount, id)
	}

	_, err := s.agent.Load().Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, stream.WrapDelta(assembleID(stream.SuffixText), delta))
			s.Output.Flush()
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, stream.WrapDelta(assembleID(stream.SuffixReasoning), delta))
			s.Output.Flush()
			return nil
		},
		OnToolCallStart: func(toolCallID, toolName string) error {
			s.writeToolCallStart(toolName, toolCallID)
			s.Output.Flush()
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			s.writeToolCall(toolName, string(input), toolCallID)
			s.Output.Flush()
			return nil
		},
		OnToolResult: func(toolCallID string, output llm.ToolResultOutput) error {
			status := "success"
			if textOutput, ok := output.(llm.ToolResultOutputText); ok {
				s.writeToolOutput(toolCallID, textOutput.Text)
			} else if errOutput, ok := output.(llm.ToolResultOutputError); ok {
				status = "error"
				s.writeToolOutput(toolCallID, errOutput.Error)
			}
			s.writeToolResult(toolCallID, status)
			return nil
		},
		OnStepStart: func(step int) error {
			stepCount = step

			// Send step start event to run().
			s.sendEvent(taskEvent{
				typ:  eventStepStart,
				step: step,
			})

			// Sync think level if it was changed during task execution.
			if s.thinkDirty.Load() {
				if p := s.provider.Load(); p != nil {
					if prov, ok := p.(llm.Provider); ok {
						prov.SetReasoningLevel(int(s.thinkLevel.Load()))
					}
				}
				s.thinkDirty.Store(false)
			}

			s.requestSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			// Update local working copy.
			if len(messages) > 0 {
				s.Messages = append(s.Messages, messages...)
			}

			// Send event to run() so it updates runMessages and totals.
			s.sendEvent(taskEvent{
				typ:                 eventStepFinish,
				messages:            messages,
				inputTokens:         usage.InputTokens,
				outputTokens:        usage.OutputTokens,
				cacheReadTokens:     usage.CacheReadTokens,
				cacheCreationTokens: usage.CacheCreationTokens,
			})

			outputTokens += usage.OutputTokens
			s.requestSystemInfo()
			return nil
		},
	})

	s.Output.Flush()

	if err != nil {
		return 0, err
	}

	return outputTokens, nil
}

// cleanIncompleteToolCalls removes incomplete tool calls from messages.
// An incomplete tool call is one whose ToolCallID has no matching
// ToolResultPart in any subsequent message.  This happens when the API
// errors mid-cycle.
func cleanIncompleteToolCalls(messages []llm.Message) []llm.Message {
	unmatchedCalls := make(map[string]bool)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case llm.ToolCallPart:
				unmatchedCalls[p.ToolCallID] = true
			case llm.ToolResultPart:
				delete(unmatchedCalls, p.ToolCallID)
			}
		}
	}

	if len(unmatchedCalls) == 0 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		hasUnmatchedCall := false
		for _, part := range msg.Content {
			if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
				hasUnmatchedCall = true
				break
			}
		}

		if hasUnmatchedCall {
			filteredParts := make([]llm.ContentPart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
					continue
				}
				filteredParts = append(filteredParts, part)
			}

			if len(filteredParts) > 0 {
				messages[i].Content = filteredParts
				return messages[:i+1]
			}
			messages = messages[:i]
			continue
		}

		return messages[:i+1]
	}

	return messages
}
