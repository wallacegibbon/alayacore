package agent

// Session task execution: processing prompts through the agent loop,
// auto-summarization, and cleaning incomplete tool calls.
//
// The main event loop lives in session_loop.go.
// I/O (input pump, command dispatch) lives in session_io.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
// contents, then runs the agent loop.
//
// parts is the combined user content (media parts + optional text part).
// For media-only messages, there is no text part — only media parts.
func (s *Session) handleUserPrompt(ctx context.Context, contents []llm.ContentPart, parts []llm.ContentPart) ([]llm.ContentPart, int64) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx, contents)
	}

	// Assign history IDs, append to contents, and echo to output.
	for _, part := range parts {
		id := s.histIncAndGet()
		part.SetHistoryID(id)
		part.SetRole(llm.RoleUser)
		contents = append(contents, part)
		if tag, val, err := contentPartToTLV(part); err == nil && tag != "" {
			s.writeTLV(tag, stream.WrapDelta(strconv.FormatUint(id, 10), val))
		}
	}

	// Signal end of user message so the adapter can group parts into one window.
	s.writeTLV(stream.TagMessageBoundary, "")

	fullContents, outputTokens, err := s.processPrompt(ctx, contents)
	if err != nil {
		s.writeError(err.Error())
		s.requestSystemInfo()
		return contents, 0
	}

	return fullContents, outputTokens
}

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed AutoSummarizeThreshold of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	limit := s.ContextLimit
	return s.AutoSummarize && limit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= limit*AutoSummarizeThreshold/AutoSummarizePctBase
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context, contents []llm.ContentPart) []llm.ContentPart {
	limit := s.ContextLimit
	usage := float64(s.ContextTokens) * AutoSummarizePctBase / float64(limit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, limit, usage)

	// Save a backup before summarization.
	if s.SessionFile != "" {
		ext := filepath.Ext(s.SessionFile)
		base := strings.TrimSuffix(s.SessionFile, ext)
		backupPath := fmt.Sprintf("%s-%s%s", base, time.Now().Format("20060102150405"), ext)
		if err := s.saveContentToFile(backupPath, contents); err != nil {
			s.writeNotifyf("Failed to create pre-summarize backup: %v", err)
		} else {
			s.writeNotifyf("Pre-summarize backup saved to %s", backupPath)
		}
	}

	prompt := `Summarize the conversation for continuation. The resuming instance has no prior context.

Provide:
1. **Task** — Original request and success criteria
2. **Done** — Completed items with specifics (file paths, function names, values)
3. **State** — Files created/modified/deleted, key decisions and rationale
4. **Blocked** — Unresolved errors, failing tests, open questions
5. **Next** — Ordered actions to resume

Rules:
- Prefer exact identifiers, file paths, and code snippets over prose descriptions
- Include error messages verbatim
- Skip completed exploration; only preserve findings that affect next steps`

	s.writeNotify("Summarizing conversation...")

	// Append the summarization prompt and process.
	beforeLen := len(contents)
	contents, outputTokens := s.handleUserPrompt(ctx, contents,
		[]llm.ContentPart{&llm.TextPart{Text: prompt}})

	// Find assistant content parts in the newly added content.
	var summaryParts []llm.ContentPart
	for _, part := range contents[beforeLen:] {
		if part.GetRole() != llm.RoleAssistant {
			continue
		}
		switch part.(type) {
		case *llm.ReasoningPart:
			continue
		default:
			summaryParts = append(summaryParts, part)
		}
	}

	// Rebuild contents: "Continue" user message + filtered summary.
	contents = contents[:0]
	continueID := s.histIncAndGet()
	contents = append(contents, &llm.TextPart{
		Text: "Continue",
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: continueID,
			Role:      llm.RoleUser,
		},
	})
	for _, part := range summaryParts {
		part.UpdateContentPartMeta(s.histIncAndGet(), llm.RoleAssistant)
		contents = append(contents, part)
	}

	if outputTokens > 0 {
		s.sendEvent(SetContextTokensEvent{Tokens: outputTokens})
	}

	s.writeNotify("Summarized conversation")
	s.requestSystemInfo()
	return contents
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

	var fullContents []llm.ContentPart

	_, err := s.agent.Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantT, historyID, delta)
			return nil
		},
		OnReasoningDelta: func(delta string, historyID uint64) error {
			s.writeTLVWithID(stream.TagAssistantR, historyID, delta)
			return nil
		},
		OnToolInputStart: func(toolCallID, name string, historyID uint64) error {
			data, err := json.Marshal(stream.ToolInputData{ID: toolCallID, Name: name})
			if err != nil {
				return fmt.Errorf("failed to marshal tool input start: %w", err)
			}
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolInputComplete: func(toolCallID string, input json.RawMessage, historyID uint64) error {
			data, err := json.Marshal(stream.ToolInputData{ID: toolCallID, Input: input})
			if err != nil {
				return fmt.Errorf("failed to marshal tool input complete: %w", err)
			}
			s.writeTLVWithID(stream.TagAssistantF, historyID, string(data))
			return nil
		},
		OnToolOutput: func(toolCallID string, contents []llm.ContentPart, err error, historyID uint64) error {
			contentJSON, serErr := serializeContentParts(contents)
			if serErr != nil {
				contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
			}
			data, marshalErr := json.Marshal(stream.ToolOutputData{
				ID:      toolCallID,
				Output:  contentJSON,
				IsError: err != nil,
			})
			if marshalErr != nil {
				return fmt.Errorf("failed to marshal tool output: %w", marshalErr)
			}
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
		ToolNeedsConfirm: func(name string) bool {
			if s.toolConfirmSet == nil {
				return false
			}
			_, ok := s.toolConfirmSet[name]
			return ok
		},
		OnStepStart: func(step int) error {
			s.sendEvent(StepStartEvent{Step: step})
			s.requestSystemInfo()
			return nil
		},
		OnStepFinish: func(contents []llm.ContentPart, usage llm.Usage) error {
			fullContents = cleanIncompleteToolInputs(contents)
			s.sendEvent(usageToStepFinishEvent(usage))
			outputTokens += usage.OutputTokens
			s.requestSystemInfo()
			return nil
		},
		IDGen: s.histIncAndGet,
	})

	if err != nil {
		return fullContents, 0, err
	}

	return fullContents, outputTokens, nil
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
