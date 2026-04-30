// Package llm provides a custom LLM client with streaming support
package llm

import (
	"context"
	"encoding/json"
	"iter"
)

// DefaultMaxTokens is the default maximum output tokens when the user
// doesn't specify one. Safe for all current models (Claude 3.5 maxes
// at 8K; newer models support more).
const DefaultMaxTokens = 8192

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
	isContentPart()
}

// Content part type constants. These are the canonical domain-level type
// strings used in ContentPart implementations. Each provider maps these to
// its own wire-format type (e.g., Anthropic maps ContentPartReasoning to "thinking").
const (
	ContentPartText       = "text"
	ContentPartReasoning  = "reasoning"
	ContentPartToolUse    = "tool_use"
	ContentPartToolResult = "tool_result"
)

// TextPart represents text content
type TextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextPart) isContentPart() {}

// ReasoningPart represents reasoning/thinking content
type ReasoningPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (ReasoningPart) isContentPart() {}

// ToolCallPart represents a tool call
type ToolCallPart struct {
	Type       string          `json:"type"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Input      json.RawMessage `json:"input"`
}

func (ToolCallPart) isContentPart() {}

// ToolResultPart represents a tool execution result
type ToolResultPart struct {
	Type       string           `json:"type"`
	ToolCallID string           `json:"tool_call_id"`
	Output     ToolResultOutput `json:"output"`
}

func (ToolResultPart) isContentPart() {}

// ToolResultOutput represents the output of a tool
type ToolResultOutput interface {
	isToolResultOutput()
}

// ToolResultOutputText represents text output
type ToolResultOutputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (ToolResultOutputText) isToolResultOutput() {}

// ToolResultOutputError represents error output
type ToolResultOutputError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

func (ToolResultOutputError) isToolResultOutput() {}

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

// Usage tracks token usage
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
}

func (TextDeltaEvent) isStreamEvent() {}

// ReasoningDeltaEvent represents reasoning content streaming
type ReasoningDeltaEvent struct {
	Delta string
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ToolCallEvent represents a tool call
type ToolCallEvent struct {
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
}

func (ToolCallEvent) isStreamEvent() {}

// StepCompleteEvent represents completion of an agentic step
type StepCompleteEvent struct {
	Messages   []Message
	Usage      Usage
	StopReason string // "end_turn", "stop", "max_tokens", "length", etc.
}

func (StepCompleteEvent) isStreamEvent() {}

// Provider defines the interface for LLM providers
type Provider interface {
	// StreamMessages streams a conversation with tools
	// systemPrompt is the base system prompt (always present)
	// extraSystemPrompt is the user-provided additional system prompt (optional, from --system flag)
	// The provider should merge them appropriately (joined by "\n\n" if both present)
	StreamMessages(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		systemPrompt string,
		extraSystemPrompt string,
	) (iter.Seq2[StreamEvent, error], error)

	// SetReasoningLevel sets the think/reasoning level.
	// 0 = off, 1 = normal (high), 2 = maximum.
	SetReasoningLevel(level int)
}
