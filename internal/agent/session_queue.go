package agent

// Session task execution: runs prompts and LLM-requiring commands
// in their own goroutine, communicating results back via taskResultCh.

import (
	"context"
	"strconv"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// sendTaskResult sends the final contents back to the run() goroutine
// via taskResultCh when the task goroutine exits. Must be deferred at
// the start of each task runner (runTask, runContinue, runSummarize).
func (s *Session) sendTaskResult(contents *[]llm.ContentPart) {
	s.taskResultCh <- *contents
}

// runTask executes a prompt in its own goroutine.
func (s *Session) runTask(ctx context.Context, taskContent []llm.ContentPart, parts []llm.ContentPart) {
	contents := taskContent

	defer s.sendTaskResult(&contents)

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

	defer s.sendTaskResult(&contents)

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

	defer s.sendTaskResult(&contents)

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
	s.writeTLV(stream.TagAssistantT, stream.WrapDelta(strconv.FormatUint(id, 10), cancelMessage))
	return contents
}
