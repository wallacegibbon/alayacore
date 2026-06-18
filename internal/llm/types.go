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

// ContentMeta holds the metadata common to all ContentPart types.
// Embedded in each concrete ContentPart to avoid duplicating
// the HistoryID/Role fields and their accessor methods.
type ContentMeta struct {
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`
}

func (m *ContentMeta) GetHistoryID() uint64   { return m.HistoryID }
func (m *ContentMeta) SetHistoryID(id uint64) { m.HistoryID = id }
func (m *ContentMeta) GetRole() MessageRole   { return m.Role }
func (m *ContentMeta) SetRole(r MessageRole)  { m.Role = r }

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
	ContentPartVideo      = "video"
	ContentPartAudio      = "audio"
	ContentPartDocument   = "document"
	ContentPartReasoning  = "reasoning"
	ContentPartToolUse    = "tool_use"
	ContentPartToolResult = "tool_result"
)

// TextPart represents text content
type TextPart struct {
	ContentMeta
	Text string `json:"text"`
}

func (p *TextPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ImagePart represents an image content (DataURI: data:image/...;base64,...)
type ImagePart struct {
	ContentMeta
	DataURL string `json:"data_url"`
}

func (p *ImagePart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// VideoPart represents a video content (DataURI: data:video/...;base64,...)
type VideoPart struct {
	ContentMeta
	DataURL string `json:"data_url"`
}

func (p *VideoPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// AudioPart represents an audio content (DataURI: data:audio/...;base64,...)
type AudioPart struct {
	ContentMeta
	DataURL string `json:"data_url"`
}

func (p *AudioPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// DocumentPart represents a document content (DataURI: data:application/...;base64,...)
type DocumentPart struct {
	ContentMeta
	DataURL string `json:"data_url"`
}

func (p *DocumentPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ReasoningPart represents reasoning/thinking content.
type ReasoningPart struct {
	ContentMeta
	Text string `json:"text"`
}

func (p *ReasoningPart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ToolUsePart represents a tool call stored in conversation history.
type ToolUsePart struct {
	ContentMeta
	ID       string          `json:"id"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

func (p *ToolUsePart) UpdateContentPartMeta(id uint64, r MessageRole) ContentPart {
	p.HistoryID = id
	p.Role = r
	return p
}

// ToolResultPart represents a tool execution result.
type ToolResultPart struct {
	ContentMeta
	ID      string        `json:"id"`
	Content []ContentPart `json:"content"`
	IsError bool          `json:"is_error"`
}

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
	StreamMessages(ctx context.Context, messages []Message, tools []ToolDefinition, systemPrompt string, extraSystemPrompt string) (iter.Seq2[StreamEvent, error], error)
	SetReasoningLevel(level int)
}
