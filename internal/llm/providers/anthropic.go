// Package providers implements LLM provider clients
package providers

// Anthropic Provider Gotchas:
//
// 1. PROMPT CACHING: System message must be ≥1024 tokens for caching to activate.
//    Shorter prompts won't be cached even with cache_control set.
//
// 2. CACHE CONTROL PLACEMENT: Cache control is applied as a top-level field on the
//    request body (Anthropic's automatic caching). It is NOT applied to individual
//    system messages.
//
// 3. DUAL SYSTEM PROMPT: --system flag appends extra system prompt rather than
//    replacing default. Both prompts become separate system messages, each with
//    cache_control for Anthropic APIs.
//
// 4. PROMPT CACHE PER-MODEL: prompt_cache: true in model.conf enables cache_control
//    markers for Anthropic. Other providers auto-cache and ignore this setting.
//
// 5. EMPTY THINKING BLOCK PADDING: Per DeepSeek's documentation, between two
//    user messages all intermediate assistant reasoning_content must be passed
//    back. When reasoning mode is enabled, every assistant message is padded
//    with an empty "thinking" block if none is present, so that assistant
//    messages containing only tool calls still satisfy this requirement.
//    Conditional on reasoning mode to avoid wasting tokens when thinking is off.
//    See docs/architecture.md → "Empty thinking block padding".

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
)

const (
	blockTypeToolUse             = "tool_use"
	anthropicBlockTypeText       = "text"
	anthropicBlockTypeThinking   = "thinking"
	anthropicBlockTypeToolResult = "tool_result"

	// Anthropic SSE delta types
	anthropicDeltaTypeText      = "text_delta"
	anthropicDeltaTypeThinking  = "thinking_delta"
	anthropicDeltaTypeInputJSON = "input_json_delta"
)

// AnthropicProvider implements the Anthropic API
type AnthropicProvider struct {
	apiKey         string
	baseURL        string
	client         *http.Client
	model          string
	promptCache    bool
	maxTokens      int
	reasoningLevel int // 0=off, 1=high, 2=max
}

// AnthropicOption configures the provider
type AnthropicOption func(*AnthropicProvider)

