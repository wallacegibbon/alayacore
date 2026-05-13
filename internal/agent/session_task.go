package agent

// Session task execution: reading input, processing prompts,
// executing the agent loop with streaming callbacks.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Input Processing
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

func (s *Session) readFromInput() {
	defer func() {
		s.sessionCancel()
		s.cond.Signal()
	}()
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag, "invalid input tag: %s", tag).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			if isCommandImmediate(cmd) {
				s.handleCommand(context.Background(), cmd)
			} else {
				s.submitDeferredCommand(cmd)
			}
		} else {
			s.submitTask(UserPrompt{Text: value})
		}
	}
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
		s.mu.Lock()
		s.pausedOnError = true
		s.mu.Unlock()
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
	promptID := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	assembleID := func(id string) string {
		return stream.NewStreamID(promptID, stepCount, id)
	}

	_, err := s.Agent.Stream(ctx, history, llm.StreamCallbacks{
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
			s.mu.Lock()
			s.currentStep = step
			s.mu.Unlock()
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
