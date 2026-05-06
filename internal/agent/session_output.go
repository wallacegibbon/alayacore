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

func (s *Session) writeGapped(tag string, msg string) {
	if s.Output == nil {
		return
	}
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, tag, msg)
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	// Send tool call as JSON via FC tag
	tc := stream.ToolCallData{
		ID:    id,
		Name:  toolName,
		Input: input,
	}
	jsonData, _ := json.Marshal(tc) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionCall, string(jsonData))
	s.Output.Flush()
	s.writeToolResult(id, "pending")
}

func (s *Session) writeToolOutput(toolCallID string, output string) {
	// Send tool result as JSON via FR tag
	tr := stream.ToolResultData{
		ID:     toolCallID,
		Output: output,
	}
	jsonData, _ := json.Marshal(tr) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionResult, string(jsonData))
	s.Output.Flush()
}

func (s *Session) writeToolResult(toolCallID string, status string) {
	if s.Output == nil {
		return
	}
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionState, stream.WrapDelta(toolCallID, status))
	s.Output.Flush()
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
	data, _ := json.Marshal(info) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagSystemData, string(data))
	s.Output.Flush()
}
