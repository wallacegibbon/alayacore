// Package providers implements LLM provider clients
package providers

// Anthropic Provider Gotchas:
//
// 1. DUAL SYSTEM PROMPT: --system flag appends extra system prompt rather than
//    replacing default. Both prompts become separate system messages.
//
// 2. EMPTY THINKING BLOCK PADDING: Per DeepSeek's documentation, between two
//    user messages all intermediate assistant reasoning_content must be passed
//    back. When reasoning mode is enabled, an empty "thinking" block is
//    prepended to every assistant message that lacks one, so that assistant
//    messages containing only tool calls still satisfy this requirement.
//    The thinking block must come first per Anthropic's API.
//    Conditional on reasoning mode to avoid wasting tokens when thinking is off.
//    See docs/architecture.md → "Empty thinking block padding".

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Anthropic Wire Format Types
// ============================================================================

const (
	anthropicBlockTypeText       = "text"
	anthropicBlockTypeImage      = "image"
	anthropicBlockTypeThinking   = "thinking"
	anthropicBlockTypeToolResult = "tool_result"
	anthropicBlockTypeToolUse    = "tool_use"

	// Anthropic SSE delta types
	anthropicDeltaTypeText      = "text_delta"
	anthropicDeltaTypeThinking  = "thinking_delta"
	anthropicDeltaTypeInputJSON = "input_json_delta"
)

// anthropicRequest represents the Anthropic API request
type anthropicRequest struct {
	Model        string                   `json:"model"`
	Messages     []anthropicMessage       `json:"messages"`
	MaxTokens    int                      `json:"max_tokens"`
	System       []anthropicSystemMessage `json:"system,omitempty"`
	Tools        []anthropicTool          `json:"tools,omitempty"`
	Stream       bool                     `json:"stream"`
	Thinking     *anthropicThinkingField  `json:"thinking"`
	OutputConfig *anthropicOutputConfig   `json:"output_config,omitempty"`
}

// anthropicThinkingField maps to the Anthropic `thinking` API field.
// The wire name is "thinking" (Anthropic API convention), while the
// codebase uses "reasoning" for the domain-level concept.
type anthropicThinkingField struct {
	Type string `json:"type"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort"`
}

type anthropicSystemMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// For tool use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// For thinking (extended thinking)
	// Pointer so we can emit `"thinking": ""` (DeepSeek requires empty thinking block)
	// vs. omitting the field on non-thinking blocks.
	Thinking *string `json:"thinking,omitempty"`
	// Signature verifies thinking block integrity. Only present on "thinking"
	// blocks — Anthropic requires it to be passed back exactly as received.
	// Omitted from JSON for non-thinking blocks (text, tool_use, etc.).
	Signature string `json:"signature,omitempty"`

	// For image
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ============================================================================
// SSE Event Types (Anthropic wire format)
// ============================================================================

// anthropicUsage represents token usage in SSE events.
// Fields use pointers so absent fields stay nil (zero-value merge logic).
type anthropicUsage struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheReadTokens *int64 `json:"cache_read_input_tokens"`
	CreationTokens  *int64 `json:"cache_creation_input_tokens"`
}

