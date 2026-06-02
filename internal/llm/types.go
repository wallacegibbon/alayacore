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

	// RoleTool marks a message containing tool (function) results.
	//
	// It exists because OpenAI's wire format requires each tool result to be a
	// separate message with its own tool_call_id:
	//
	//   {"role": "tool", "tool_call_id": "call_abc", "content": "result1"}
	//   {"role": "tool", "tool_call_id": "call_def", "content": "result2"}
	//
	// The OpenAI converter (openai.go) uses RoleTool as an early-exit gate:
	// it detects these messages by role, explodes their content parts into N
	// individual wire messages, and skips the normal content conversion.
	//
	// Anthropic handles tool results differently — they are collapsed into a
	// single "user" message whose content blocks carry the per-result data.
	// The Anthropic converter (anthropic.go) maps RoleTool → "user" on the wire.
	//
	// Without a dedicated role, every message would need content-type sniffing
	// to decide whether to split (OpenAI) or collapse (Anthropic).
	RoleTool MessageRole = "tool"
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
	ContentPartImage      = "image"
	ContentPartReasoning  = "reasoning"
	ContentPartToolUse    = "tool_use"
	ContentPartToolResult = "tool_result"
)

// TextPart represents text content
type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) isContentPart() {}

// ImagePart represents an image content (DataURI: data:image/...;base64,...)
type ImagePart struct {
	DataURL string `json:"data_url"`
}

func (ImagePart) isContentPart() {}

// ReasoningPart represents reasoning/thinking content.
// Signature is Anthropic-specific: it verifies thinking block integrity
// and must be passed back to the API exactly as received. Empty for
// providers that don't use signatures (OpenAI, DeepSeek, etc.).
type ReasoningPart struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

func (ReasoningPart) isContentPart() {}

// ToolUsePart represents a tool call
type ToolUsePart struct {
	ID       string          `json:"id"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

func (ToolUsePart) isContentPart() {}

// ToolResultPart represents a tool execution result
type ToolResultPart struct {
	ID     string           `json:"id"`
	Output ToolResultOutput `json:"output"`
}

func (ToolResultPart) isContentPart() {}

// ToolResultOutput represents the output of a tool
type ToolResultOutput interface {
	isToolResultOutput()
}

// ToolResultOutputText represents text output
type ToolResultOutputText struct {
	Text string `json:"text"`
}

func (ToolResultOutputText) isToolResultOutput() {}

// ToolResultOutputFailed represents a failed tool execution result.
type ToolResultOutputFailed struct {
	Reason string `json:"reason"`
}

func (ToolResultOutputFailed) isToolResultOutput() {}

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
// For Anthropic: InputTokens excludes cached tokens (per their API docs:
// "input_tokens: Number of input tokens which were not read from or
// used to create a cache"). Sum all three fields for total context.
// For OpenAI-compatible APIs: Cache* fields are always 0.
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

// ToolUseStartEvent signals that a tool use has started (name and ID known,
// but arguments may still be streaming). Providers emit this as soon as the
// tool name is available so the UI can show a placeholder window immediately,
// before the potentially large argument payload finishes streaming.
type ToolUseStartEvent struct {
	ID       string
	ToolName string
}

func (ToolUseStartEvent) isStreamEvent() {}

// ToolUseEvent represents a complete tool use (all arguments received).
type ToolUseEvent struct {
	ID       string
	ToolName string
	Input    json.RawMessage
}

func (ToolUseEvent) isStreamEvent() {}

// StepCompleteEvent represents completion of an agentic step.
// The provider emits this as the final event after accumulating all streaming
// deltas. Message is a single assistant Message with role RoleAssistant,
// containing all content parts (text, reasoning, tool calls) built
// incrementally during streaming.
type StepCompleteEvent struct {
	Message    Message
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

	// SetReasoningLevel sets the reasoning level.
	// 0 = off, 1 = normal (high), 2 = maximum.
	SetReasoningLevel(level int)
}
