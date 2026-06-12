package agent

// Session task execution: processing prompts through the agent loop,
// auto-summarization, and cleaning incomplete tool calls.
//
// The main event loop lives in session_loop.go.
// I/O (input pump, command dispatch) lives in session_io.go.

import (
	"context"
	"encoding/json"
	"strconv"

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
// Takes the current messages and entries, returns the updated values after processing.
func (s *Session) handleUserPrompt(ctx context.Context, messages []llm.Message, entries []llm.ContentPart, prompt string, images []string) ([]llm.Message, []llm.ContentPart) {
	if s.shouldAutoSummarize() {
		messages, entries = s.doAutoSummarize(ctx, messages, entries)
	}

	// Build content parts: images first, then text
	content := make([]llm.ContentPart, 0, 1+len(images))
	for _, img := range images {
		content = append(content, &llm.ImagePart{DataURL: img})
	}
	content = append(content, &llm.TextPart{Text: prompt})

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

	updatedMessages, newEntries, _, err := s.processPrompt(ctx, messages)

	entries = append(entries, newEntries...)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		// When cancel or error occurs before OnStepFinish sets updatedMessages,
		// updatedMessages is nil/empty and the user prompt would be lost on save.
		// Fall back to messages (which has the user prompt appended above)
		// so the UT is preserved in the session file alongside the cancel AT.
		if len(updatedMessages) == 0 {
			return messages, entries
		}
		return updatedMessages, entries
	}

	return updatedMessages, entries
}

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed AutoSummarizeThreshold of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	return s.AutoSummarize && s.ContextLimit > 0 && s.ContextTokens.Load() > 0 &&
		s.ContextTokens.Load() >= s.ContextLimit*AutoSummarizeThreshold/AutoSummarizePctBase
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context, messages []llm.Message, entries []llm.ContentPart) ([]llm.Message, []llm.ContentPart) {
	usage := float64(s.ContextTokens.Load()) * AutoSummarizePctBase / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens.Load(), s.ContextLimit, usage)
	return s.summarize(ctx, messages, entries)
}

// ============================================================================
// Prompt Processing
// ============================================================================

// writeTLVWithID formats the historyID and writes a TLV entry to the output stream.
func (s *Session) writeTLVWithID(tag string, historyID uint64, data string) {
	id := strconv.FormatUint(historyID, 10)
	s.writeTLV(tag, stream.WrapDelta(id, data))
}

//nolint:gocyclo // callback-heavy; extracting harms readability.
func (s *Session) processPrompt(ctx context.Context, history []llm.Message) ([]llm.Message, []llm.ContentPart, int64, error) {
	var outputTokens int64

	var updatedMessages []llm.Message
	var newEntries []llm.ContentPart

	lastProcessed := len(history) // track where the last step ended

	_, err := s.agent.Load().Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantT, historyID, delta)
			return nil
		},
		OnReasoningDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantR, historyID, delta)
			return nil
		},
		OnToolUseStart: func(toolCallID, toolName string, historyID uint64) error {
			data, _ := json.Marshal(stream.ToolUseData{ID: toolCallID, Name: toolName}) //nolint:errcheck
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolUseInput: func(toolCallID string, input json.RawMessage, historyID uint64) error {
			data, _ := json.Marshal(stream.ToolUseData{ID: toolCallID, Input: input}) //nolint:errcheck
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolUseOutput: func(toolCallID string, content []llm.ContentPart, err error, historyID uint64) error {
			contentJSON, err2 := serializeContentParts(content)
			if err2 != nil {
				contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
			}
			data, _ := json.Marshal(stream.ToolResultData{ //nolint:errcheck
				ID:      toolCallID,
				Output:  contentJSON,
				IsError: err != nil,
			})
			s.writeTLVWithID(stream.TagUserF, historyID, string(data))
			return nil
		},
		OnToolConfirm: func(requests []llm.ToolConfirmRequest) <-chan llm.ToolConfirmResponse {
			ch := make(chan llm.ToolConfirmResponse, len(requests))
			sc := chan<- llm.ToolConfirmResponse(ch)
			s.confirmCh.Store(&sc) // visible to input pump immediately

			for _, req := range requests {
				if s.toolConfirmSet != nil {
					if _, ok := s.toolConfirmSet[req.ToolName]; ok {
						// Must guarantee a response on ch even if output
						// is broken, otherwise the agent blocks forever.
						if s.outputBroken.Load() {
							ch <- llm.ToolConfirmResponse{ID: req.ID, Error: "output broken"}
						} else if err := stream.WriteSystemMsg(s.Output, stream.ToolConfirmMsg{ID: req.ID}); err != nil {
							s.markOutputBroken()
							ch <- llm.ToolConfirmResponse{ID: req.ID, Error: domainerrors.Wrap("tool", err).Error()}
						}
						continue
					}
				}
				ch <- llm.ToolConfirmResponse{ID: req.ID, Allowed: true}
			}

			return ch
		},
		OnStepStart: func(step int) error {
			s.sendEvent(StepStartEvent{Step: step})

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
			// Remove orphaned tool uses from the last message before capturing
			// new entries. This keeps both Messages and Content free of tool
			// calls that were emitted but never executed (cancel/error).
			messages = cleanIncompleteToolUses(messages)
			if len(messages) > 0 {
				updatedMessages = messages
			}

			// Only capture messages added since the last step to avoid
			// duplicating content across multi-step conversations.
			newMsgs := messages[lastProcessed:]
			lastProcessed = len(messages)
			for _, msg := range newMsgs {
				newEntries = append(newEntries, msg.Content...)
			}

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
		IDGen: s.histIncAndGet,
	})

	if err != nil {
		return updatedMessages, newEntries, 0, err
	}

	return updatedMessages, newEntries, outputTokens, nil
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

	// All tool uses in the last assistant message are orphaned — the agent
	// only completes a step after executing all tools. If the last message
	// has tool uses, the step was interrupted (cancel/error).
	filtered := make([]llm.ContentPart, 0, len(last.Content))
	for _, part := range last.Content {
		if _, ok := part.(*llm.ToolUsePart); ok {
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
