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
	"github.com/alayacore/alayacore/internal/mcp"
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

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame.
// On marshal failure, marks output as broken (same as writeTLV on write failure).
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		s.markOutputBroken()
		return
	}
	s.writeTLV(tag, string(data))
}

func (s *Session) writeToolInput(input json.RawMessage, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.ToolInputData{
		ID:    id,
		Input: input,
	})
}

func (s *Session) writeToolOutput(id string, contents []llm.ContentPart, isError bool) {
	contentJSON, err := serializeContentParts(contents)
	if err != nil {
		contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
	}
	s.writeTLVJSON(stream.TagUserF, stream.ToolOutputData{
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
	s.writeSystemMsg(MessageVersionMsg{MessageVersion: MessageVersion})
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
		ContextLimit:    s.ContextLimit,
	})
}

// sendModelListMsg sends the full model list.
// Called once on startup so adapters can populate the model selector.
func (s *Session) sendModelListMsg() {
	if s.ModelManager == nil {
		return
	}
	s.writeSystemMsg(ModelListMsg{
		Models: s.ModelManager.GetModels(),
	})
}

func (s *Session) sendThemeMsg() {
	if s.RuntimeManager == nil {
		return
	}
	name := s.RuntimeManager.GetActiveTheme()
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
	s.writeSystemMsg(ReasoningMsg{Level: s.reasoningLevel})
}

func (s *Session) sendVideoConfigMsg() {
	s.writeSystemMsg(VideoConfigMsg{FPS: s.videoFPS, Res: s.videoRes})
}

// sendMCPInitMsg sends an MCP initialization status update to the adapter.
func (s *Session) sendMCPInitMsg(status string, toolCount int, pending []MCPAuthServer) {
	s.writeSystemMsg(MCPInitMsg{
		Status:      status,
		ToolCount:   toolCount,
		PendingAuth: pending,
	})
}

// sendServerMCPInitMsg forwards a per-server progress event to the adapter.
func (s *Session) sendServerMCPInitMsg(p mcp.AsyncProgress) {
	s.writeSystemMsg(MCPInitMsg{
		Status:         p.Status,
		Server:         p.Server,
		ServerCount:    p.TotalCount,
		ConnectedCount: p.ConnectedCount,
		SkippedCount:   p.SkippedCount,
	})
}

// sendMCPAuthConfirm sends an MCP OAuth confirmation prompt to the adapter.
// The adapter shows a y/n confirm dialog for the given server.
func (s *Session) sendMCPAuthConfirm(serverName, serverURL string) {
	s.writeSystemMsg(MCPAuthMsg{
		Server: serverName,
		URL:    serverURL,
		Status: "confirm",
	})
}

// sendMCPAuthDone tells the adapter that all OAuth servers have been
// processed. The adapter closes any remaining MCP overlay.
func (s *Session) sendMCPAuthDone() {
	s.writeSystemMsg(MCPAuthMsg{
		Status: "done",
	})
}
