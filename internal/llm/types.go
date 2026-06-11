// Package llm provides a custom LLM client with streaming support
package llm

import (
	"context"
	"encoding/json"
	"iter"
)

// DefaultMaxTokens is the default maximum output tokens when the user
// doesn't specify one. 128K covers coding agents generating large code
// blocks, multi-file changes, and long tool call chains.
const DefaultMaxTokens = 131072

// MessageRole represents the role of a message
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ContentPart represents a part of message content
type ContentPart interface {
	GetHistoryID() uint64
	SetHistoryID(uint64)
	GetRole() MessageRole
	SetRole(MessageRole)
	UpdateContentPartMeta(historyID uint64, role MessageRole) ContentPart
}

const (
	ContentPartText       = "text"
	ContentPartImage      = "image"
	ContentPartReasoning  = "reasoning"
	ContentPartToolUse    = "tool_use"
	ContentPartToolResult = "tool_result"
)

// TextPart represents text content
type TextPart struct {
	Text      string      `json:"text"`
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`
}

func (p *TextPart) GetHistoryID() uint64   { return p.HistoryID }
func (p *TextPart) SetHistoryID(id uint64) { p.HistoryID = id }
func (p *TextPart) GetRole() MessageRole   { return p.Role }
func (p *TextPart) SetRole(r MessageRole)  { p.Role = r }
func (p *TextPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ImagePart represents an image content (DataURI: data:image/...;base64,...)
type ImagePart struct {
	DataURL   string      `json:"data_url"`
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`
}

func (p *ImagePart) GetHistoryID() uint64   { return p.HistoryID }
func (p *ImagePart) SetHistoryID(id uint64) { p.HistoryID = id }
func (p *ImagePart) GetRole() MessageRole   { return p.Role }
func (p *ImagePart) SetRole(r MessageRole)  { p.Role = r }
func (p *ImagePart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ReasoningPart represents reasoning/thinking content.
type ReasoningPart struct {
	Text      string      `json:"text"`
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`
}

func (p *ReasoningPart) GetHistoryID() uint64   { return p.HistoryID }
func (p *ReasoningPart) SetHistoryID(id uint64) { p.HistoryID = id }
func (p *ReasoningPart) GetRole() MessageRole   { return p.Role }
func (p *ReasoningPart) SetRole(r MessageRole)  { p.Role = r }
func (p *ReasoningPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ToolUsePart represents a tool call stored in conversation history.
type ToolUsePart struct {
	ID        string          `json:"id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	HistoryID uint64          `json:"-"`
	Role      MessageRole     `json:"-"`
}

func (p *ToolUsePart) GetHistoryID() uint64   { return p.HistoryID }
func (p *ToolUsePart) SetHistoryID(id uint64) { p.HistoryID = id }
func (p *ToolUsePart) GetRole() MessageRole   { return p.Role }
func (p *ToolUsePart) SetRole(r MessageRole)  { p.Role = r }
func (p *ToolUsePart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ToolResultPart represents a tool execution result.
type ToolResultPart struct {
	ID        string        `json:"id"`
	Content   []ContentPart `json:"content"`
	IsError   bool          `json:"is_error"`
	HistoryID uint64        `json:"-"`
	Role      MessageRole   `json:"-"`
}

func (p *ToolResultPart) GetHistoryID() uint64   { return p.HistoryID }
func (p *ToolResultPart) SetHistoryID(id uint64) { p.HistoryID = id }
func (p *ToolResultPart) GetRole() MessageRole   { return p.Role }
func (p *ToolResultPart) SetRole(r MessageRole)  { p.Role = r }
func (p *ToolResultPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// Message represents a single message in the conversation
type Message struct {
	Role    MessageRole   `json:"role"`
	Content []ContentPart `json:"content"`
}

// ToolDefinition defines a tool that can be called
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Usage tracks token usage.
type Usage struct {
	CacheCreationTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

// StreamEvent represents a streaming event
type StreamEvent interface {
	isStreamEvent()
}

// TextDeltaEvent represents text content streaming
type TextDeltaEvent struct {
	Delta string
	Index int
}

func (TextDeltaEvent) isStreamEvent() {}

// ReasoningDeltaEvent represents reasoning content streaming
type ReasoningDeltaEvent struct {
	Delta string
	Index int
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ToolUseStartEvent signals that a tool use has started
type ToolUseStartEvent struct {
	ID       string
	ToolName string
	Index    int
}

func (ToolUseStartEvent) isStreamEvent() {}

// ToolUseCompleteEvent signals that a tool use's arguments have finished streaming
type ToolUseCompleteEvent struct {
	ID       string
	ToolName string
	Input    json.RawMessage
	Index    int
}

func (ToolUseCompleteEvent) isStreamEvent() {}

// StepCompleteEvent represents completion of an agentic step.
type StepCompleteEvent struct {
	Message    Message
	Usage      Usage
	StopReason string
}

func (StepCompleteEvent) isStreamEvent() {}

// Provider defines the interface for LLM providers
type Provider interface {
	StreamMessages(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		systemPrompt string,
		extraSystemPrompt string,
	) (iter.Seq2[StreamEvent, error], error)
	SetReasoningLevel(level int)
}
