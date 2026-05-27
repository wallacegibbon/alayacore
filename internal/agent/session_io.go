package agent

// Session I/O: input pump, command handling, prompt processing.
//
// These methods are called from either the run() goroutine (for immediate
// commands) or the task goroutine (for deferred commands). The task
// goroutine sends state mutations via stateCh, and the run() goroutine
// owns taskQueue and s.Messages directly.

import (
	"context"
	"fmt"
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
	inProg := s.inProgress

	if inProg {
		if s.cancelRunningTask() {
			return
		}
	}
	s.writeError(domainerrors.ErrNothingToCancel.Error())
}

func (s *Session) cancelAllTasks() {
	queueLen := len(s.taskQueue)
	s.taskQueue = make([]QueueItem, 0)

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

	s.sendSystemInfo()
}

func (s *Session) handleContinue(ctx context.Context, messages []llm.Message, args []string) []llm.Message {
	// Validate arguments before doing anything.
	if len(args) > 0 && args[0] != "skip" {
		s.writeError("usage: :continue [skip]")
		return messages
	}

	s.pausedOnError.Store(false)

	// With no arguments, resend the last prompt.
	if len(args) == 0 {
		return s.resendPrompt(ctx, messages)
	}

	// "skip" — skip the failed prompt and resume the remaining queue.
	qLen := len(s.taskQueue)
	if qLen == 0 {
		s.writeNotify("Queue is empty")
		return messages
	}

	s.writeNotify("Resuming queue...")
	s.requestSystemInfo()
	return messages
}

func (s *Session) summarize(ctx context.Context, messages []llm.Message) []llm.Message {
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

	messages = append(messages, llm.NewUserMessage(prompt))
	beforeCount := len(messages)

	s.writeNotify("Summarizing conversation...")

	result, outputTokens, err := s.processPrompt(ctx, messages)
	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		return result
	}

	var lastAssistantMsg llm.Message
	for i := beforeCount; i < len(result); i++ {
		if result[i].Role == llm.RoleAssistant {
			lastAssistantMsg = result[i]
		}
	}
	result = []llm.Message{lastAssistantMsg}
	// Both events are sent from this same goroutine sequentially
	// (StepFinishEvent during processPrompt, then this correction).
	// The FIFO channel guarantees the run() goroutine processes
	// this correction after the StepFinishEvent, so ContextTokens
	// ends up at the summary size. Does not affect TotalSpent.
	if outputTokens > 0 {
		s.sendEvent(SetContextTokensEvent{Tokens: outputTokens})
	}

	s.writeNotify("Summarized conversation")
	s.requestSystemInfo()
	return result
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

	if err := s.saveSessionToFile(path); err != nil {
		s.writeError(domainerrors.Wrap(domainerrors.OpSave, fmt.Errorf("%w: %v", domainerrors.ErrFailedToSaveSession, err)).Error())
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

	inProg := s.inProgress

	if inProg {
		s.writeError("Cannot switch model while a task is running. Please wait or cancel the current task.")
		return
	}

	modelIDStr := args[0]
	modelID, err := strconv.Atoi(modelIDStr)
	if err != nil {
		s.writeError(domainerrors.NewSessionErrorf(domainerrors.OpModelSet, "invalid model ID: %s", modelIDStr).Error())
		return
	}
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(domainerrors.Wrapf(domainerrors.OpModelSet, domainerrors.ErrModelNotFound, "model not found: %d", modelID).Error())
		return
	}

	if err := s.ModelManager.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	if s.RuntimeManager != nil {
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
		s.writeError(domainerrors.Wrap(domainerrors.OpModelLoad, fmt.Errorf("%w: %v", domainerrors.ErrFailedToLoadModels, err)).Error())
		return
	}

	// Report validation messages (unknown protocol_type, missing fields, etc.)
	if msgs := s.ModelManager.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			s.writeError(m)
		}
	}

	s.initModelManager()
	s.modelsChanged = true
	s.sendSystemInfo()
	s.writeNotify("Models reloaded from configuration file")
}

