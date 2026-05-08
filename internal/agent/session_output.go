package agent

// Session output helpers: writing TLV messages to the adaptor output,
// tracking token usage, and broadcasting system info.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
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
	_ = stream.WriteTLV(s.Output, tag, msg)
	s.Output.Flush()
}

// writeTLVJSON marshals a value to JSON and writes it as a TLV frame. Best effort.
func (s *Session) writeTLVJSON(tag string, v any) {
	if s.Output == nil {
		return
	}
	data, _ := json.Marshal(v)
	_ = stream.WriteTLV(s.Output, tag, string(data))
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
// Usage Tracking
// ============================================================================

func (s *Session) trackUsage(usage llm.Usage) {
	s.mu.Lock()
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	// Only overwrite ContextTokens if the provider reported a non-zero value.
	// OpenAI-compatible providers (e.g. GLM-5.1) may omit the usage field from
	// SSE chunks entirely. Go's json.Unmarshal leaves absent fields at their
	// zero values, so Usage arrives as {InputTokens: 0, OutputTokens: 0, ...}.
	// Without this guard, ContextTokens would be reset to 0.
	newContext := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
	if newContext > 0 {
		s.ContextTokens = newContext
	}
	s.mu.Unlock()
	s.sendSystemInfo()
}

// ============================================================================
// System Info Broadcasting
// ============================================================================

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

	s.mu.Lock()
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
	inProgress := s.inProgress
	contextTokens := s.ContextTokens
	contextLimit := s.ContextLimit
	totalTokens := s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens
	currentStep := s.currentStep
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens:     contextTokens,
		ContextLimit:      contextLimit,
		TotalTokens:       totalTokens,
		QueueItems:        queueItems,
		InProgress:        inProgress,
		CurrentStep:       currentStep,
		MaxSteps:          s.maxSteps,
		TaskError:         s.pausedOnError,
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: activeModelConfig,
		ActiveModelName:   activeModelName,
		HasModels:         hasModels,
		ModelConfigPath:   modelConfigPath,
		ThinkLevel:        s.thinkLevel,
	}
	s.writeTLVJSON(stream.TagSystemData, info)
}
