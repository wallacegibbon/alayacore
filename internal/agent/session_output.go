package agent

// Session output helpers: writing TLV messages to the adaptor output,
// tracking token usage, and broadcasting system info.
//
// sendSystemInfo is called from the run() goroutine only; the task
// goroutine requests updates via requestSystemInfo(), which sends on
// infoUpdateCh. All state reads are from fields owned by run() or
// from atomic fields — no mutex needed.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/stream"
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
	_ = stream.WriteTLV(s.Output, tag, msg) //nolint:errcheck // best-effort write to adaptor
}

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame. Best effort.
//
//nolint:errcheck
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.Output == nil {
		return
	}
	data, _ := json.Marshal(v)
	_ = stream.WriteTLV(s.Output, tag, string(data)) //nolint:errcheck // best-effort write to adaptor
}

func (s *Session) writeToolCall(toolName, input, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.FunctionData{
		ID:    id,
		Type:  "call",
		Name:  toolName,
		Input: input,
	})
}

// writeToolCallStart writes a placeholder tool call window.
// The full input is written later by writeToolCall when all
// arguments have been received.
func (s *Session) writeToolCallStart(toolName, id string) {
	s.writeTLVJSON(stream.TagAssistantF, stream.FunctionData{
		ID:   id,
		Type: "start",
		Name: toolName,
	})
}

func (s *Session) writeToolOutput(toolCallID string, output string, status string) {
	s.writeTLVJSON(stream.TagUserF, stream.ToolResultData{
		ID:     toolCallID,
		Output: output,
		Status: status,
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

// sendSystemInfo sends one or more TagSystemMsg frames to the adaptor.
// kind selects which messages to send: "task", "model", "theme",
// "reasoning", or "all".
// Must only be called from the run() goroutine.
func (s *Session) sendSystemInfo(kind string) {
	if s.Output == nil {
		return
	}

	switch kind {
	case "all":
		s.sendTaskMsg()
		s.sendModelMsgs()
		s.sendThemeMsg()
		s.sendReasoningMsg()
	case "task":
		s.sendTaskMsg()
	case "model":
		s.sendModelMsgs()
	case "theme":
		s.sendThemeMsg()
	case "reasoning":
		s.sendReasoningMsg()
	}
}

func (s *Session) sendTaskMsg() {
	_ = stream.WriteSystemMsg(s.Output, TaskMsg{ //nolint:errcheck
		InProgress:   s.inProgress,
		CurrentStep:  int(s.currentStep.Load()),
		MaxSteps:     s.MaxSteps,
		Context:      s.ContextTokens.Load(),
		ContextLimit: s.ContextLimit,
		TotalTokens:  s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens,
		TaskError:    s.pausedOnError.Load(),
		QueueItems:   len(s.taskQueue),
	})
}

func (s *Session) sendModelMsgs() {
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
	})
	_ = stream.WriteSystemMsg(s.Output, ModelListMsg{ //nolint:errcheck
		Models:          s.ModelManager.GetModels(),
		ModelConfigPath: s.ModelManager.GetFilePath(),
	})
}

func (s *Session) sendThemeMsg() {
	if s.RuntimeManager != nil {
		_ = stream.WriteSystemMsg(s.Output, ThemeMsg{Name: s.RuntimeManager.GetActiveTheme()}) //nolint:errcheck
	}
}

func (s *Session) sendReasoningMsg() {
	_ = stream.WriteSystemMsg(s.Output, ReasoningMsg{Level: int(s.reasoningLevel.Load())}) //nolint:errcheck
}