// NewAnthropic creates a new Anthropic provider
func NewAnthropic(opts ...AnthropicOption) (*AnthropicProvider, error) {
	p := &AnthropicProvider{
		baseURL:   "https://api.anthropic.com",
		client:    &http.Client{Timeout: 10 * time.Minute},
		model:     "claude-3-5-sonnet-20241022",
		maxTokens: llm.DefaultMaxTokens,
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

// WithPromptCache enables prompt caching for Anthropic
func WithPromptCache(enabled bool) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.promptCache = enabled
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

// anthropicRequest represents the Anthropic API request
type anthropicRequest struct {
	Model        string                   `json:"model"`
	Messages     []anthropicMessage       `json:"messages"`
	MaxTokens    int                      `json:"max_tokens"`
	System       []anthropicSystemMessage `json:"system,omitempty"`
	Tools        []anthropicTool          `json:"tools,omitempty"`
	Stream       bool                     `json:"stream"`
	CacheControl *anthropicCacheControl   `json:"cache_control,omitempty"`
	Thinking     *anthropicThinking       `json:"thinking"`
	OutputConfig *anthropicOutputConfig   `json:"output_config,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicSystemMessage struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
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
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`

	// For thinking (extended thinking)
	// Pointer so we can emit `"thinking": ""` (DeepSeek requires empty thinking block)
	// vs. omitting the field on non-thinking blocks.
	Thinking *string `json:"thinking,omitempty"`

	// Cache control
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicStreamState tracks accumulation state during streaming
type anthropicStreamState struct {
	streamUsage
	contentParts []llm.ContentPart

	// Current block being accumulated
	currentIndex int
	currentType  string
	currentText  strings.Builder
	currentInput strings.Builder
	currentID    string
	currentName  string
}

func (s *anthropicStreamState) startBlock(index int, blockType, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentIndex = index
	s.currentType = blockType
	s.currentID = id
	s.currentName = name
	s.currentText.Reset()
	s.currentInput.Reset()
}

func (s *anthropicStreamState) appendText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentText.WriteString(text)
}

func (s *anthropicStreamState) appendInput(jsonStr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentInput.WriteString(jsonStr)
}

func (s *anthropicStreamState) finishBlock() {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.currentType {
	case anthropicBlockTypeText:
		s.contentParts = append(s.contentParts, llm.TextPart{
			Type: llm.ContentPartText,
			Text: s.currentText.String(),
		})
	case anthropicBlockTypeThinking:
		s.contentParts = append(s.contentParts, llm.ReasoningPart{
			Type: llm.ContentPartReasoning,
			Text: s.currentText.String(),
		})
	case blockTypeToolUse:
		s.contentParts = append(s.contentParts, llm.ToolCallPart{
			Type:       blockTypeToolUse,
			ToolCallID: s.currentID,
			ToolName:   s.currentName,
			Input:      json.RawMessage(s.currentInput.String()),
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

func (s *anthropicStreamState) getMessage() llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: append([]llm.ContentPart{}, s.contentParts...),
	}
}

// lastToolCall returns the last tool call if the current block is a tool_use
func (s *anthropicStreamState) lastToolCall() *llm.ToolCallPart {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentType == blockTypeToolUse {
		return &llm.ToolCallPart{
			Type:       blockTypeToolUse,
			ToolCallID: s.currentID,
			ToolName:   s.currentName,
			Input:      json.RawMessage(s.currentInput.String()),
		}
	}
	return nil
}

// anthropicConvertMessages converts domain messages to Anthropic wire format.
//
// Wire-format mappings:
//   - llm.TextPart       → anthropicContentBlock{Type: "text"}
//   - llm.ReasoningPart  → anthropicContentBlock{Type: "thinking"}  (domain "reasoning" → wire "thinking")
//   - llm.ToolCallPart   → anthropicContentBlock{Type: "tool_use"}
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
			switch v := part.(type) {
			case llm.TextPart:
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type: llm.ContentPartText,
					Text: v.Text,
				})
			case llm.ReasoningPart:
				// Domain "reasoning" maps to Anthropic wire format "thinking"
				text := v.Text
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:     anthropicBlockTypeThinking,
					Thinking: &text,
				})
			case llm.ToolCallPart:
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:  blockTypeToolUse,
					ID:    v.ToolCallID,
					Name:  v.ToolName,
					Input: v.Input,
				})
			case llm.ToolResultPart:
				var content interface{}
				switch out := v.Output.(type) {
				case llm.ToolResultOutputText:
					content = out.Text
				case llm.ToolResultOutputError:
					content = out.Error
					apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
						Type:      anthropicBlockTypeToolResult,
						ToolUseID: v.ToolCallID,
						Content:   content,
						IsError:   true,
					})
					continue
				}
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:      anthropicBlockTypeToolResult,
					ToolUseID: v.ToolCallID,
					Content:   content,
				})
			}
		}

		// Per DeepSeek's documentation, intermediate assistant reasoning_content
		// must be passed back. Pad assistant messages with an empty "thinking"
		// block when reasoning mode is enabled and none is present, so that
		// tool-call-only messages still satisfy this requirement. Other providers
		// ignore the extra block. Only done when reasoning is enabled to avoid
		// wasting tokens.
		if reasoningLevel > config.ThinkLevelOff && msg.Role == llm.RoleAssistant {
			hasThinking := false
			for _, block := range apiMsg.Content {
				if block.Type == anthropicBlockTypeThinking {
					hasThinking = true
					break
				}
			}
			if !hasThinking {
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:     anthropicBlockTypeThinking,
					Thinking: ptrTo(""),
				})
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}
	return apiMessages
}

