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

func (s *Session) cancelTask() {
	if s.activeTask != nil {
		if s.cancelRunningTask() {
			return
		}
	}
	s.writeError(domainerrors.ErrNothingToCancel.Error())
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

	if err := s.saveContentToFile(path, s.Contents); err != nil {
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
		if err := s.RuntimeManager.SetActiveModel(model.Name); err != nil {
			s.writeNotifyf("Failed to persist model switch: %v", err)
		}
	}

	if err := s.SwitchModel(model); err != nil {
		s.writeError("Failed to switch model: " + err.Error())
		return
	}
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
}

func (s *Session) handleReason(args []string) {
	level, err := strconv.Atoi(args[0])
	if err != nil || level < config.ReasoningLevelOff || level > config.ReasoningLevelMax {
		s.writeError("usage: :reason [0|1|2]  (0=off, 1=normal, 2=max)")
		return
	}
	s.SetReasoningLevel(level)
}

// handleVideoConfig sets the default video FPS and resolution for video attachments.
// Usage: :video_config <fps> <resolution>
//
//	fps:        frames per second (positive integer, e.g. 2)
//	resolution: 0=default, 1=max
func (s *Session) handleVideoConfig(args []string) {
	if len(args) < 2 {
		s.writeError("usage: :video_config <fps> <resolution>  (resolution: 0=default, 1=max)")
		return
	}
	fps, err := strconv.Atoi(args[0])
	if err != nil || fps < 1 {
		s.writeError("usage: :video_config <fps> <resolution>  (fps must be a positive integer)")
		return
	}
	res, err := strconv.Atoi(args[1])
	if err != nil || res < 0 || res > 1 {
		s.writeError("usage: :video_config <fps> <resolution>  (resolution: 0=default, 1=max)")
		return
	}
	s.SetVideoConfig(fps, res)
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
		if err := s.RuntimeManager.SetActiveTheme(name); err != nil {
			s.writeNotifyf("Failed to persist theme switch: %v", err)
		}
	}
	s.sendSystemInfo("theme")
}

// resendPrompt resends the conversation history to the LLM.
// This is called by handleContinue (no args) to resend the failed prompt.
//
// Three cases based on the role of the last content part:
//  1. User prompt → re-send history as-is (the previous API call never produced a response).
//  2. Tool result → re-send history as-is. Tool results are functionally
//     equivalent to a user turn, so the LLM can respond directly.
//  3. Assistant message → the API partially succeeded or was canceled.
//     A "Continue" user message is appended so the model picks up where it left off.
// ============================================================================

// inputMsg carries a parsed input message from the I/O pump to run().
//
// For prompt messages:
//   - contentParts holds the combined message (media parts + optional text part)
//   - isCmd is false, cmd is empty
//
// For command messages:
//   - cmd holds the command text (without ':' prefix)
//   - contentParts is nil, isCmd is true
type inputMsg struct {
	contentParts []llm.ContentPart // combined user content (media + text)
	cmd          string            // command text for commands, empty for prompts
	isCmd        bool              // true when cmd is set
	errText      string            // non-empty when the input pump hit a validation error
}

// inputPump runs in its own goroutine.  It reads TLV frames from the
// input stream, builds inputMsg values, and sends them to inputMsgCh.
// It does NOT interpret commands or access session state — all of
// that lives in the run() goroutine.
func (s *Session) inputPump() {
	var staged []llm.ContentPart

	// flushStaged sends all staged content as a prompt.
	// Called on MB tag or EOF.
	flushStaged := func() {
		if len(staged) > 0 {
			msg := inputMsg{contentParts: staged}
			staged = nil
			s.inputMsgCh <- msg
		}
	}

	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			// Flush any staged content before closing on EOF.
			flushStaged()
			close(s.inputMsgCh)
			return
		}

		switch tag {
		case stream.TagUserI:
			staged = append(staged, &llm.ImagePart{URI: value})
		case stream.TagUserV:
			staged = append(staged, &llm.VideoPart{URI: value})
		case stream.TagUserA:
			staged = append(staged, &llm.AudioPart{URI: value})
		case stream.TagUserD:
			staged = append(staged, &llm.DocumentPart{URI: value})
		case stream.TagUserT:
			s.handleInputUserText(value, &staged)
		case stream.TagMessageBoundary:
			flushStaged()

		default:
			if len(staged) > 0 {
				staged = nil
				s.inputMsgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
					"unexpected tag while content is staged: %s", tag).Error()}
			} else {
				s.inputMsgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
					"invalid input tag: %s", tag).Error()}
			}
		}
	}
}

