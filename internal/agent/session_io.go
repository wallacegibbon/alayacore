package agent

// Session I/O: input pump, command handling, prompt processing.
//
// All command dispatching happens in the run() goroutine via
// handleInputMsg.  The input pump is a pure TLV parser — it has no
// knowledge of command names and never touches session state.
// This keeps the design simple: one goroutine owns everything,
// no split-path exceptions for :cancel / :confirm / etc.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Command Handling
// ============================================================================

func (s *Session) handleCommand(ctx context.Context, cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return
	}

	if s.dispatchCommand(ctx, cmd) {
		return
	}

	s.writeError(domainerrors.NewSessionErrorf("command", "unknown cmd <%s>", parts[0]).Error())
}

func (s *Session) cancelTask() {
	if s.inProgress {
		if s.cancelRunningTask() {
			return
		}
	}
	s.writeError(domainerrors.ErrNothingToCancel.Error())
}

func (s *Session) cancelAllTasks() {
	queueLen := len(s.taskQueue)
	s.taskQueue = make([]QueueItem, 0)

	// Cancel the running task (if any) and clear the queue in one
	// place.  Everything runs in the run() goroutine — no split.
	currentCanceled := false
	if s.inProgress {
		if s.cancelRunningTask() {
			currentCanceled = true
		}
	}

	// Send notification
	switch {
	case currentCanceled && queueLen > 0:
		s.writeNotifyf("Canceled current task and cleared %d queued tasks", queueLen)
	case currentCanceled:
		s.writeNotify("Canceled current task")
	case queueLen > 0:
		s.writeNotifyf("Cleared %d queued tasks", queueLen)
	default:
		s.writeError(domainerrors.ErrNothingToCancel.Error())
		return
	}

	// Clear paused-on-error state so the queue can resume if needed
	s.pausedOnError.Store(false)

	s.requestSystemInfo()
}

// clearQueue removes all queued tasks without affecting the currently
// running task.  If the queue is already empty it reports an error.
func (s *Session) clearQueue() {
	if len(s.taskQueue) == 0 {
		s.writeError("Task queue is already empty")
		return
	}
	count := len(s.taskQueue)
	s.taskQueue = make([]QueueItem, 0)
	s.writeNotifyf("Cleared %d queued tasks", count)
	s.requestSystemInfo()
}

func (s *Session) handleContinue(ctx context.Context, tc *taskCtx, args []string) {
	// Validate arguments before doing anything.
	if len(args) > 0 && args[0] != "skip" {
		s.writeError("usage: :continue [skip]")
		return
	}

	s.pausedOnError.Store(false)

	// With no arguments, resend the last prompt.
	if len(args) == 0 {
		s.resendPrompt(ctx, tc)
		return
	}

	// "skip" — skip the failed prompt and resume the remaining queue.
	qLen := len(s.taskQueue)
	if qLen == 0 {
		s.writeNotify("Queue is empty")
		return
	}

	s.writeNotify("Resuming queue...")
	s.requestSystemInfo()
}

func (s *Session) summarize(ctx context.Context, tc *taskCtx) {
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

	tc.Messages = append(tc.Messages, llm.NewUserMessage(prompt))
	beforeCount := len(tc.Messages)

	s.writeNotify("Summarizing conversation...")

	result, newEntries, outputTokens, err := s.processPrompt(ctx, tc.Messages)
	tc.Entries = append(tc.Entries, newEntries...)

	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		tc.Messages = result
		return
	}

	var lastAssistantMsg llm.Message
	for i := beforeCount; i < len(result); i++ {
		if result[i].Role == llm.RoleAssistant {
			lastAssistantMsg = result[i]
		}
	}
	// Strip reasoning/thinking content from the summary message — it's
	// internal model deliberation, not summary content.
	filtered := make([]llm.ContentPart, 0, len(lastAssistantMsg.Content))
	for _, part := range lastAssistantMsg.Content {
		switch part.(type) {
		case *llm.ReasoningPart:
			continue
		default:
			filtered = append(filtered, part)
		}
	}
	lastAssistantMsg.Content = filtered
	// Prepend a "Continue" user message before the summary assistant message.
	result = []llm.Message{
		llm.NewUserMessage("Continue"),
		lastAssistantMsg,
	}
	// Rebuild entries to match the new message structure:
	// [UserMessage("Continue"), filteredAssistantMsg]
	// Assign new IDs since the old entries are being replaced.
	tc.Entries = tc.Entries[:0]
	continueID := s.histIncAndGet()
	tc.Entries = append(tc.Entries, &llm.TextPart{
		Text: "Continue",
		ContentMeta: llm.ContentMeta{
			HistoryID: continueID,
			Role:      llm.RoleUser,
		},
	})
	summaryID := s.histIncAndGet()
	firstPart := lastAssistantMsg.Content[0]
	tc.Entries = append(tc.Entries, firstPart.UpdateContentPartMeta(summaryID, llm.RoleAssistant))
	for i := 1; i < len(lastAssistantMsg.Content); i++ {
		part := lastAssistantMsg.Content[i]
		tc.Entries = append(tc.Entries, part.UpdateContentPartMeta(s.histIncAndGet(), llm.RoleAssistant))
	}

	if outputTokens > 0 {
		s.sendEvent(SetContextTokensEvent{Tokens: outputTokens})
	}

	tc.Messages = result
	s.writeNotify("Summarized conversation")
	s.requestSystemInfo()
}

