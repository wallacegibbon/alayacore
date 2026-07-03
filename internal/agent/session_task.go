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

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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
		contents = s.doAutoSummarize(ctx, contents)
	}

	// Assign history IDs, append to contents, and echo to output.
	for _, part := range parts {
		id := s.histIncAndGet()
		part.SetHistoryID(id)
		part.SetRole(llm.RoleUser)
		contents = append(contents, part)
		if tag, val, err := contentPartToTLV(part); err == nil && tag != "" {
			s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
		}
	}

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

// summarizeBackup saves a timestamped backup of the current session contents
// before summarization. Silently skips if no session file is configured.
func (s *Session) summarizeBackup(contents []llm.ContentPart) {
	if s.SessionFile == "" {
		return
	}
	ext := filepath.Ext(s.SessionFile)
	base := strings.TrimSuffix(s.SessionFile, ext)
	backupPath := fmt.Sprintf("%s-%s%s", base, time.Now().Format("20060102150405"), ext)
	if err := s.saveContentToFile(backupPath, contents); err != nil {
		s.writeNotifyf("Failed to create pre-summarize backup: %v", err)
	} else {
		s.writeNotifyf("Pre-summarize backup saved to %s", backupPath)
	}
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context, contents []llm.ContentPart) []llm.ContentPart {
	limit := s.ContextLimit
	usage := float64(s.ContextTokens) * AutoSummarizePctBase / float64(limit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, limit, usage)

	s.summarizeBackup(contents)
	s.writeNotify("Summarizing conversation...")

	// Echo the summarization prompt to output, same as
	// handleUserPrompt does for a normal prompt, but call processPrompt
	// directly to avoid mutual recursion.
	promptPart := &llm.TextPart{Text: summarizePrompt}
	id := s.histIncAndGet()
	promptPart.SetHistoryID(id)
	promptPart.SetRole(llm.RoleUser)
	contents = append(contents, promptPart)
	if tag, val, err := contentPartToTLV(promptPart); err == nil && tag != "" {
		s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
	}

	beforeLen := len(contents)
	fullContents, outputTokens, err := s.processPrompt(ctx, contents)
	if err != nil {
		s.writeError(err.Error())
		s.requestSystemInfo()
		return contents
	}

	return s.buildSummary(fullContents, beforeLen, outputTokens)
}

// buildSummary extracts assistant response parts from the LLM output,
// rebuilds contents as a "Continue" user message + filtered summary,
// and updates context tokens. Shared between doAutoSummarize and
// runSummarize.
func (s *Session) buildSummary(fullContents []llm.ContentPart, beforeLen int, outputTokens int64) []llm.ContentPart {
	var summaryParts []llm.ContentPart
	for _, part := range fullContents[beforeLen:] {
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

	contents := make([]llm.ContentPart, 0, 1+len(summaryParts))
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
	s.writeTLV(tag, tlv.WrapID(id, data))
}

// handleToolConfirm handles a tool confirmation request.
// It creates a buffered channel and either:
//   - Sends false immediately if output is broken (no map entry needed)
//   - Registers the channel in confirmChs map and sends the SM notification
//   - On SM write failure: removes the map entry and sends false
//
// The caller (agent goroutine) blocks on the returned channel until the
// user responds via :confirm command, which writes to the channel via
// handleConfirmCommand.
func (s *Session) handleToolConfirm(req llm.ToolConfirmRequest) <-chan bool {
	// Cap 2: normal flow uses one slot (handleConfirmCommand). The second
	// slot prevents deadlock when the error path (ch <- false) races with
	// a concurrent handleConfirmCommand write.
	ch := make(chan bool, 2)

	if s.outputBroken.Load() {
		ch <- false
		return ch
	}

	// Store in map first so the channel is findable the instant the
	// user sees the SM notification and responds.
	s.confirmMu.Lock()
	s.confirmChs[req.ID] = ch
	s.confirmMu.Unlock()

	if err := protocol.WriteSystemMsg(s.Output, protocol.ToolConfirmMsg{ID: req.ID}); err != nil {
		s.markOutputBroken()
		s.confirmMu.Lock()
		delete(s.confirmChs, req.ID)
		s.confirmMu.Unlock()
		ch <- false
	}

	return ch
}

// handleToolOutput serializes tool results and writes them to the output stream.
func (s *Session) handleToolOutput(toolCallID string, contents []llm.ContentPart, err error, historyID uint64) error {
	data, marshalErr := marshalToolOutputData(toolCallID, contents, err != nil)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal tool output: %w", marshalErr)
	}
	s.writeTLVWithID(tlv.TagUserF, historyID, string(data))
	return nil
}

// handleTextDelta streams assistant text deltas to the output.
func (s *Session) handleTextDelta(delta string, historyID uint64) error {
	s.writeTLVWithID(tlv.TagAssistantT, historyID, delta)
	return nil
}

// handleReasoningDelta streams assistant reasoning deltas to the output.
func (s *Session) handleReasoningDelta(delta string, historyID uint64) error {
	s.writeTLVWithID(tlv.TagAssistantR, historyID, delta)
	return nil
}

