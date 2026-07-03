package agent

// Session output helpers: writing TLV messages to the adapter output,
// tracking token usage, and broadcasting system info.
//
// Broadcasting overview:
//
//   Guaranteed broadcasts (critical state transitions):
//     handleTaskEvent(StepStartEvent)  → sendSystemInfo("task")  — step counter
//     handleTaskDone()                 → sendSystemInfo("task")  — task completion
//     handleModelSet/ModelLoad         → sendSystemInfo("model") — model switch
//     SetReasoningLevel()              → sendSystemInfo("reasoning")
//     SetVideoConfig()                 → sendSystemInfo("video_config")
//     handleThemeSet()                 → sendSystemInfo("theme")
//
//   Best-effort broadcasts (UI responsiveness optimization):
//     The task goroutine calls requestSystemInfo() which sends on
//     taskRefreshCh (non-blocking). The run() goroutine picks it up
//     and calls sendSystemInfo("task"). If the send is dropped
//     (buffer full), the TUI misses a transient update but catches
//     up at the next guaranteed broadcast above.
//
// All state reads in sendSystemInfo are from fields owned by run()
// or from atomic fields — no mutex needed.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/alayacore/alayacore/internal/version"
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
	if err := tlv.WriteTLV(s.Output, tag, value); err != nil {
		s.markOutputBroken()
	}
}

// writeSystemMsg writes a TagSystemMsg frame. On error, marks output as broken.
func (s *Session) writeSystemMsg(msg protocol.SystemMsg) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	if err := protocol.WriteSystemMsg(s.Output, msg); err != nil {
		s.markOutputBroken()
	}
}

func (s *Session) writeError(msg string) {
	s.writeSystemMsg(protocol.ErrorMsg{Text: msg})
}

func (s *Session) writeNotify(msg string) {
	s.writeSystemMsg(protocol.NotifyMsg{Text: msg})
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

func (s *Session) writeToolInput(input json.RawMessage, id string) {
	data, err := marshalToolInputData(id, "", input)
	if err != nil {
		return
	}
	s.writeTLV(tlv.TagAssistantF, string(data))
}

func (s *Session) writeToolOutput(id string, contents []llm.ContentPart, isError bool) {
	data, err := marshalToolOutputData(id, contents, isError)
	if err != nil {
		return
	}
	s.writeTLV(tlv.TagUserF, string(data))
}

// ============================================================================
// System Info Broadcasting
// ============================================================================

// requestSystemInfo signals the run() goroutine to broadcast task info.
// Non-blocking — if a signal is already pending, this is a no-op.
// Only the latest state matters, and critical state transitions
// (step counter, task completion) are sent directly from run(), so
// dropping a redundant request is harmless.
// Called from the task goroutine whenever state changes that should be
// reflected in the UI (step boundaries, errors, etc.).
func (s *Session) requestSystemInfo() {
	select {
	case s.taskRefreshCh <- struct{}{}:
	default:
	}
}

// sendSystemInfo sends one or more TagSystemMsg frames to the adapter.
// kind selects which messages to send: "task", "model", "theme",
// "reasoning", "video_config", or "all".
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
		s.sendVideoConfigMsg()
	case "task":
		s.sendTaskMsg()
	case "model":
		s.sendModelMsg()
	case "theme":
		s.sendThemeMsg()
	case "reasoning":
		s.sendReasoningMsg()
	case "video_config":
		s.sendVideoConfigMsg()
	}
}

func (s *Session) sendMessageVersionMsg() {
	s.writeSystemMsg(MessageVersionMsg{MessageVersion: MessageVersion, CoreVersion: version.Version})
}

func (s *Session) sendTaskMsg() {
	s.writeSystemMsg(TaskMsg{
		InProgress:  s.activeTask != nil,
		CurrentStep: s.activeTaskStep(),
		MaxSteps:    s.MaxSteps,
		Context:     s.ContextTokens,
		TaskError:   false,
	})
}

func (s *Session) sendModelMsg() {
	ms := s.modelService
	s.writeSystemMsg(ModelMsg{
		ActiveModelID:   ms.ActiveModelID(),
		ActiveModelName: ms.ActiveModelName(),
		ContextLimit:    ms.ContextLimit(),
	})
}

// sendModelListMsg sends the full model list.
func (s *Session) sendModelListMsg() {
	ms := s.modelService
	if !ms.HasModels() {
		return
	}
	s.writeSystemMsg(ModelListMsg{
		Models: ms.GetModels(),
	})
}

func (s *Session) sendThemeMsg() {
	rm := s.modelService.RuntimeManager()
	if rm == nil {
		return
	}
	name := rm.GetActiveTheme()
	s.writeSystemMsg(ThemeMsg{Name: name})
}

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
	s.writeSystemMsg(ReasoningMsg{Level: s.modelService.ReasoningLevel()})
}

func (s *Session) sendVideoConfigMsg() {
	s.writeSystemMsg(VideoConfigMsg{FPS: s.modelService.VideoFPS(), Res: s.modelService.VideoRes()})
}
