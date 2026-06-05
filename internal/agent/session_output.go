package agent

// Session output helpers: writing TLV messages to the adapter output,
// tracking token usage, and broadcasting system info.
//
// sendSystemInfo is called from the run() goroutine only; the task
// goroutine requests updates via requestSystemInfo(), which sends on
// infoUpdateCh. All state reads are from fields owned by run() or
// from atomic fields — no mutex needed.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// TLV Write Helpers
// ============================================================================

func (s *Session) signalPromptStart(prompt string) {
	s.writeTLVStr(stream.TagUserT, prompt)
}

func (s *Session) writeError(msg string) {
	_ = stream.WriteSystemMsg(s.Output, stream.ErrorMsg{Text: msg}) //nolint:errcheck
}

func (s *Session) writeNotify(msg string) {
	_ = stream.WriteSystemMsg(s.Output, stream.NotifyMsg{Text: msg}) //nolint:errcheck
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

// writeTLVStr writes a string TLV frame and flushes. Best effort — errors are ignored.
//
//nolint:errcheck
func (s *Session) writeTLVStr(tag string, msg string) {
	if s.Output == nil {
		return
	}
	_ = stream.WriteTLV(s.Output, tag, msg) //nolint:errcheck // best-effort write to adapter
}

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame. Best effort.
//
//nolint:errcheck
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.Output == nil {
		return
	}
	data, _ := json.Marshal(v)
	_ = stream.WriteTLV(s.Output, tag, string(data)) //nolint:errcheck // best-effort write to adapter
}

// writeToolUseStart writes a placeholder tool use window.
// The full input is written later by writeToolUseInput when all
// arguments have been received.
func (s *Session) writeToolUseStart(toolName, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.ToolUseData{
		ID:   id,
		Name: toolName,
	})
}

func (s *Session) writeToolUseInput(input, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.ToolUseData{
		ID:    id,
		Input: input,
	})
}

func (s *Session) writeToolUseOutput(id string, output string, isError bool) {
	s.writeTLVJSON(stream.TagUserF, stream.ToolResultData{
		ID:      id,
		Output:  output,
		IsError: isError,
	})
}

// ============================================================================
// System Info Broadcasting
// ============================================================================

// requestSystemInfo signals the run() goroutine to broadcast task info.
// Non-blocking — if a signal is already pending, this is a no-op.
// Called from the task goroutine whenever state changes that should be
// reflected in the UI (step boundaries, errors, etc.).
func (s *Session) requestSystemInfo() {
	select {
	case s.infoUpdateCh <- "task":
	default:
	}
}

// sendSystemInfo sends one or more TagSystemMsg frames to the adapter.
// kind selects which messages to send: "task", "model", "theme",
// "reasoning", or "all".
// Must only be called from the run() goroutine.
func (s *Session) sendSystemInfo(kind string) {
	if s.Output == nil {
		return
	}

	switch kind {
	case "all":
		s.sendMessageVersionMsg()
		s.sendTaskMsg()
		s.sendModelListMsg()
		s.sendModelMsg()
		s.sendThemeListMsg()
		s.sendThemeMsg()
		s.sendReasoningMsg()
	case "task":
		s.sendTaskMsg()
	case "model":
		s.sendModelMsg()
	case "theme":
		s.sendThemeMsg()
	case "reasoning":
		s.sendReasoningMsg()
	}
}

func (s *Session) sendMessageVersionMsg() {
	_ = stream.WriteSystemMsg(s.Output, MessageVersionMsg{MessageVersion: MessageVersion}) //nolint:errcheck
}

func (s *Session) sendTaskMsg() {
	_ = stream.WriteSystemMsg(s.Output, TaskMsg{ //nolint:errcheck
		InProgress:  s.inProgress.Load(),
		CurrentStep: int(s.currentStep.Load()),
		MaxSteps:    s.MaxSteps,
		Context:     s.ContextTokens.Load(),
		TaskError:   s.pausedOnError.Load(),
		QueueItems:  s.taskQueue,
	})
}

func (s *Session) sendModelMsg() {
	if s.ModelManager == nil {
		return
	}
	activeID := s.ModelManager.GetActiveID()
	activeName := ""
	if activeModel := s.ModelManager.GetActive(); activeModel != nil {
		activeName = activeModel.Name
	}
	_ = stream.WriteSystemMsg(s.Output, ModelMsg{ //nolint:errcheck
		ActiveModelID:   activeID,
		ActiveModelName: activeName,
		ContextLimit:    s.ContextLimit,
	})
}

// sendModelListMsg sends the full model list.
// Called once on startup so adapters can populate the model selector.
func (s *Session) sendModelListMsg() {
	if s.ModelManager == nil {
		return
	}
	_ = stream.WriteSystemMsg(s.Output, ModelListMsg{ //nolint:errcheck
		Models:          s.ModelManager.GetModels(),
		ModelConfigPath: s.ModelManager.GetFilePath(),
	})
}

func (s *Session) sendThemeMsg() {
	if s.RuntimeManager == nil {
		return
	}
	name := s.RuntimeManager.GetActiveTheme()
	_ = stream.WriteSystemMsg(s.Output, ThemeMsg{Name: name}) //nolint:errcheck
}

// sendThemeListMsg sends the full list of available themes with content.
// loadThemeFromFile loads a theme file and returns a ThemeInfo.
// Returns zero ThemeInfo and false if the file cannot be loaded.
func loadThemeFromFile(path string) (ThemeInfo, bool) {
	name := strings.TrimSuffix(filepath.Base(path), ".conf")
	t, err := theme.LoadTheme(path)
	if err != nil {
		return ThemeInfo{}, false
	}
	return ThemeInfo{Name: name, Theme: t}, true
}

// sendThemeListMsg sends the full list of available themes with content.
// Called once on startup so adapters can cache theme data.
func (s *Session) sendThemeListMsg() {
	if s.ThemesFolder == "" {
		return
	}
	confs, err := filepath.Glob(filepath.Join(s.ThemesFolder, "*.conf"))
	if err != nil {
		return
	}
	infos := make([]ThemeInfo, 0, len(confs))
	for _, path := range confs {
		if info, ok := loadThemeFromFile(path); ok {
			infos = append(infos, info)
		}
	}
	if len(infos) > 0 {
		_ = stream.WriteSystemMsg(s.Output, ThemeListMsg{Themes: infos}) //nolint:errcheck
	}
}

func (s *Session) sendReasoningMsg() {
	_ = stream.WriteSystemMsg(s.Output, ReasoningMsg{Level: int(s.reasoningLevel.Load())}) //nolint:errcheck
}