// StreamMessages streams messages from Anthropic
//
//nolint:gocyclo // message conversion requires multiple type switches
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

	// Add default system prompt
	if systemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{
			Type: anthropicBlockTypeText,
			Text: systemPrompt,
		})
	}

	// Add extra system prompt separately
	if extraSystemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{
			Type: anthropicBlockTypeText,
			Text: extraSystemPrompt,
		})
	}

	// Build request
	reqBody := anthropicRequest{
		Model:     p.model,
		Messages:  apiMessages,
		MaxTokens: p.maxTokens,
		System:    systemMessages,
		Tools:     apiTools,
		Stream:    true,
	}

	// Add top-level cache_control for automatic caching (Anthropic's automatic caching)
	if p.promptCache {
		reqBody.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	// Always include thinking config based on reasoning level.
	if p.reasoningLevel > config.ThinkLevelOff {
		reqBody.Thinking = &anthropicThinking{Type: "enabled"}
		effort := "high"
		if p.reasoningLevel >= config.ThinkLevelMax {
			effort = "max"
		}
		reqBody.OutputConfig = &anthropicOutputConfig{Effort: effort}
	} else {
		reqBody.Thinking = &anthropicThinking{Type: "disabled"}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("API error (status %d): failed to read error body: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Return iterator that reads from the response body
	return p.parseStream(resp.Body), nil
}

// parseStream returns an iterator that yields SSE events from the Anthropic response.
func (p *AnthropicProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			closer, ok := reader.(io.Closer)
			if ok {
				_ = closer.Close()
			}
		}()

		state := &anthropicStreamState{
			contentParts: make([]llm.ContentPart, 0),
		}

		scanner := bufio.NewScanner(reader)
		// Increase buffer size for large responses
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		var eventType string
		var eventData strings.Builder

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")
				eventData.Reset()
			case strings.HasPrefix(line, "data: "):
				eventData.WriteString(strings.TrimPrefix(line, "data: "))
			case line == "" && eventType != "":
				// Process complete event
				data := eventData.String()
				if !p.handleEvent(eventType, data, yield, state) {
					return
				}
				eventType = ""
				eventData.Reset()
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// ============================================================================
// SSE Event Types
// ============================================================================

// anthropicUsage represents token usage in SSE events.
// Fields use pointers so absent fields stay nil (zero-value merge logic).
type anthropicUsage struct {
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_input_tokens"`
	CreationTokens      *int64 `json:"cache_creation_input_tokens"`
}

// anthropicSSEMessageStart is the payload for "message_start" events.
type anthropicSSEMessageStart struct {
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

// anthropicSSEContentBlock is a content block in "content_block_start" events.
type anthropicSSEContentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// anthropicSSEContentBlockStart is the payload for "content_block_start" events.
type anthropicSSEContentBlockStart struct {
	Index        int                    `json:"index"`
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

// mergeUsage merges partial usage data from an SSE event into the current state.
// Only overwrites fields that are present (non-nil) in the incoming data.
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

// handleEvent handles a single SSE event. Returns false if iteration should stop.
func (p *AnthropicProvider) handleEvent(eventType, data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	if data == "" {
		return true
	}

	switch eventType {
	case "message_start":
		var event anthropicSSEMessageStart
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse message_start: %w", err))
			return false
		}
		p.mergeUsage(event.Message.Usage, state)
		return true

	case "content_block_start":
		var event anthropicSSEContentBlockStart
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse content_block_start: %w", err))
			return false
		}
		state.startBlock(event.Index, event.ContentBlock.Type, event.ContentBlock.ID, event.ContentBlock.Name)
		return true

	case "content_block_delta":
		var event anthropicSSEContentBlockDelta
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse content_block_delta: %w", err))
			return false
		}
		return p.handleContentDelta(event.Delta, yield, state)

	case "content_block_stop":
		return p.handleContentBlockStop(yield, state)

	case "message_delta":
		var event anthropicSSEMessageDelta
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse message_delta: %w", err))
			return false
		}
		if !p.handleStopReason(event.Delta.StopReason, yield, state) {
			return false
		}
		p.mergeUsage(event.Usage, state)
		return true

	case "message_stop":
		var event anthropicSSEMessageStop
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse message_stop: %w", err))
			return false
		}
		p.mergeUsage(event.Usage, state)
		yield(llm.StepCompleteEvent{
			Messages:   []llm.Message{state.getMessage()},
			Usage:      state.getUsage(),
			StopReason: state.stopReason,
		}, nil)
		return true

	case "ping":
		return true

	case "error":
		var event anthropicSSEError
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, fmt.Errorf("failed to parse error event: %w", err))
			return false
		}
		if event.Error.Message != "" {
			yield(nil, fmt.Errorf("API error: %s", event.Error.Message))
		} else {
			yield(nil, fmt.Errorf("unknown API error"))
		}
		return false
	}

	return true
}

// handleStopReason validates the stop reason and updates state.
// Returns false if the stop reason indicates an error.
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
	// Get the tool call info before finishBlock() clears it
	tc := state.lastToolCall()

	state.finishBlock()

	// If we just finished a tool_use block, emit ToolCallEvent
	if tc != nil {
		if !yield(llm.ToolCallEvent{
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.ToolName,
			Input:      tc.Input,
		}, nil) {
			return false
		}
	}
	return true
}

// ptrTo returns a pointer to the given string value.
func ptrTo(s string) *string { return &s }