func (s *Session) saveSession(args []string) {
	var path string
	switch len(args) {
	case 0:
		if s.SessionFile == "" {
			s.writeError(domainerrors.ErrNoSessionFile.Error())
			return
		}
		path = s.SessionFile
	case 1:
		path = config.ExpandPath(args[0])
	default:
		s.writeError("usage: :save [filename]")
		return
	}

	if err := s.saveContentToFile(path, s.Content); err != nil {
		s.writeError(domainerrors.Wrapf(CommandNameSave, domainerrors.ErrFailedToSaveSession, "%v", err).Error())
	} else {
		s.writeNotifyf("Session saved to %s", path)
	}
}

func (s *Session) handleModelSet(args []string) {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_set <id>")
		return
	}

	modelIDStr := args[0]
	modelID, err := strconv.Atoi(modelIDStr)
	if err != nil {
		s.writeError(domainerrors.NewSessionErrorf(CommandNameModelSet, "invalid model ID: %s", modelIDStr).Error())
		return
	}
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(domainerrors.Wrapf(CommandNameModelSet, domainerrors.ErrModelNotFound, "model not found: %d", modelID).Error())
		return
	}

	if err := s.ModelManager.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	// Persist the switch. Sessions with a file-specified model store the
	// preference in-memory (saved to the session file on :save), while
	// sessions without one write to the global runtime.conf.
	if s.SessionFile != "" {
		s.sessionMetaModel = model.Name
	} else if s.RuntimeManager != nil {
		_ = s.RuntimeManager.SetActiveModel(model.Name) //nolint:errcheck // best-effort save, errors ignored
	}

	if err := s.SwitchModel(model); err != nil {
		s.writeError("Failed to switch model: " + err.Error())
		return
	}

	s.writeNotifyf("Switched to model: %s (%s)", model.Name, model.ModelName)
}

func (s *Session) handleModelLoad() {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	path := s.ModelManager.GetFilePath()
	if path == "" {
		s.writeError(domainerrors.ErrNoModelFilePath.Error())
		return
	}

	if err := s.ModelManager.LoadFromFile(path); err != nil {
		s.writeError(domainerrors.Wrapf(CommandNameModelLoad, domainerrors.ErrFailedToLoadModels, "%v", err).Error())
		return
	}

	// Report validation messages (unknown protocol_type, missing fields, etc.)
	if msgs := s.ModelManager.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			s.writeError(m)
		}
	}

	// Re-resolve the active model using the standard priority chain:
	// runtime.conf → session file frontmatter → --model CLI flag.
	s.setActiveFromRuntimeConfig()
	s.setActiveFromSessionMeta()
	s.setActiveFromCliFlag()

	// Re-initialize the provider/agent with the (potentially edited)
	// model config. This ensures that changes made to the active model's
	// settings (base_url, api_key, model_name, etc.) take effect immediately.
	if model := s.ModelManager.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			s.writeError("Failed to reinitialize model after reload: " + err.Error())
		}
	}

	s.sendModelListMsg()
	s.writeNotify("Models reloaded from configuration file")
}

