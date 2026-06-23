package agent

// Session task execution: runs prompts and LLM-requiring commands
// in their own goroutine, communicating results back via taskResultCh.

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

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

	beforeLen := len(contents)
	promptParts := []llm.ContentPart{&llm.TextPart{Text: prompt}}
	fullContents, outputTokens := s.handleUserPrompt(ctx, contents, promptParts)

	// Find assistant content parts in the newly added content.
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
	s.writeTLV(stream.TagAssistantT, stream.WrapDelta(strconv.FormatUint(id, 10), cancelMessage))
	return contents
}
