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
// tc.Contents, then runs the agent loop.
func (s *Session) handleUserPrompt(ctx context.Context, tc *taskCtx, prompt string, attachments []llm.ContentPart) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx, tc)
	}

	// Assign history IDs, append to tc.Contents, and echo to output.
	for _, att := range attachments {
		id := s.histIncAndGet()
		att.SetHistoryID(id)
		att.SetRole(llm.RoleUser)
		tc.Contents = append(tc.Contents, att)
		if tag, val, err := contentPartToTLV(att); err == nil && tag != "" {
			s.writeTLVStr(tag, stream.WrapDelta(strconv.FormatUint(id, 10), val))
		}
	}
	id := s.histIncAndGet()
	part := &llm.TextPart{Text: prompt, ContentPartMeta: llm.ContentPartMeta{HistoryID: id, Role: llm.RoleUser}}
	tc.Contents = append(tc.Contents, part)
	s.writeTLVStr(stream.TagUserT, stream.WrapDelta(strconv.FormatUint(id, 10), prompt))

	newContents, _, err := s.processPrompt(ctx, tc.Contents)
	tc.Contents = append(tc.Contents, newContents...)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		return
	}
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
func (s *Session) processPrompt(ctx context.Context, history []llm.ContentPart) ([]llm.ContentPart, int64, error) {
	var outputTokens int64

	var newContents []llm.ContentPart

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
		OnStepFinish: func(contents []llm.ContentPart, usage llm.Usage) error {
			// Remove orphaned tool uses from the end before capturing
			// new content. This keeps Contents free of tool
			// calls that were emitted but never executed (cancel/error).
			contents = cleanIncompleteToolInputs(contents)

			// Only capture content parts added since the last step to avoid
			// duplicating content across multi-step conversations.
			newParts := contents[lastProcessed:]
			lastProcessed = len(contents)
			newContents = append(newContents, newParts...)

			s.sendEvent(usageToStepFinishEvent(usage))

			outputTokens += usage.OutputTokens
			s.requestSystemInfo()
			return nil
		},
		IDGen: s.histIncAndGet,
	})

	if err != nil {
		return newContents, 0, err
	}

	return newContents, outputTokens, nil
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

// cleanIncompleteToolInputs removes orphaned tool uses from the end of
// the content slice. This happens when the user cancels mid-cycle: the model
// emitted tool uses but the agent never executed them. Only the most recent
// assistant content parts can have orphaned uses — earlier steps are already
// complete.
func cleanIncompleteToolInputs(contents []llm.ContentPart) []llm.ContentPart {
	if len(contents) == 0 {
		return contents
	}

	// Find the last assistant segment and remove ToolInputParts from it.
	// Work backwards: find where the last batch of assistant parts starts.
	lastIdx := len(contents) - 1
	for lastIdx >= 0 && contents[lastIdx].GetRole() != llm.RoleAssistant {
		lastIdx--
	}
	if lastIdx < 0 {
		return contents
	}

	// If there are ToolOutputParts after the last assistant segment,
	// the tools were actually executed — keep everything.
	for _, part := range contents[lastIdx+1:] {
		if _, ok := part.(*llm.ToolOutputPart); ok {
			return contents
		}
	}

	// Find the start of this assistant segment
	startIdx := lastIdx
	for startIdx > 0 && contents[startIdx-1].GetRole() == llm.RoleAssistant {
		startIdx--
	}

	// Check if any tool calls in this segment
	hasToolCalls := false
	for _, part := range contents[startIdx : lastIdx+1] {
		if _, ok := part.(*llm.ToolInputPart); ok {
			hasToolCalls = true
			break
		}
	}
	if !hasToolCalls {
		return contents
	}

	// Filter out ToolInputParts from the last assistant segment
	filtered := make([]llm.ContentPart, 0, len(contents))
	filtered = append(filtered, contents[:startIdx]...)
	for _, part := range contents[startIdx : lastIdx+1] {
		if _, ok := part.(*llm.ToolInputPart); !ok {
			filtered = append(filtered, part)
		}
	}
	filtered = append(filtered, contents[lastIdx+1:]...)

	return filtered
}
