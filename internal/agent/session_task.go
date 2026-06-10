package agent

// Session task execution: processing prompts through the agent loop,
// auto-summarization, and cleaning incomplete tool calls.
//
// The main event loop lives in session_loop.go.
// I/O (input pump, command dispatch) lives in session_io.go.

import (
	"context"
	"encoding/json"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
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
func (s *Session) handleUserPrompt(ctx context.Context, messages []llm.Message, prompt string, images []string) []llm.Message {
	if s.shouldAutoSummarize() {
		messages = s.doAutoSummarize(ctx, messages)
	}

	// Build content parts: images first, then text
	content := make([]llm.ContentPart, 0, 1+len(images))
	for _, img := range images {
		content = append(content, llm.ImagePart{DataURL: img})
	}
	content = append(content, llm.TextPart{Text: prompt})

	if len(messages) > 0 && messages[len(messages)-1].Role == llm.RoleUser {
		messages[len(messages)-1].Content = append(
			messages[len(messages)-1].Content,
			content...,
		)
	} else {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: content,
		})
	}

	result, _, err := s.processPrompt(ctx, messages)

	result = cleanIncompleteToolUses(result)

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
		OnTextDelta: func(delta string, index int) error {
			_ = stream.WriteTLV(s.Output, stream.TagAssistantT, stream.WrapDelta(stream.NewStreamID(promptID, stepCount, index), delta)) //nolint:errcheck // best-effort write to adapter
			return nil
		},
		OnReasoningDelta: func(delta string, index int) error {
			_ = stream.WriteTLV(s.Output, stream.TagAssistantR, stream.WrapDelta(stream.NewStreamID(promptID, stepCount, index), delta)) //nolint:errcheck // best-effort write to adapter
			return nil
		},
		OnToolUseStart: func(id, toolName string) error {
			s.writeToolUseStart(toolName, id)
			return nil
		},
		OnToolUseInput: func(id string, input json.RawMessage) error {
			s.writeToolUseInput(input, id)
			return nil
		},
		OnToolUseOutput: func(id string, content []llm.ContentPart, err error) error {
			if err != nil {
				s.writeToolUseOutput(id, content, true)
			} else {
				s.writeToolUseOutput(id, content, false)
			}
			return nil
		},
		OnToolConfirm: func(requests []llm.ToolConfirmRequest) <-chan llm.ToolConfirmResponse {
			ch := make(chan llm.ToolConfirmResponse, len(requests))

			for _, req := range requests {
				if s.toolConfirmSet != nil {
					if _, ok := s.toolConfirmSet[req.ToolName]; ok {
						// Needs user confirmation — send prompt to adapter.
						if err := stream.WriteSystemMsg(s.Output, stream.ToolConfirmMsg{ID: req.ID}); err != nil {
							ch <- llm.ToolConfirmResponse{ID: req.ID, Error: domainerrors.Wrap("tool", err).Error()}
						}
						continue
					}
				}
				// No confirmation needed — auto-confirm immediately.
				ch <- llm.ToolConfirmResponse{ID: req.ID, Allowed: true}
			}

			s.confirmCh = ch
			return ch
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

// cleanIncompleteToolUses removes orphaned tool uses from the last
// message. This happens when the user cancels mid-cycle: the model
// emitted tool uses but the agent never executed them. Only the last
// message can have orphaned uses — earlier steps are already complete.
// If the last message becomes empty after stripping, it is removed.
func cleanIncompleteToolUses(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	last := messages[len(messages)-1]

	// Only assistant messages carry tool calls.
	if last.Role != llm.RoleAssistant {
		return messages
	}

	// Tool calls in the last message are always orphaned — the agent
	// only stops after executing all tool calls from a completed step.
	filtered := make([]llm.ContentPart, 0, len(last.Content))
	for _, part := range last.Content {
		if _, ok := part.(llm.ToolUsePart); ok {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) > 0 {
		messages[len(messages)-1].Content = filtered
		return messages
	}
	// The last message had nothing but orphaned tool calls — drop it.
	return messages[:len(messages)-1]
}
