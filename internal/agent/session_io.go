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
	s.writeError("nothing to cancel")
}

func (s *Session) saveSession(args []string) {
	var path string
	switch len(args) {
	case 0:
		if s.SessionFile == "" {
			s.writeError("no session file set")
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
		s.writeError(fmt.Sprintf("save: failed to save session: %v", err))
	} else {
		s.writeNotifyf("Session saved to %s", path)
	}
}

func (s *Session) handleModelSet(args []string) {
	if s.ModelManager == nil {
		s.writeError("model manager not initialized")
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_set <id>")
		return
	}

	modelIDStr := args[0]
	modelID, err := strconv.Atoi(modelIDStr)
	if err != nil {
		s.writeError(fmt.Sprintf("model_set: invalid model ID: %s", modelIDStr))
		return
	}
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(fmt.Sprintf("model_set: model not found: %d", modelID))
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
		s.writeError("model manager not initialized")
		return
	}

	path := s.ModelManager.GetFilePath()
	if path == "" {
		s.writeError("no model file path configured")
		return
	}

	if err := s.ModelManager.LoadFromFile(path); err != nil {
		s.writeError(fmt.Sprintf("model_load: failed to load models: %v", err))
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

// handleModelSync replaces all models with JSON content from an adapter
// editor session. The JSON is compact (no whitespace) and matches the
// ModelListMsg wire format ([]ModelConfig).
func (s *Session) handleModelSync(args []string) {
	if s.ModelManager == nil {
		s.writeError("model manager not initialized")
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_sync <json>")
		return
	}

	content := args[0]

	msgs := s.ModelManager.SyncFromContent(content)

	// Report validation messages
	for _, m := range msgs {
		s.writeError(m)
	}

	// Re-resolve the active model
	s.setActiveFromRuntimeConfig()
	s.setActiveFromSessionMeta()
	s.setActiveFromCliFlag()

	// Re-initialize the provider/agent with the (potentially edited)
	// model config
	if model := s.ModelManager.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			s.writeError("Failed to reinitialize model after sync: " + err.Error())
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
	err          error             // non-nil when the input pump hit a validation error
}

// inputPump runs in its own goroutine.  It reads TLV frames from the
// input stream, builds inputMsg values, and sends them to inputMsgCh.
// It does NOT interpret commands or access session state — all of
// that lives in the run() goroutine.
func (s *Session) inputPump() {
	var staged []llm.ContentPart

	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			if len(staged) > 0 {
				s.inputMsgCh <- inputMsg{contentParts: staged}
			}
			close(s.inputMsgCh)
			return
		}
		staged = s.handleInputFrame(tag, value, staged)
	}
}

// handleInputFrame processes a single TLV frame from the input stream.
// Returns the updated staged content (nil when staged content has been
// consumed by UE or discarded by an error). Media tags (UI/UV/UA/UD)
// and regular text (UT without ':') are staged until UE or EOF.
// Command text (UT starting with ':') is sent immediately without staging.
func (s *Session) handleInputFrame(tag, value string, staged []llm.ContentPart) []llm.ContentPart {
	switch tag {
	case stream.TagUserI:
		return append(staged, &llm.ImagePart{URI: value})
	case stream.TagUserV:
		return append(staged, &llm.VideoPart{URI: value})
	case stream.TagUserA:
		return append(staged, &llm.AudioPart{URI: value})
	case stream.TagUserD:
		return append(staged, &llm.DocumentPart{URI: value})
	case stream.TagUserT:
		if len(value) > 0 && value[0] == ':' {
			if len(staged) > 0 {
				s.inputMsgCh <- inputMsg{err: fmt.Errorf("command can not be sent with staged content")}
				return nil
			}
			s.inputMsgCh <- inputMsg{isCmd: true, cmd: value[1:]}
			return staged
		}
		if value != "" {
			return append(staged, &llm.TextPart{Text: value})
		}
		return staged
	case stream.TagUserEnd:
		if len(staged) > 0 {
			s.inputMsgCh <- inputMsg{contentParts: staged}
		}
		return nil
	default:
		s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid input tag: %s", tag)}
		return nil
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
	case "yes":
		allowed = true
	case "no":
		allowed = false
	default:
		s.writeError("usage: :confirm <id> yes|no")
		return
	}

	*p <- llm.ToolConfirmResponse{ID: id, Allowed: allowed}
}

// startTaskContinue starts a task goroutine for :continue.
func (s *Session) startTaskContinue() {
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
	go s.runContinue(taskCtx, taskContent)
}

// startTaskSummarize starts a task goroutine for :summarize.
func (s *Session) startTaskSummarize() {
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
	go s.runSummarize(taskCtx, taskContent)
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.err != nil {
		s.writeError(msg.err.Error())
		return
	}

	if !msg.isCmd {
		s.handlePrompt(msg.contentParts)
		return
	}

	// Command dispatch.
	fields := strings.Fields(msg.cmd)
	if len(fields) == 0 {
		s.writeError("empty command")
		return
	}

	name := fields[0]
	args := fields[1:]

	// Registry commands.
	if cmdDef, ok := LookupCommand(name); ok {
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
		return
	}

	// Task commands — :continue and :summarize run in their own goroutine.
	switch name {
	case CommandNameContinue:
		s.startTaskContinue()
	case CommandNameSummarize:
		s.startTaskSummarize()
	default:
		s.writeError(fmt.Sprintf("unknown command: %s", name))
	}
}

// handlePrompt starts a task goroutine for a user prompt.
// Called from handleInputMsg when the input is not a command.
func (s *Session) handlePrompt(contentParts []llm.ContentPart) {
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
	go s.runTask(taskCtx, taskContent, contentParts)
}