// handleToolInputStart marshals and writes a tool call start frame.
func (s *Session) handleToolInputStart(toolCallID, name string, historyID uint64) error {
	data, err := marshalToolInputData(toolCallID, name, nil)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input start: %w", err)
	}
	s.writeTLVWithID(tlv.TagAssistantF, historyID, string(data))
	return nil
}

// handleToolInputComplete marshals and writes a tool call input frame.
func (s *Session) handleToolInputComplete(toolCallID string, input json.RawMessage, historyID uint64) error {
	data, err := marshalToolInputData(toolCallID, "", input)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input complete: %w", err)
	}
	s.writeTLVWithID(tlv.TagAssistantF, historyID, string(data))
	return nil
}

// needsToolConfirm reports whether a tool requires user confirmation.
func (s *Session) needsToolConfirm(name string) bool {
	if s.toolConfirmSet == nil {
		return false
	}
	_, ok := s.toolConfirmSet[name]
	return ok
}

// handleStepStart handles the start of a new agent step.
func (s *Session) handleStepStart(step int) error {
	s.sendEvent(StepStartEvent{Step: step})
	s.requestSystemInfo()
	return nil
}

func (s *Session) processPrompt(ctx context.Context, history []llm.ContentPart) ([]llm.ContentPart, int64, error) {
	var fullContents []llm.ContentPart
	var outputTokens int64

	onStepFinish := func(contents []llm.ContentPart, usage llm.Usage) error {
		fullContents = cleanIncompleteToolInputs(contents)
		s.sendEvent(usageToStepFinishEvent(usage))
		outputTokens += usage.OutputTokens
		s.requestSystemInfo()
		return nil
	}

	_, err := s.Agent().Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta:         s.handleTextDelta,
		OnReasoningDelta:    s.handleReasoningDelta,
		OnToolInputStart:    s.handleToolInputStart,
		OnToolInputComplete: s.handleToolInputComplete,
		OnToolOutput:        s.handleToolOutput,
		OnToolConfirm:       s.handleToolConfirm,
		ToolNeedsConfirm:    s.needsToolConfirm,
		OnStepStart:         s.handleStepStart,
		OnStepFinish:        onStepFinish,
		IDGen:               s.histIncAndGet,
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

// runTask executes a prompt in its own goroutine.
func (s *Session) runTask(ctx context.Context, taskContent []llm.ContentPart, parts []llm.ContentPart) {
	contents := taskContent

	defer func() {
		s.taskResultCh <- contents
	}()

	s.requestSystemInfo()

	contents, _ = s.handleUserPrompt(ctx, contents, parts)

	if ctx.Err() == context.Canceled {
		contents = s.appendCancelMessage(contents)
	}
}

// runContinue constructs a "Continue" user prompt and processes it as
// a normal user message.  If the last message was from the assistant,
// a "Continue" text is appended; otherwise the last prompt is resent.
func (s *Session) runContinue(ctx context.Context, taskContent []llm.ContentPart) {
	contents := taskContent

	defer func() {
		s.taskResultCh <- contents
	}()

	if len(contents) == 0 {
		s.writeError("No messages to resend")
		return
	}

	lastPart := contents[len(contents)-1]
	if lastPart.GetRole() == llm.RoleAssistant {
		// Assistant message — LLM was interrupted mid-response.
		// Append "Continue" as a user message to tell it to pick up where it left off.
		contents, _ = s.handleUserPrompt(ctx, contents, []llm.ContentPart{
			&llm.TextPart{Text: "Continue"},
		})
	} else {
		// User or tool message — resend the conversation as-is.
		s.writeNotify("Resending...")
		fullContents, _, err := s.processPrompt(ctx, contents)
		if err != nil {
			s.writeError(err.Error())
			s.requestSystemInfo()
		}
		contents = fullContents
	}

	if ctx.Err() == context.Canceled {
		contents = s.appendCancelMessage(contents)
	}
}

// runSummarize constructs a summarization prompt and processes it.
// After the LLM responds, the conversation is replaced with a summary.
func (s *Session) runSummarize(ctx context.Context, taskContent []llm.ContentPart) {
	contents := taskContent

	defer func() {
		s.taskResultCh <- contents
	}()

	s.summarizeBackup(contents)
	s.writeNotify("Summarizing conversation...")

	beforeLen := len(contents)
	promptParts := []llm.ContentPart{&llm.TextPart{Text: summarizePrompt}}
	fullContents, outputTokens := s.handleUserPrompt(ctx, contents, promptParts)

	contents = s.buildSummary(fullContents, beforeLen, outputTokens)

	if ctx.Err() == context.Canceled {
		contents = s.appendCancelMessage(contents)
	}
}

// cancelMessage is inserted into the conversation history when a task
// is canceled by the user.
const cancelMessage = "Canceled"

func (s *Session) appendCancelMessage(contents []llm.ContentPart) []llm.ContentPart {
	id := s.histIncAndGet()
	contents = append(contents, &llm.TextPart{
		Text: cancelMessage,
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: id,
			Role:      llm.RoleAssistant,
		},
	})
	s.writeTLV(tlv.TagAssistantT, tlv.WrapID(strconv.FormatUint(id, 10), cancelMessage))
	return contents
}
