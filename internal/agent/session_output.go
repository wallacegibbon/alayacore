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
	"time"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// TLV Write Helpers
// ============================================================================

func (s *Session) signalPromptStart(prompt string) {
	s.writeTLVStr(stream.TagUserT, prompt)
}

func (s *Session) writeError(msg string) {
	s.writeTLVStr(stream.TagSystemError, msg)
}

func (s *Session) writeNotify(msg string) {
	s.writeTLVStr(stream.TagSystemNotify, msg)
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

// requestSystemInfo signals the run() goroutine to broadcast system info.
// Non-blocking — if a signal is already pending, this is a no-op.
// Called from the task goroutine whenever state changes that should be
// reflected in the UI (step boundaries, errors, etc.).
func (s *Session) requestSystemInfo() {
	select {
	case s.infoUpdateCh <- struct{}{}:
	default:
	}
}

func (s *Session) sendSystemInfo() {
	if s.Output == nil {
		return
	}

	// Only include model data when it has changed since the last broadcast.
	// Models are large (all configured models with metadata) and change
	// infrequently — skipping them on every status update saves bandwidth.
	var models []ModelInfo
	var activeID int
	var activeModelName string
	var modelConfigPath string

	if s.modelsChanged && s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
		if activeModel := s.ModelManager.GetActive(); activeModel != nil {
			activeModelName = activeModel.Name
		}
		modelConfigPath = s.ModelManager.GetFilePath()
		s.modelsChanged = false
	}

	// taskQueue is owned by run() goroutine (or single-threaded during construction).
	queueItems := make([]QueueItemInfo, len(s.taskQueue))
	for i, item := range s.taskQueue {
		var itemType, content string
		switch t := item.Task.(type) {
		case UserPrompt:
			itemType = "prompt"
			content = t.Text
		case CommandPrompt:
			itemType = "command"
			content = t.Command
		}
		queueItems[i] = QueueItemInfo{
			QueueID:   item.QueueID,
			Type:      itemType,
			Content:   content,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
	}

	info := SystemInfo{
		ContextTokens:   s.ContextTokens.Load(),
		ContextLimit:    s.ContextLimit,
		TotalTokens:     s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens,
		QueueItems:      queueItems,
		InProgress:      s.inProgress,
		CurrentStep:     int(s.currentStep.Load()),
		MaxSteps:        s.MaxSteps,
		TaskError:       s.pausedOnError.Load(),
		Models:          models,
		ActiveModelID:   activeID,
		ActiveModelName: activeModelName,
		ModelConfigPath: modelConfigPath,
		ReasoningLevel:  int(s.reasoningLevel.Load()),
	}

	if s.RuntimeManager != nil {
		info.ActiveTheme = s.RuntimeManager.GetActiveTheme()
	}

	s.writeTLVJSON(stream.TagSystemData, info)
}