func (s *Session) handleTaskQueueGetAll() {
	s.sendSystemInfo()
}

func (s *Session) handleTaskQueueDel(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :taskqueue_del <queue_id>")
		return
	}

	queueID := args[0]
	if s.DeleteQueueItem(queueID) {
		s.sendSystemInfo()
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
		s.sendSystemInfo()
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

// handleThemeSet sets the active theme and persists it to runtime config.
func (s *Session) handleThemeSet(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :theme_set <name>")
		return
	}
	if s.RuntimeManager != nil {
		_ = s.RuntimeManager.SetActiveTheme(args[0]) //nolint:errcheck // best-effort save, errors ignored
	}
	s.writeNotifyf("Theme set to: %s", args[0])
	s.sendSystemInfo()
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
//     or was canceled. A "Please continue." user message is appended so the
//     model picks up where it left off.
func (s *Session) resendPrompt(ctx context.Context, messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		s.writeError("No messages to resend")
		return messages
	}

	msgCount := len(messages)
	lastMsg := messages[msgCount-1]
	if lastMsg.Role == llm.RoleAssistant {
		// The last message is an assistant response (partial success,
		// cancel, or error mid-stream). Append a continuation prompt so the
		// model resumes naturally.
		messages = append(messages, llm.NewUserMessage("Please continue."))
		// Echo the inserted message to the adaptor so it is visible.
		s.signalPromptStart("Please continue.")
	} else {
		// If the last message is RoleUser or RoleTool, the conversation
		// history is already at a valid point for the LLM to respond — just
		// re-send as-is.
		s.writeNotify("Resending...")
	}

	result, _, err := s.processPrompt(ctx, messages)

	result = cleanIncompleteToolCalls(result)
	if err != nil {
		s.writeError(err.Error())
		s.pausedOnError.Store(true)
		s.requestSystemInfo()
		return result
	}

	s.requestSystemInfo()
	return result
}

// ============================================================================
// Input Pump (reads TLV frames from input stream)
// ============================================================================

// inputMsg carries a parsed input message from the I/O pump to run().
type inputMsg struct {
	text  string // the raw user text or command text (without ':')
	isCmd bool   // true if text starts with ':'
}

// inputPump runs in its own goroutine and reads TLV frames from the
// input stream. It sends parsed messages to msgCh. It does NOT access
// any session state directly; for :cancel / :cancel_all commands it sends
// to taskCancelCh (a buffered channel) which the task goroutine listens on.
func (s *Session) inputPump(msgCh chan<- inputMsg) {
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			close(msgCh)
			return
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag, "invalid input tag: %s", tag).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			if cmd == commandNameCancel || cmd == commandNameCancelAll {
				// Signal cancellation to the running task (non-blocking).
				// If no task is running, the signal is lost and the command
				// is forwarded to msgCh so run() can handle queue clearing
				// or report "nothing to cancel".
				canceled := s.cancelRunningTask()
				if canceled && cmd == commandNameCancel {
					// Task was running and was canceled — don't send to msgCh
					// to avoid a spurious "nothing to cancel" message.
					continue
				}
				// For cancel_all, always send to msgCh for queue clearing.
				// For cancel (when no task running), forward for error reporting.
			}
			msgCh <- inputMsg{text: cmd, isCmd: true}
		} else {
			msgCh <- inputMsg{text: value, isCmd: false}
		}
	}
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.isCmd {
		cmd := msg.text
		// Immediate commands are handled directly; deferred commands
		// go through the task queue.
		if IsImmediate(cmd) {
			s.handleCommand(context.Background(), cmd)
		} else {
			s.submitDeferredCommand(cmd)
		}
	} else {
		s.submitTask(UserPrompt{Text: msg.text})
	}
}
