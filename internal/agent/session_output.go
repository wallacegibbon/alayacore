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

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// TLV Write Helpers
//
// All writes check for a previously broken output stream.  On the first
// write error the session context is canceled, which stops the agent
// loop and prevents wasted API calls on a dead adapter.
// ============================================================================

// markOutputBroken sets the broken flag and cancels the session context.
// Idempotent — only the first call has any effect.
func (s *Session) markOutputBroken() {
	if s.outputBroken.CompareAndSwap(false, true) {
		s.sessionCancel()
	}
}

// writeTLV writes a TLV frame. On error, marks output as broken.
func (s *Session) writeTLV(tag string, value string) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	if err := stream.WriteTLV(s.Output, tag, value); err != nil {
		s.markOutputBroken()
	}
}

// writeSystemMsg writes a TagSystemMsg frame. On error, marks output as broken.
func (s *Session) writeSystemMsg(msg stream.SystemMsg) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	if err := stream.WriteSystemMsg(s.Output, msg); err != nil {
		s.markOutputBroken()
	}
}

func (s *Session) writeError(msg string) {
	s.writeSystemMsg(stream.ErrorMsg{Text: msg})
}

func (s *Session) writeNotify(msg string) {
	s.writeSystemMsg(stream.NotifyMsg{Text: msg})
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

// writeTLVStr writes a string TLV frame.
func (s *Session) writeTLVStr(tag string, msg string) {
	s.writeTLV(tag, msg)
}

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame.
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.writeTLV(tag, string(data))
}

func (s *Session) writeToolUseInput(input json.RawMessage, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.ToolUseData{
		ID:    id,
		Input: input,
	})
}

func (s *Session) writeToolUseOutput(id string, content []llm.ContentPart, isError bool) {
	contentJSON, err := serializeContentParts(content)
	if err != nil {
		contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
	}
	s.writeTLVJSON(stream.TagUserF, stream.ToolResultData{
		ID:      id,
		Output:  contentJSON,
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
	s.writeSystemMsg(MessageVersionMsg{MessageVersion: MessageVersion})
}

func (s *Session) sendTaskMsg() {
	s.writeSystemMsg(TaskMsg{
		InProgress:  s.inProgress,
		CurrentStep: s.currentStep,
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
	s.writeSystemMsg(ModelMsg{
		ActiveModelID:   activeID,
		ActiveModelName: activeName,
		ContextLimit:    s.ContextLimit.Load(),
	})
}

// sendModelListMsg sends the full model list.
// Called once on startup so adapters can populate the model selector.
func (s *Session) sendModelListMsg() {
	if s.ModelManager == nil {
		return
	}
	s.writeSystemMsg(ModelListMsg{
		Models:          s.ModelManager.GetModels(),
		ModelConfigPath: s.ModelManager.GetFilePath(),
	})
}

func (s *Session) sendThemeMsg() {
	if s.RuntimeManager == nil {
		return
	}
	name := s.RuntimeManager.GetActiveTheme()
	s.writeSystemMsg(ThemeMsg{Name: name})
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
		s.writeSystemMsg(ThemeListMsg{Themes: infos})
	}
}

func (s *Session) sendReasoningMsg() {
	s.writeSystemMsg(ReasoningMsg{Level: int(s.reasoningLevel.Load())})
}