func (s *Session) handleTaskQueueGetAll() {
	s.sendSystemInfo("task")
}

func (s *Session) handleTaskQueueDel(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :taskqueue_del <queue_id>")
		return
	}

	queueID := args[0]
	if s.DeleteQueueItem(queueID) {
		s.sendSystemInfo("task")
	} else {
		s.writeError(domainerrors.Wrapf("taskqueue_del", domainerrors.ErrQueueItemNotFound, "queue item %s not found", queueID).Error())
	}
}

func (s *Session) handleTaskQueueEdit(args []string) {
	if len(args) < 2 {
		s.writeError("usage: :taskqueue_edit <queue_id> <new_content>")
		return
	}

	queueID := args[0]
	newContent := strings.Join(args[1:], " ")
	if s.UpdateQueueItem(queueID, newContent) {
		s.sendSystemInfo("task")
	} else {
		s.writeError(domainerrors.Wrapf("taskqueue_edit", domainerrors.ErrQueueItemNotFound, "queue item %s not found", queueID).Error())
	}
}

func (s *Session) handleReason(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :reason [0|1|2]  (0=off, 1=normal, 2=max)")
		return
	}
	level, err := strconv.Atoi(args[0])
	if err != nil || level < config.ReasoningLevelOff || level > config.ReasoningLevelMax {
		s.writeError("usage: :reason [0|1|2]  (0=off, 1=normal, 2=max)")
		return
	}
	s.SetReasoningLevel(level)
}

// handleThemeSet sets the active theme, persists it to runtime config,
// and sends updated system info so adapters receive the full theme data.
func (s *Session) handleThemeSet(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :theme_set <name>")
		return
	}
	name := args[0]

	// Validate that the theme exists before persisting.
	if s.ThemesFolder != "" {
		themePath := filepath.Join(s.ThemesFolder, name+".conf")
		if _, err := os.Stat(themePath); os.IsNotExist(err) {
			s.writeError(fmt.Sprintf("Theme %q not found", name))
			return
		}
	}

	if s.RuntimeManager != nil {
		_ = s.RuntimeManager.SetActiveTheme(name) //nolint:errcheck // best-effort save, errors ignored
	}
	s.writeNotifyf("Theme set to: %s", name)
	s.sendSystemInfo("theme")
}

// resendPrompt resends the conversation history to the LLM.
// This is called by handleContinue (no args) to resend the failed prompt.
//
// Three cases:
//  1. Latest message is a user prompt → re-send history as-is (the previous
//     API call never produced a response).
//  2. Latest message is a tool result → re-send history as-is. Tool results
//     are functionally equivalent to a user turn, so the LLM can respond
//     directly without an additional user message.
//  3. Latest message is an assistant message → the API partially succeeded
//     or was canceled. A "Continue" user message is appended so the
//     model picks up where it left off.
func (s *Session) resendPrompt(ctx context.Context, tc *taskCtx) {
	if len(tc.Messages) == 0 {
		s.writeError("No messages to resend")
		return
	}

	msgCount := len(tc.Messages)
	lastMsg := tc.Messages[msgCount-1]
	if lastMsg.Role == llm.RoleAssistant {
		tc.Messages = append(tc.Messages, llm.NewUserMessage("Continue"))
		id := s.histIncAndGet()
		tc.Entries = append(tc.Entries, &llm.TextPart{
			Text: "Continue",
			ContentMeta: llm.ContentMeta{
				HistoryID: id,
				Role:      llm.RoleUser,
			},
		})
		s.writeTLVStr(stream.TagUserT, stream.WrapDelta(strconv.FormatUint(id, 10), "Continue"))
	} else {
		s.writeNotify("Resending...")
	}

	result, newEntries, _, err := s.processPrompt(ctx, tc.Messages)
	tc.Entries = append(tc.Entries, newEntries...)

	result = cleanIncompleteToolUses(result)
	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		tc.Messages = result
		return
	}

	tc.Messages = result
	s.requestSystemInfo()
}

// ============================================================================
// Input Pump (reads TLV frames from input stream)
// ============================================================================

// inputMsg carries a parsed input message from the I/O pump to run().
type inputMsg struct {
	text    string   // the raw user text or command text (without ':')
	images  []string // image DataURIs from preceding UI tags
	isCmd   bool     // true if text starts with ':'
	errText string   // non-empty when the input pump hit a validation error
}

