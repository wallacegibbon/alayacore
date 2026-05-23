package agent

// Session task execution: reading input, processing prompts,
// executing the agent loop with streaming callbacks.
//
// All methods in this file are called from the single run() goroutine
// (except inputPump which is a pure I/O goroutine that sends messages
// to run() and may call cancelCurrent for :cancel commands only).

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

// run is the single goroutine that owns all mutable session state.
// It reads input from the ChanInput channel and processes tasks sequentially.
func (s *Session) run() {
	defer close(s.runDone)
	defer s.sessionCancel()

	// Start the I/O pump goroutine — it reads TLV from the input and
	// sends parsed messages to run() for processing. It has NO access
	// to session state except cancelCurrent (for :cancel commands).
	msgCh := make(chan inputMsg, 100)
	go s.inputPump(msgCh)

	var currentTask context.CancelFunc

	for {
		// Check for context cancellation (input closed externally)
		if s.sessionCtx.Err() != nil {
			if currentTask != nil {
				currentTask()
			}
			return
		}

		// If a task is running (API call in progress), we block in
		// processTask. New input accumulates in msgCh. We drain the
		// channel after each step completes.
		if len(s.taskQueue) > 0 && s.sessionCtx.Err() == nil {
			item := s.taskQueue[0]
			s.taskQueue = s.taskQueue[1:]

			// Run the task with a cancellable context
			s.inProgress = true
			s.runTask(item)
			s.inProgress = len(s.taskQueue) > 0

			// After the task completes (or gets canceled), reset
			// cancelCurrent so the inputPump goroutine can't cancel
			// a stale context.
			s.cancelCurrent = nil
			currentTask = nil

			// Drain any pending cancel commands from the input buffer
			// before starting the next task.
			s.drainCancels(msgCh)

			// Send updated system info
			s.sendSystemInfo()
			continue
		}

		// No tasks — wait for input
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			s.handleInputMsg(msg)
		case <-s.sessionCtx.Done():
			return
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
// any session state except cancelCurrent (for :cancel commands).
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
			// Cancel commands need immediate action — cancel the current
			// task's context so the run() goroutine stops at the next
			// step boundary. This is the ONLY session state access from
			// this goroutine.
			cmd := value[1:]
			if cmd == commandNameCancel || cmd == commandNameCancelAll {
				if s.cancelCurrent != nil {
					s.cancelCurrent()
				}
			}
			msgCh <- inputMsg{text: cmd, isCmd: true}
		} else {
			msgCh <- inputMsg{text: value, isCmd: false}
		}
	}
}

// drainCancels reads from msgCh (non-blocking) and processes any pending
// cancel commands. This ensures cancellation is prompt even when the
// task processing loop doesn't select on msgCh.
func (s *Session) drainCancels(msgCh <-chan inputMsg) {
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			s.handleInputMsg(msg)
		default:
			return
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
	case commandNameCancel, commandNameCancelAll, commandNameModelLoad, commandNameTaskQueueGetAll, commandNameTaskQueueEdit, commandNameThink:
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
		s.pausedOnError = true
		s.sendSystemInfo()
		return
	}
}

func (s *Session) shouldAutoSummarize() bool {
	return s.AutoSummarize && s.ContextLimit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= s.ContextLimit*65/100
}

func (s *Session) doAutoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens) * 100 / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, s.ContextLimit, usage)
	s.summarize(ctx)
}

func (s *Session) processPrompt(ctx context.Context, history []llm.Message) (int64, error) {
	s.nextPromptID++
	promptID := s.nextPromptID - 1

	var stepCount int
	var outputTokens int64

	assembleID := func(id string) string {
		return stream.NewStreamID(promptID, stepCount, id)
	}

	_, err := s.agent.Stream(ctx, history, llm.StreamCallbacks{
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
			s.currentStep = step
			s.sendSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			s.trackUsage(usage)
			if len(messages) > 0 {
				s.Messages = append(s.Messages, messages...)
			}
			outputTokens += usage.OutputTokens
			s.autoSaveIfEnabled()
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