// anthropicSSEMessageStart is the payload for "message_start" events.
type anthropicSSEMessageStart struct {
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

// anthropicSSEContentBlock is a content block in "content_block_start" events.
type anthropicSSEContentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// anthropicSSEContentBlockStart is the payload for "content_block_start" events.
type anthropicSSEContentBlockStart struct {
	Index        int                      `json:"index"`
	ContentBlock anthropicSSEContentBlock `json:"content_block"`
}

// anthropicSSEDelta is the delta in "content_block_delta" events.
type anthropicSSEDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// anthropicSSEContentBlockDelta is the payload for "content_block_delta" events.
type anthropicSSEContentBlockDelta struct {
	Index int               `json:"index"`
	Delta anthropicSSEDelta `json:"delta"`
}

// anthropicSSEMessageDelta is the payload for "message_delta" events.
type anthropicSSEMessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

// anthropicSSEMessageStop is the payload for "message_stop" events.
type anthropicSSEMessageStop struct {
	Usage anthropicUsage `json:"usage"`
}

// anthropicSSEError is the payload for "error" events.
type anthropicSSEError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ============================================================================
// Anthropic Provider
// ============================================================================

// AnthropicProvider implements the Anthropic API
type AnthropicProvider struct {
	baseProvider
}

// AnthropicOption configures the provider
type AnthropicOption func(*AnthropicProvider)

// NewAnthropic creates a new Anthropic provider
func NewAnthropic(opts ...AnthropicOption) (*AnthropicProvider, error) {
	p := &AnthropicProvider{
		baseProvider: newBaseProvider("", "https://api.anthropic.com", "claude-3-5-sonnet-20241022", llm.DefaultMaxTokens),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// WithAPIKey sets the API key
func WithAPIKey(key string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.apiKey = key
	}
}

// WithBaseURL sets the base URL
func WithBaseURL(url string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithHTTPClient sets the HTTP client
func WithHTTPClient(client *http.Client) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.client = client
	}
}

// WithAnthropicModel sets the model name
func WithAnthropicModel(model string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.model = model
	}
}

// WithMaxTokens sets the maximum output tokens
func WithMaxTokens(tokens int) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.maxTokens = tokens
	}
}

// SetReasoningLevel sets the reasoning level for Anthropic.
// 0=off, 1=high, 2=max.
func (p *AnthropicProvider) SetReasoningLevel(level int) {
	p.reasoningLevel = level
}

// StreamMessages streams messages from Anthropic
func (p *AnthropicProvider) StreamMessages(
	ctx context.Context,
	messages []llm.Message,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[llm.StreamEvent, error], error) {
	// Convert messages to Anthropic format
	apiMessages := anthropicConvertMessages(messages, p.reasoningLevel)

	// Convert tools to Anthropic format
	apiTools := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
		})
	}

	// Build system messages array
	systemMessages := make([]anthropicSystemMessage, 0, 2)
	if systemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{Type: anthropicBlockTypeText, Text: systemPrompt})
	}
	if extraSystemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{Type: anthropicBlockTypeText, Text: extraSystemPrompt})
	}

	// Build request body
	reqBody := anthropicRequest{
		Model:     p.model,
		Messages:  apiMessages,
		MaxTokens: p.maxTokens,
		System:    systemMessages,
		Tools:     apiTools,
		Stream:    true,
		Thinking:  computeAnthropicReasoning(p.reasoningLevel),
	}

	// Build and send HTTP request
	req, err := p.buildRequest(ctx, "/v1/messages", reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	body, err := p.doRequest(req)
	if err != nil {
		return nil, err
	}

	return p.parseStream(body), nil
}

// computeAnthropicReasoning returns the thinking field for an Anthropic request.
func computeAnthropicReasoning(level int) *anthropicThinkingField {
	if level > config.ReasoningLevelOff {
		return &anthropicThinkingField{Type: "enabled"}
	}
	return &anthropicThinkingField{Type: "disabled"}
}

// ============================================================================
// SSE Stream Parsing (Anthropic named-event format)
// ============================================================================

