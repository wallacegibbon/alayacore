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

// handleUserPrompt echoes the user prompt to output, appends it to
// both tc.Messages and tc.Entries, then runs the agent loop.
func (s *Session) handleUserPrompt(ctx context.Context, tc *taskCtx, prompt string, attachments []llm.ContentPart) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx, tc)
	}

	// Build content parts with history IDs and echo to output.
	// ContentParts are shared between messages and entries so both
	// representations refer to the same objects.
	contents := make([]llm.ContentPart, 0, 1+len(attachments))
	for _, att := range attachments {
		id := s.histIncAndGet()
		att.SetHistoryID(id)
		att.SetRole(llm.RoleUser)
		contents = append(contents, att)
		tc.Entries = append(tc.Entries, att)
		// Echo back using the correct TLV tag for the attachment type
		if tag, val, err := contentPartToTLV(att); err == nil && tag != "" {
			s.writeTLVStr(tag, stream.WrapDelta(strconv.FormatUint(id, 10), val))
		}
	}
	id := s.histIncAndGet()
	part := &llm.TextPart{Text: prompt, ContentMeta: llm.ContentMeta{HistoryID: id, Role: llm.RoleUser}}
	contents = append(contents, part)
	tc.Entries = append(tc.Entries, part)
	s.writeTLVStr(stream.TagUserT, stream.WrapDelta(strconv.FormatUint(id, 10), prompt))

	// Append to messages for the LLM request.  If the preceding message
	// is also from the user, merge to avoid duplicate user messages.
	if len(tc.Messages) > 0 && tc.Messages[len(tc.Messages)-1].Role == llm.RoleUser {
		tc.Messages[len(tc.Messages)-1].Contents = append(tc.Messages[len(tc.Messages)-1].Contents, contents...)
	} else {
		tc.Messages = append(tc.Messages, llm.Message{Role: llm.RoleUser, Contents: contents})
	}

	updatedMessages, newEntries, _, err := s.processPrompt(ctx, tc.Messages)
	tc.Entries = append(tc.Entries, newEntries...)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		// When cancel or error occurs before OnStepFinish sets updatedMessages,
		// updatedMessages is nil/empty and the user prompt would be lost on save.
		// Fall back to tc.Messages (which has the user prompt appended above)
		// so the UT is preserved in the session file alongside the cancel AT.
		if len(updatedMessages) > 0 {
			tc.Messages = updatedMessages
		}
		return
	}

	tc.Messages = updatedMessages
}

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed AutoSummarizeThreshold of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	limit := s.ContextLimit
	return s.AutoSummarize && limit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= limit*AutoSummarizeThreshold/AutoSummarizePctBase
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context, tc *taskCtx) {
	limit := s.ContextLimit
	usage := float64(s.ContextTokens) * AutoSummarizePctBase / float64(limit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, limit, usage)
	s.summarize(ctx, tc)
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

	_, err := s.agent.Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantT, historyID, delta)
			return nil
		},
		OnReasoningDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantR, historyID, delta)
			return nil
		},
		OnToolInputStart: func(toolCallID, toolName string, historyID uint64) error {
			data, _ := json.Marshal(stream.ToolInputData{ID: toolCallID, Name: toolName}) //nolint:errcheck
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolInputComplete: func(toolCallID string, input json.RawMessage, historyID uint64) error {
			data, _ := json.Marshal(stream.ToolInputData{ID: toolCallID, Input: input}) //nolint:errcheck
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolOutput: func(toolCallID string, contents []llm.ContentPart, err error, historyID uint64) error {
			contentJSON, err2 := serializeContentParts(contents)
			if err2 != nil {
				contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
			}
			data, _ := json.Marshal(stream.ToolOutputData{ //nolint:errcheck
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
			s.confirmCh.Store(&sc)

			for _, req := range requests {
				if s.outputBroken.Load() {
					ch <- llm.ToolConfirmResponse{ID: req.ID, Error: "output broken"}
				} else if err := stream.WriteSystemMsg(s.Output, stream.ToolConfirmMsg{ID: req.ID}); err != nil {
					s.markOutputBroken()
					ch <- llm.ToolConfirmResponse{ID: req.ID, Error: domainerrors.Wrap("tool", err).Error()}
				}
			}

			return ch
		},
		ToolNeedsConfirm: func(toolName string) bool {
			if s.toolConfirmSet == nil {
				return false
			}
			_, ok := s.toolConfirmSet[toolName]
			return ok
		},
		OnStepStart: func(step int) error {
			s.sendEvent(StepStartEvent{Step: step})
			s.requestSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			// Remove orphaned tool uses from the last message before capturing
			// new entries. This keeps both Messages and Contents free of tool
			// calls that were emitted but never executed (cancel/error).
			messages = cleanIncompleteToolInputs(messages)
			if len(messages) > 0 {
				updatedMessages = messages
			}

			// Only capture messages added since the last step to avoid
			// duplicating content across multi-step conversations.
			newMsgs := messages[lastProcessed:]
			lastProcessed = len(messages)
			for _, msg := range newMsgs {
				newEntries = append(newEntries, msg.Contents...)
			}

			s.sendEvent(usageToStepFinishEvent(usage))

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

// usageToStepFinishEvent converts an llm.Usage to a StepFinishEvent.
func usageToStepFinishEvent(usage llm.Usage) StepFinishEvent {
	return StepFinishEvent{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
	}
}

// cleanIncompleteToolInputs removes orphaned tool uses from the last
// message. This happens when the user cancels mid-cycle: the model
// emitted tool uses but the agent never executed them. Only the last
// message can have orphaned uses — earlier steps are already complete.
// If the last message becomes empty after stripping, it is removed.
func cleanIncompleteToolInputs(messages []llm.Message) []llm.Message {
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
	filtered := make([]llm.ContentPart, 0, len(last.Contents))
	for _, part := range last.Contents {
		if _, ok := part.(*llm.ToolInputPart); ok {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) > 0 {
		messages[len(messages)-1].Contents = filtered
		return messages
	}
	// The last message had nothing but orphaned tool calls — drop it.
	return messages[:len(messages)-1]
}