// handleInputUserText handles a UT (user text) tag from the input pump.
//
// All content — both media (UI/UV/UA/UD) and text — is staged until
// an MB (message boundary) tag or EOF flushes it.  Commands (starting
// with ':') are sent immediately when there is no staged content; a
// command with staged content is rejected.
func (s *Session) handleInputUserText(value string, staged *[]llm.ContentPart) {
	if len(value) > 0 && value[0] == ':' {
		// Command with staged content is rejected.
		if len(*staged) > 0 {
			*staged = nil
			s.inputMsgCh <- inputMsg{errText: domainerrors.Wrapf("input", domainerrors.ErrInvalidInputTag,
				"command can not be sent with staged content").Error()}
			return
		}
		// Command without staged content — send as-is.
		s.inputMsgCh <- inputMsg{
			isCmd: true,
			cmd:   value[1:],
		}
		return
	}

	// Regular text — stage it.  Content stays staged until MB or EOF.
	if value != "" {
		*staged = append(*staged, &llm.TextPart{Text: value})
	}
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
	for i, part := range s.Contents {
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
	if err := s.saveContentToFile(path, s.Contents[:endIdx+1]); err != nil {
		s.writeError(fmt.Sprintf("failed to fork: %v", err))
		return
	}
	s.writeNotifyf("Session forked to %s (up to content ID %d)", path, id)
}

// handleConfirmCommand processes a `:confirm <id> yes|no` command.
// It writes the response to the task goroutine's pending confirmation
// channel.  Called from the run() goroutine via the command registry.
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

// startTaskOrCmd starts a task goroutine for an unrecognized command.
// text is the full raw input; name is the first word for routing.
// :continue and :summarize run their own handlers; everything else is
// treated as a normal prompt with the raw text as content.
// Known commands like :continue and :summarize are routed to specific
// handlers; all others are treated as a normal prompt with the command
// text as content.
func (s *Session) startTaskOrCmd(text, name string) {
	if s.activeTask != nil {
		s.writeError("Cannot run command while a task is running. Please wait or cancel first.")
		return
	}
	if err := s.ensureAgentInitialized(); err != nil {
		s.writeError(err.Error())
		return
	}
	taskContent := make([]llm.ContentPart, len(s.Contents))
	copy(taskContent, s.Contents)
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.activeTask = &taskHandle{cancel: taskCancel, step: 0}
	switch name {
	case CommandNameContinue:
		go s.runContinue(taskCtx, taskContent)
	case CommandNameSummarize:
		go s.runSummarize(taskCtx, taskContent)
	default:
		go s.runTask(taskCtx, taskContent, []llm.ContentPart{&llm.TextPart{Text: text}})
	}
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.errText != "" {
		s.writeError(msg.errText)
		return
	}

	if !msg.isCmd {
		// Prompt — reject if a task is already running.
		if s.activeTask != nil {
			s.writeError("A task is already running. Wait for it to complete or cancel it.")
			return
		}
		if err := s.ensureAgentInitialized(); err != nil {
			s.writeError(err.Error())
			return
		}
		taskContent := make([]llm.ContentPart, len(s.Contents))
		copy(taskContent, s.Contents)
		taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
		s.activeTask = &taskHandle{cancel: taskCancel, step: 0}
		go s.runTask(taskCtx, taskContent, msg.contentParts)
		return
	}

	// Command dispatch.
	raw := msg.cmd
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return
	}

	name := fields[0]
	args := fields[1:]

	cmdDef, ok := LookupCommand(name)
	if !ok {
		s.startTaskOrCmd(raw, name)
		return
	}

	switch cmdDef.Policy {
	case CmdImmediate:
		cmdDef.Handler(s, s.sessionCtx, args)
	case CmdIdle:
		if s.activeTask != nil {
			s.writeError("Cannot run this command while a task is in progress. Please wait or cancel the current task.")
			return
		}
		cmdDef.Handler(s, s.sessionCtx, args)
	}
}