// parseStream returns an iterator that yields SSE events from the Anthropic response.
func (p *AnthropicProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
		}()

		state := &anthropicStreamState{
			contentParts: make([]llm.ContentPart, 0),
		}
		scanner := newSSEScanner(reader)

		for scanner.Next() {
			eventType, data := scanner.Event()
			if data == "" {
				continue
			}
			if !p.handleEvent(eventType, data, yield, state) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// anthropicStreamState tracks accumulation state during streaming
type anthropicStreamState struct {
	streamUsage
	contentParts []llm.ContentPart

	// Current block being accumulated
	currentIndex     int
	currentType      string
	currentText      strings.Builder
	currentInput     strings.Builder
	currentID        string
	currentName      string
	currentSignature string
}

func (s *anthropicStreamState) startBlock(index int, blockType, id, name, signature string) {
	s.currentIndex = index
	s.currentType = blockType
	s.currentID = id
	s.currentName = name
	s.currentSignature = signature
	s.currentText.Reset()
	s.currentInput.Reset()
}

func (s *anthropicStreamState) appendText(text string) {
	s.currentText.WriteString(text)
}

func (s *anthropicStreamState) appendInput(jsonStr string) {
	s.currentInput.WriteString(jsonStr)
}

func (s *anthropicStreamState) finishBlock() {
	switch s.currentType {
	case anthropicBlockTypeText:
		s.contentParts = append(s.contentParts, llm.TextPart{
			Text: s.currentText.String(),
		})
	case anthropicBlockTypeThinking:
		s.contentParts = append(s.contentParts, llm.ReasoningPart{
			Text:      s.currentText.String(),
			Signature: s.currentSignature,
		})
	case anthropicBlockTypeToolUse:
		s.contentParts = append(s.contentParts, llm.ToolUsePart{
			ID:       s.currentID,
			ToolName: s.currentName,
			Input:    json.RawMessage(s.currentInput.String()),
		})
	}
	s.currentType = ""
}

func (s *anthropicStreamState) setUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64) {
	s.streamUsage.setUsage(llm.Usage{
		CacheCreationTokens: cacheCreationTokens,
		CacheReadTokens:     cacheReadTokens,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
	})
}

// getMessage wraps the accumulated contentParts into a domain Message.
// Unlike OpenAI's parallel accumulators (reasoningBuilder + textBuilder + toolCallArgs),
// Anthropic's block lifecycle model delivers content serially — each block starts, streams,
// and finishes before the next begins. finishBlock() already converted each block to the
// correct ContentPart type, so this function is a trivial wrapper.
func (s *anthropicStreamState) getMessage() llm.Message {
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: append([]llm.ContentPart{}, s.contentParts...),
	}
}

// lastToolCall returns the last tool call if the current block is a tool_use
func (s *anthropicStreamState) lastToolCall() *llm.ToolUsePart {
	if s.currentType == anthropicBlockTypeToolUse {
		return &llm.ToolUsePart{
			ID:       s.currentID,
			ToolName: s.currentName,
			Input:    json.RawMessage(s.currentInput.String()),
		}
	}
	return nil
}

// ============================================================================
// Event Handlers
// ============================================================================

// handleEvent handles a single SSE event. Returns false if iteration should stop.
func (p *AnthropicProvider) handleEvent(eventType, data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	switch eventType {
	case "message_start":
		event, ok := unmarshalSSE[anthropicSSEMessageStart](data, yield)
		if !ok {
			return false
		}
		p.mergeUsage(event.Message.Usage, state)

	case "content_block_start":
		return p.handleContentBlockStart(data, yield, state)

	case "content_block_delta":
		event, ok := unmarshalSSE[anthropicSSEContentBlockDelta](data, yield)
		if !ok {
			return false
		}
		return p.handleContentDelta(event.Delta, yield, state)

	case "content_block_stop":
		return p.handleContentBlockStop(yield, state)

	case "message_delta":
		return p.handleMessageDeltaEvent(data, yield, state)

	case "message_stop":
		event, ok := unmarshalSSE[anthropicSSEMessageStop](data, yield)
		if !ok {
			return false
		}
		p.mergeUsage(event.Usage, state)
		yield(llm.StepCompleteEvent{
			Message:    state.getMessage(),
			Usage:      state.getUsage(),
			StopReason: state.stopReason,
		}, nil)

	case "ping":
		// Ignore

	case "error":
		return p.handleSSEError(data, yield)
	}

	return true
}

// handleContentBlockStart handles content_block_start events.
func (p *AnthropicProvider) handleContentBlockStart(data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	event, ok := unmarshalSSE[anthropicSSEContentBlockStart](data, yield)
	if !ok {
		return false
	}
	state.startBlock(event.Index, event.ContentBlock.Type, event.ContentBlock.ID, event.ContentBlock.Name, event.ContentBlock.Signature)
	if event.ContentBlock.Type == anthropicBlockTypeToolUse {
		if !yield(llm.ToolUseStartEvent{
			ID:       event.ContentBlock.ID,
			ToolName: event.ContentBlock.Name,
		}, nil) {
			return false
		}
	}
	return true
}

