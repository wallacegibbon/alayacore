package agent

// Session auto-summarization: automatically triggers :summarize when
// context usage exceeds 65% of the configured limit.
//
// Extracted from session_task.go to separate concerns.

import (
	"context"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Auto-Summarization
// ============================================================================

// handleUserPrompt processes a user prompt through the agent loop.
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

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed 65% of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	return s.AutoSummarize && s.ContextLimit > 0 && s.ContextTokens.Load() > 0 &&
		s.ContextTokens.Load() >= s.ContextLimit*65/100
}

// doAutoSummarize logs a notification and triggers summarization.
func (s *Session) doAutoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens.Load()) * 100 / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens.Load(), s.ContextLimit, usage)
	s.summarize(ctx)
}
