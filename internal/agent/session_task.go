package agent

// Session task execution: processing prompts through the agent loop,
// auto-summarization, and cleaning incomplete tool calls.
//
// The main event loop lives in session_loop.go.
// I/O (input pump, command dispatch) lives in session_io.go.

import (
	"context"
	"encoding/json"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// Auto-summarization threshold constants.
const (
	// AutoSummarizeThreshold is the context usage percentage at which
	// auto-summarization is triggered (65% of context limit).
	AutoSummarizeThreshold = 65

	// AutoSummarizePctBase is the base for percentage calculations (100%).
	AutoSummarizePctBase = 100
)

// ============================================================================
// User Prompt
// ============================================================================

// handleUserPrompt processes a user prompt through the agent loop.
// Takes the current messages and returns the updated messages after processing.
func (s *Session) handleUserPrompt(ctx context.Context, messages []llm.Message, prompt string) []llm.Message {
	if s.shouldAutoSummarize() {
		messages = s.doAutoSummarize(ctx, messages)
	}

	if len(messages) > 0 && messages[len(messages)-1].Role == llm.RoleUser {
		messages[len(messages)-1].Content = append(
			messages[len(messages)-1].Content,
			llm.TextPart{Type: "text", Text: prompt},
		)
	} else {
		messages = append(messages, llm.NewUserMessage(prompt))
	}

	result, _, err := s.processPrompt(ctx, messages)

	result = cleanIncompleteToolCalls(result)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		// When cancel or error occurs before OnStepFinish sets processResult,
		// result is nil/empty and the user prompt would be lost on save.
		// Fall back to messages (which has the user prompt appended above)
		// so the UT is preserved in the session file alongside the cancel AT.
		if len(result) == 0 {
			return messages
		}
		return result
	}

	return result
}

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed AutoSummarizeThreshold of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	return s.AutoSummarize && s.ContextLimit > 0 && s.ContextTokens.Load() > 0 &&
		s.ContextTokens.Load() >= s.ContextLimit*AutoSummarizeThreshold/AutoSummarizePctBase
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context, messages []llm.Message) []llm.Message {
	usage := float64(s.ContextTokens.Load()) * AutoSummarizePctBase / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens.Load(), s.ContextLimit, usage)
	return s.summarize(ctx, messages)
}

// ============================================================================
// Prompt Processing
// ============================================================================

func (s *Session) processPrompt(ctx context.Context, history []llm.Message) ([]llm.Message, int64, error) {
	// nextPromptID is goroutine-local (only accessed from the task goroutine),
	// so it's updated outside the mutex.
	s.nextPromptID++
	promptID := s.nextPromptID - 1

	var stepCount int
	var outputTokens int64

	// processResult captures the final message state from the agent.
	// It is set by OnStepFinish and returned to the caller.
	var processResult []llm.Message

	_, err := s.agent.Load().Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, stream.WrapDelta(stream.NewStreamID(promptID, stepCount), delta)) //nolint:errcheck // best-effort write to adaptor
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, stream.WrapDelta(stream.NewStreamID(promptID, stepCount), delta)) //nolint:errcheck // best-effort write to adaptor
			return nil
		},
		OnToolCallStart: func(toolCallID, toolName string) error {
			s.writeToolCallStart(toolName, toolCallID)
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			s.writeToolCall(toolName, string(input), toolCallID)
			return nil
		},
		OnToolResult: func(toolCallID string, output llm.ToolResultOutput) error {
			status := "success" //nolint:goconst // pre-existing lint, used in writeToolOutput
			if textOutput, ok := output.(llm.ToolResultOutputText); ok {
				s.writeToolOutput(toolCallID, textOutput.Text, status)
			} else if errOutput, ok := output.(llm.ToolResultOutputFailed); ok {
				status = "failed"
				s.writeToolOutput(toolCallID, errOutput.Reason, status)
			}
			return nil
		},
		OnStepStart: func(step int) error {
			stepCount = step

			// Send step start event to run().
			s.sendEvent(StepStartEvent{
				Step: step,
			})

			// Sync reasoning level if it was changed during task execution.
			if s.reasoningDirty.Load() {
				if p := s.provider.Load(); p != nil {
					(*p).SetReasoningLevel(int(s.reasoningLevel.Load()))
				}
				s.reasoningDirty.Store(false)
			}

			s.requestSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			// messages is allMessages (full history) from the agent.
			// Capture the final state so we can return it to the caller.
			// The task goroutine's caller owns the authoritative copy;
			// we never write to s.Messages here.
			if len(messages) > 0 {
				processResult = messages
			}

			// Send event to run() so it updates token totals.
			s.sendEvent(StepFinishEvent{
				InputTokens:         usage.InputTokens,
				OutputTokens:        usage.OutputTokens,
				CacheReadTokens:     usage.CacheReadTokens,
				CacheCreationTokens: usage.CacheCreationTokens,
			})

			outputTokens += usage.OutputTokens
			s.requestSystemInfo()
			return nil
		},
	})

	if err != nil {
		return processResult, 0, err
	}

	return processResult, outputTokens, nil
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