// handleContentDelta handles content block delta events
func (p *AnthropicProvider) handleContentDelta(delta anthropicSSEDelta, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	switch delta.Type {
	case anthropicDeltaTypeText:
		state.appendText(delta.Text)
		if !yield(llm.TextDeltaEvent{Delta: delta.Text}, nil) {
			return false
		}
	case anthropicDeltaTypeThinking:
		state.appendText(delta.Thinking)
		if !yield(llm.ReasoningDeltaEvent{Delta: delta.Thinking}, nil) {
			return false
		}
	case anthropicDeltaTypeInputJSON:
		state.appendInput(delta.PartialJSON)
	}
	return true
}

// handleContentBlockStop handles content_block_stop events
func (p *AnthropicProvider) handleContentBlockStop(yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	tc := state.lastToolCall()
	state.finishBlock()
	if tc != nil {
		if !yield(llm.ToolUseEvent{
			ID:       tc.ID,
			ToolName: tc.ToolName,
			Input:    tc.Input,
		}, nil) {
			return false
		}
	}
	return true
}

// handleMessageDeltaEvent handles "message_delta" SSE events.
func (p *AnthropicProvider) handleMessageDeltaEvent(data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	event, ok := unmarshalSSE[anthropicSSEMessageDelta](data, yield)
	if !ok {
		return false
	}
	if !p.handleStopReason(event.Delta.StopReason, yield, state) {
		return false
	}
	p.mergeUsage(event.Usage, state)
	return true
}

// handleSSEError handles "error" SSE events.
func (p *AnthropicProvider) handleSSEError(data string, yield func(llm.StreamEvent, error) bool) bool {
	event, ok := unmarshalSSE[anthropicSSEError](data, yield)
	if !ok {
		return false
	}
	if event.Error.Message != "" {
		yield(nil, fmt.Errorf("API error: %s", event.Error.Message))
	} else {
		yield(nil, fmt.Errorf("unknown API error"))
	}
	return false
}

// handleStopReason validates the stop reason and updates state.
func (p *AnthropicProvider) handleStopReason(stopReason string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	if stopReason == "" {
		return true
	}
	if stopReason == "refusal" {
		yield(nil, fmt.Errorf("model refused to respond: content policy violation"))
		return false
	}
	valid := stopReason == "end_turn" || stopReason == "max_tokens" ||
		stopReason == "stop_sequence" || stopReason == "tool_use" || stopReason == "pause_turn"
	if !valid {
		yield(nil, fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason))
		return false
	}
	state.setStopReason(stopReason)
	return true
}

// mergeUsage merges partial usage data from an SSE event into the current state.
func (p *AnthropicProvider) mergeUsage(usage anthropicUsage, state *anthropicStreamState) {
	current := state.getUsage()
	if usage.InputTokens != nil {
		current.InputTokens = *usage.InputTokens
	}
	if usage.OutputTokens != nil {
		current.OutputTokens = *usage.OutputTokens
	}
	if usage.CacheReadTokens != nil {
		current.CacheReadTokens = *usage.CacheReadTokens
	}
	if usage.CreationTokens != nil {
		current.CacheCreationTokens = *usage.CreationTokens
	}
	state.setUsage(current.InputTokens, current.OutputTokens, current.CacheReadTokens, current.CacheCreationTokens)
}

// ============================================================================
// Generic SSE Unmarshaling Helper
// ============================================================================

// unmarshalSSE is a generic helper that unmarshals SSE event data into a typed struct.
// On error, it yields the error and returns the zero value with ok=false.
func unmarshalSSE[T any](data string, yield func(llm.StreamEvent, error) bool) (T, bool) {
	var event T
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		var zero T
		yield(nil, fmt.Errorf("failed to parse SSE event: %w", err))
		return zero, false
	}
	return event, true
}

