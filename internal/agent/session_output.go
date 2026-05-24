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
	s.writeGapped(stream.TagTextUser, prompt)
}

func (s *Session) writeError(msg string) {
	s.writeGapped(stream.TagSystemError, msg)
}

func (s *Session) writeNotify(msg string) {
	s.writeGapped(stream.TagSystemNotify, msg)
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

// writeGapped writes a string TLV frame and flushes. Best effort — errors are ignored.
func (s *Session) writeGapped(tag string, msg string) {
	if s.Output == nil {
		return
	}
	_ = stream.WriteTLV(s.Output, tag, msg) //nolint:errcheck // best-effort write to adaptor
	s.Output.Flush()
}

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame. Best effort.
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.Output == nil {
		return
	}
	data, _ := json.Marshal(v)                       //nolint:errcheck // best-effort marshal, value is always serializable
	_ = stream.WriteTLV(s.Output, tag, string(data)) //nolint:errcheck // best-effort write to adaptor
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	s.writeTLVJSON(stream.TagFunctionCall, stream.ToolCallData{
		ID:    id,
		Name:  toolName,
		Input: input,
	})
	s.writeToolResult(id, "pending")
}

// writeToolCallStart writes a placeholder tool call window using an empty JSON
// object as input. The full content is written later by writeToolCall when all
// arguments have been received.
func (s *Session) writeToolCallStart(toolName, id string) {
	s.writeTLVJSON(stream.TagFunctionCall, stream.ToolCallData{
		ID:    id,
		Name:  toolName,
		Input: "{}",
	})
	s.writeToolResult(id, "pending")
}

func (s *Session) writeToolOutput(toolCallID string, output string) {
	s.writeTLVJSON(stream.TagFunctionResult, stream.ToolResultData{
		ID:     toolCallID,
		Output: output,
	})
}

func (s *Session) writeToolResult(toolCallID string, status string) {
	s.writeGapped(stream.TagFunctionState, stream.WrapDelta(toolCallID, status))
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
	s.sendSystemInfoInternal(nil)
}

func (s *Session) sendSystemInfoInternal(activeModelConfig *ModelConfig) {
	if s.Output == nil {
		return
	}

	var models []ModelInfo
	var activeID int
	var activeModelName string
	var modelConfigPath string
	var hasModels bool

	if s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
		if activeModelConfig != nil {
			activeModelName = activeModelConfig.Name
		} else if activeModel := s.ModelManager.GetActive(); activeModel != nil {
			activeModelName = activeModel.Name
		}
		modelConfigPath = s.ModelManager.GetFilePath()
		hasModels = s.ModelManager.HasModels()
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
		ContextTokens:     s.ContextTokens.Load(),
		ContextLimit:      s.ContextLimit,
		TotalTokens:       s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens,
		QueueItems:        queueItems,
		InProgress:        s.inProgress.Load(),
		CurrentStep:       int(s.currentStep.Load()),
		MaxSteps:          s.MaxSteps,
		TaskError:         s.pausedOnError.Load(),
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: activeModelConfig,
		ActiveModelName:   activeModelName,
		HasModels:         hasModels,
		ModelConfigPath:   modelConfigPath,
		ThinkLevel:        int(s.thinkLevel.Load()),
	}

	s.writeTLVJSON(stream.TagSystemData, info)
}