// inputPump runs in its own goroutine.  It reads TLV frames from the
// input stream, builds inputMsg values, and sends them to msgCh.
// It does NOT interpret commands or access session state — all of
// that lives in the run() goroutine.
func (s *Session) inputPump() {
	var pendingImages []string

	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			close(s.msgCh)
			return
		}

		switch tag {
		case stream.TagUserI:
			pendingImages = append(pendingImages, value)

		case stream.TagUserT:
			s.handleInputUserText(value, &pendingImages)

		default:
			if len(pendingImages) > 0 {
				pendingImages = nil
				s.msgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
					"image tag must be followed by another image or text, got: %s", tag).Error()}
			} else {
				s.msgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
					"invalid input tag: %s", tag).Error()}
			}
		}
	}
}

// handleInputUserText builds an inputMsg from the text value and any
// preceding images.  It strips the ':' prefix for commands and sets
// isCmd so run() can dispatch them.  The only validation is rejecting
// images followed by a command (attaching images to a command makes
// no sense).
func (s *Session) handleInputUserText(value string, pendingImages *[]string) {
	if len(*pendingImages) > 0 && len(value) > 0 && value[0] == ':' {
		// Images followed by a command is not allowed.
		*pendingImages = nil
		s.msgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
			"command can not attach images").Error()}
		return
	}

	msg := inputMsg{
		text:   value,
		images: *pendingImages,
	}
	*pendingImages = nil

	// Strip the colon prefix; run() uses isCmd to route to dispatchCommand.
	if len(value) > 0 && value[0] == ':' {
		msg.text = value[1:]
		msg.isCmd = true
	}

	s.msgCh <- msg
}

// handleFork saves all content from the start of the session up to (and
// including) the content identified by history ID to a session file.
// Usage: :fork <history_id> <filename>
func (s *Session) handleFork(args []string) {
	if len(args) < 2 {
		s.writeError("usage: :fork <history_id> <filename>")
		return
	}

	id, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		s.writeError(fmt.Sprintf("invalid history ID: %s", args[0]))
		return
	}

	// Find the index of the content with this history ID.
	var endIdx = -1
	for i, part := range s.Content {
		if part.GetHistoryID() == id {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		s.writeError(fmt.Sprintf("no content found with history ID %d", id))
		return
	}

	path := config.ExpandPath(args[1])
	if err := s.saveContentToFile(path, s.Content[:endIdx+1]); err != nil {
		s.writeError(fmt.Sprintf("failed to fork: %v", err))
		return
	}
	s.writeNotifyf("Session forked to %s (up to content ID %d)", path, id)
}

// handleConfirmCommand processes a `:confirm <id> yes|no` command.
// It writes the response to the task goroutine's pending confirmation
// channel.  Called from the run() goroutine via dispatchCommand.
func (s *Session) handleConfirmCommand(args []string) {
	if len(args) != 2 {
		s.writeError("usage: :confirm <id> yes|no")
		return
	}

	id := args[0]
	p := s.confirmCh.Load()
	if p == nil {
		s.writeError("No pending tool confirmation")
		return
	}

	var allowed bool
	switch args[1] {
	case "yes", "y":
		allowed = true
	case "no", "n":
		allowed = false
	default:
		s.writeError("usage: :confirm <id> yes|no")
		return
	}

	*p <- llm.ToolConfirmResponse{ID: id, Allowed: allowed}
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	// Deliver any validation error from the input pump first.
	if msg.errText != "" {
		s.writeError(msg.errText)
		return
	}
	if msg.isCmd {
		cmd := msg.text
		switch LookupSchedule(cmd) {
		case ScheduleImmediate:
			s.handleCommand(s.sessionCtx, cmd)
		case ScheduleIdle:
			if s.inProgress {
				s.writeError("Cannot run this command while a task is in progress. Please wait or cancel the current task.")
				return
			}
			s.handleCommand(s.sessionCtx, cmd)
		default:
			s.submitTaskCommand(cmd)
		}
	} else {
		s.submitTask(QueueItem{Type: TaskTypePrompt, Content: msg.text, Images: msg.images})
	}
}