// ============================================================================
// Message Conversion (Anthropic wire format)
// ============================================================================

// anthropicConvertMessages converts domain messages to Anthropic wire format.
//
// Wire-format mappings:
//   - llm.TextPart       → anthropicContentBlock{Type: "text"}
//   - llm.ReasoningPart  → anthropicContentBlock{Type: "thinking"}  (domain "reasoning" → wire "thinking")
//   - llm.ToolUsePart   → anthropicContentBlock{Type: "tool_use"}
//   - llm.ToolResultPart → anthropicContentBlock{Type: "tool_result"} (role remapped to "user")
func anthropicConvertMessages(messages []llm.Message, reasoningLevel int) []anthropicMessage {
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		apiMsg := anthropicMessage{
			Role:    string(msg.Role),
			Content: make([]anthropicContentBlock, 0, len(msg.Content)),
		}

		// In Anthropic API, tool results must be in a "user" role message
		if msg.Role == llm.RoleTool {
			apiMsg.Role = "user"
		}

		for _, part := range msg.Content {
			if block := anthropicPartToBlock(part); block != nil {
				apiMsg.Content = append(apiMsg.Content, *block)
			}
		}

		// Empty thinking block padding when reasoning is enabled
		if reasoningLevel > config.ReasoningLevelOff && msg.Role == llm.RoleAssistant {
			hasThinking := false
			for _, block := range apiMsg.Content {
				if block.Type == anthropicBlockTypeThinking {
					hasThinking = true
					break
				}
			}
			if !hasThinking {
				emptyStr := ""
				apiMsg.Content = append([]anthropicContentBlock{{
					Type:     anthropicBlockTypeThinking,
					Thinking: &emptyStr,
				}}, apiMsg.Content...)
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}
	return apiMessages
}

// anthropicPartToBlock converts a domain ContentPart to an Anthropic content block.
// Returns nil for unsupported parts.
func anthropicPartToBlock(part llm.ContentPart) *anthropicContentBlock {
	switch v := part.(type) {
	case llm.TextPart:
		return &anthropicContentBlock{
			Type: anthropicBlockTypeText,
			Text: v.Text,
		}
	case llm.ImagePart:
		mediaType, b64, ok := parseDataURI(v.DataURL)
		if !ok {
			return nil
		}
		return &anthropicContentBlock{
			Type: anthropicBlockTypeImage,
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      b64,
			},
		}
	case llm.ReasoningPart:
		text := v.Text
		return &anthropicContentBlock{
			Type:      anthropicBlockTypeThinking,
			Thinking:  &text,
			Signature: v.Signature,
		}
	case llm.ToolUsePart:
		return &anthropicContentBlock{
			Type:  anthropicBlockTypeToolUse,
			ID:    v.ID,
			Name:  v.ToolName,
			Input: v.Input,
		}
	case llm.ToolResultPart:
		block := &anthropicContentBlock{
			Type:      anthropicBlockTypeToolResult,
			ToolUseID: v.ID,
		}
		switch out := v.Output.(type) {
		case llm.ToolResultOutputText:
			block.Content = out.Text
		case llm.ToolResultOutputFailed:
			block.Content = out.Reason
			block.IsError = true
		}
		return block
	}
	return nil
}

// parseDataURI parses a Data URI into media type and base64 data.
// Input: "data:image/jpeg;base64,/9j/4AAQ..."
// Output: "image/jpeg", "/9j/4AAQ...", true
func parseDataURI(uri string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := uri[len(prefix):]
	semi := strings.IndexByte(rest, ';')
	if semi == -1 {
		return "", "", false
	}
	mediaType = rest[:semi]
	rest = rest[semi+1:]
	const b64Prefix = "base64,"
	if !strings.HasPrefix(rest, b64Prefix) {
		return "", "", false
	}
	return mediaType, rest[len(b64Prefix):], true
}
