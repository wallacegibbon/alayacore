// Package providers implements LLM provider clients
package providers

// OpenAI Provider Gotchas:
//
// 1. TOOL CALL ARGUMENTS CHUNKING: OpenAI-compatible APIs split tool call arguments
//    across multiple delta events. Critical: subsequent chunks have `"id": ""` (empty)
//    but correct `"index"`. Must use `index` (not `id`) to associate argument chunks
//    with their tool call. See `appendToolCallArgs()` in openAIStreamState.
//
// 2. TOOL CALL ARGUMENTS IN REQUESTS: When sending tool calls back in conversation
//    history, arguments must be marshaled to a JSON string (not raw JSON).
//    See `convertToolCalls()`.
//
// 3. REASONING SUPPORT: OpenAI-compatible APIs (DeepSeek, Qwen, etc.) use
//    `reasoning_content` field for thinking tokens. Handled in `handleEvent()`.
//
// 4. REASONING_CONTENT IN TOOL CALL CHAINS: Per DeepSeek's documentation,
//    between two user messages all intermediate assistant reasoning_content
//    must be passed back. When reasoning mode is enabled, reasoning_content
//    is always set (even as empty string) on assistant messages so that
//    messages containing only tool calls still satisfy this requirement.
//    Conditional on reasoning mode to avoid wasting tokens. The logic lives
//    in openaiConvertMessages, not in the sub-converters.
//
// 5. NULL ARGUMENTS IN TOOL CALL CHUNKS: Some providers emit no-op deltas
//    with "arguments": null. Must be skipped to avoid corrupting the
//    accumulated arguments string. See docs/architecture.md →
//    "Null arguments in tool call chunks".
//    See `appendToolCallArgs()`.
//
// 6. TEXT CONTENT WITH TOOL CALLS: Some providers (DeepSeek, Qwen) return
//    text content alongside tool calls in the same assistant message.
//    openaiConvertToolCalls preserves text content when present, so
//    multi-turn tool call chains don't lose the assistant's commentary.

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
// OpenAI Wire Format Types
// ============================================================================

type openAIRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIMessage      `json:"messages"`
	Tools               []openAITool         `json:"tools,omitempty"`
	Stream              bool                 `json:"stream"`
	StreamOptions       *openAIStreamOptions `json:"stream_options,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Temperature         float64              `json:"temperature,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Thinking            *openAIThinkingField `json:"thinking"`
}

// openAIThinkingField maps to the OpenAI/DeepSeek `thinking` API field.
// The wire name is "thinking" (provider API convention), while the
// codebase uses "reasoning" for the domain-level concept.
type openAIThinkingField struct {
	Type string `json:"type"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	Index    int            `json:"index"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIToolFunc `json:"function"`
}

type openAIToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// openAIDelta represents the delta content from a streaming chunk.
type openAIDelta struct {
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
}

// ============================================================================
// OpenAI Provider
// ============================================================================

// OpenAIProvider implements the OpenAI API
type OpenAIProvider struct {
	baseProvider
}

// OpenAIOption configures the provider
type OpenAIOption func(*OpenAIProvider)

// NewOpenAI creates a new OpenAI provider
func NewOpenAI(opts ...OpenAIOption) (*OpenAIProvider, error) {
	p := &OpenAIProvider{
		baseProvider: newBaseProvider("", "https://api.openai.com/v1", "gpt-4o", llm.DefaultMaxTokens),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// WithOpenAIAPIKey sets the API key
func WithOpenAIAPIKey(key string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.apiKey = key
	}
}

// WithOpenAIBaseURL sets the base URL
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithOpenAIHTTPClient sets the HTTP client
func WithOpenAIHTTPClient(client *http.Client) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.client = client
	}
}

// WithOpenAIModel sets the model
func WithOpenAIModel(model string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.model = model
	}
}

// WithOpenAIMaxTokens sets the maximum output tokens
func WithOpenAIMaxTokens(tokens int) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.maxTokens = tokens
	}
}

// SetReasoningLevel sets the reasoning level for OpenAI.
// 0=off, 1=high, 2=xhigh.
func (p *OpenAIProvider) SetReasoningLevel(level int) {
	p.reasoningLevel = level
}

// StreamMessages streams messages from OpenAI
func (p *OpenAIProvider) StreamMessages(
	ctx context.Context,
	messages []llm.Message,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[llm.StreamEvent, error], error) {
	// Convert messages to OpenAI format
	apiMessages := make([]openAIMessage, 0, len(messages)+2)
	if systemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{Role: "system", Content: systemPrompt})
	}
	if extraSystemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{Role: "system", Content: extraSystemPrompt})
	}
	apiMessages = append(apiMessages, openaiConvertMessages(messages, p.reasoningLevel)...)

	// Convert tools to OpenAI format
	apiTools := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, openAITool{
			Type: "function",
			Function: openAIToolFunc{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Schema,
			},
		})
	}

	// Build request body with thinking config
	tc := computeReasoningConfig(p.reasoningLevel)
	var reasoningEffort string
	var thinking *openAIThinkingField
	if tc.Enabled {
		thinking = &openAIThinkingField{Type: "enabled"}
		reasoningEffort = tc.Effort
		if p.reasoningLevel >= config.ReasoningLevelMax {
			reasoningEffort = "xhigh"
		}
	} else {
		thinking = &openAIThinkingField{Type: "disabled"}
	}

	reqBody := openAIRequest{
		Model:    p.model,
		Messages: apiMessages,
		Tools:    apiTools,
		Stream:   true,
		StreamOptions: &openAIStreamOptions{
			IncludeUsage: true,
		},
		Thinking:        thinking,
		ReasoningEffort: reasoningEffort,
	}
	if p.maxTokens > 0 {
		reqBody.MaxCompletionTokens = p.maxTokens
	}

	// Build and send HTTP request
	req, err := p.buildRequest(ctx, "/chat/completions", reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	body, err := p.doRequest(req)
	if err != nil {
		return nil, err
	}

	return p.parseStream(body), nil
}

// ============================================================================
// SSE Stream Parsing (OpenAI data-only format)
// ============================================================================

// parseStream returns an iterator that yields SSE events from the OpenAI response.
func (p *OpenAIProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
		}()

		state := &openAIStreamState{}
		scanner := newSSEScanner(reader)

		for scanner.Next() {
			_, data := scanner.Event()
			if data == "[DONE]" {
				break
			}
			if !p.handleEvent(data, yield, state) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
			return
		}

		// Finalize tool calls and emit events
		state.finalizeToolCalls()
		for _, tc := range state.getToolCalls() {
			if !yield(llm.ToolCallEvent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Input:      tc.Input,
			}, nil) {
				return
			}
		}

		yield(llm.StepCompleteEvent{
			Messages:   []llm.Message{state.getMessage()},
			Usage:      state.getUsage(),
			StopReason: state.getStopReason(),
		}, nil)
	}
}

// openAIStreamState tracks state across streaming events
type openAIStreamState struct {
	streamUsage
	textBuilder      strings.Builder
	reasoningBuilder strings.Builder
	toolCallArgs     map[int]*strings.Builder // tool call index -> arguments builder
	toolCalls        []llm.ToolCallPart
}

func (s *openAIStreamState) addTextDelta(delta string) {
	s.textBuilder.WriteString(delta)
}

func (s *openAIStreamState) addReasoningDelta(delta string) {
	s.reasoningBuilder.WriteString(delta)
}

func (s *openAIStreamState) appendToolCallArgs(index int, args json.RawMessage) {
	if s.toolCallArgs == nil {
		s.toolCallArgs = make(map[int]*strings.Builder)
	}
	if _, exists := s.toolCallArgs[index]; !exists {
		s.toolCallArgs[index] = &strings.Builder{}
	}
	if len(args) > 0 && args[0] == '"' {
		var unquoted string
		if err := json.Unmarshal(args, &unquoted); err == nil {
			s.toolCallArgs[index].WriteString(unquoted)
			return
		}
	}
	if string(args) != "null" {
		s.toolCallArgs[index].WriteString(string(args))
	}
}

func (s *openAIStreamState) setToolCallName(index int, id, name string) {
	for len(s.toolCalls) <= index {
		s.toolCalls = append(s.toolCalls, llm.ToolCallPart{Type: llm.ContentPartToolUse})
	}
	s.toolCalls[index].ToolCallID = id
	s.toolCalls[index].ToolName = name
}

func (s *openAIStreamState) finalizeToolCalls() {
	for i := range s.toolCalls {
		if builder, exists := s.toolCallArgs[i]; exists {
			s.toolCalls[i].Input = json.RawMessage(builder.String())
		}
	}
}

func (s *openAIStreamState) getToolCalls() []llm.ToolCallPart {
	return s.toolCalls
}

func (s *openAIStreamState) getMessage() llm.Message {
	content := make([]llm.ContentPart, 0, 2+len(s.toolCalls))
	if s.reasoningBuilder.Len() > 0 {
		content = append(content, llm.ReasoningPart{
			Type: llm.ContentPartReasoning,
			Text: s.reasoningBuilder.String(),
		})
	}
	if s.textBuilder.Len() > 0 {
		content = append(content, llm.TextPart{
			Type: llm.ContentPartText,
			Text: s.textBuilder.String(),
		})
	}
	for _, tc := range s.toolCalls {
		content = append(content, tc)
	}
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: content,
	}
}

// ============================================================================
// Event Handlers
// ============================================================================

// handleEvent handles a single SSE data event. Returns false if iteration should stop.
func (p *OpenAIProvider) handleEvent(data string, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
	var streamResp struct {
		Choices []struct {
			Delta        openAIDelta `json:"delta"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		yield(nil, fmt.Errorf("failed to parse event: %w", err))
		return false
	}

	for _, choice := range streamResp.Choices {
		if ok, err := p.checkFinishReason(choice.FinishReason); !ok {
			yield(nil, err)
			return false
		}
		if choice.FinishReason != "" {
			state.setStopReason(choice.FinishReason)
		}
		if ok := p.handleDelta(choice.Delta, yield, state); !ok {
			return false
		}
	}

	if streamResp.Usage.PromptTokens > 0 || streamResp.Usage.CompletionTokens > 0 {
		state.setUsage(llm.Usage{
			InputTokens:  int64(streamResp.Usage.PromptTokens),
			OutputTokens: int64(streamResp.Usage.CompletionTokens),
		})
	}

	return true
}

// checkFinishReason validates the finish reason.
func (p *OpenAIProvider) checkFinishReason(reason string) (bool, error) {
	if reason == "content_filter" {
		return false, fmt.Errorf("content blocked by safety filter")
	}
	if reason != "" && reason != "stop" && reason != "length" && reason != "tool_calls" {
		return false, fmt.Errorf("stream finished with unexpected reason: %s", reason)
	}
	return true, nil
}

// handleDelta processes the delta content from a streaming chunk.
func (p *OpenAIProvider) handleDelta(delta openAIDelta, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
	if delta.ReasoningContent != "" {
		state.addReasoningDelta(delta.ReasoningContent)
		if !yield(llm.ReasoningDeltaEvent{Delta: delta.ReasoningContent}, nil) {
			return false
		}
	}

	if delta.Content != "" {
		state.addTextDelta(delta.Content)
		if !yield(llm.TextDeltaEvent{Delta: delta.Content}, nil) {
			return false
		}
	}

	for _, tc := range delta.ToolCalls {
		if len(tc.Function.Arguments) > 0 {
			state.appendToolCallArgs(tc.Index, tc.Function.Arguments)
		}
		if tc.Function.Name != "" {
			state.setToolCallName(tc.Index, tc.ID, tc.Function.Name)
			if !yield(llm.ToolCallStartEvent{
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
			}, nil) {
				return false
			}
		}
	}

	return true
}

// ============================================================================
// Message Conversion (OpenAI wire format)
// ============================================================================

// openaiConvertMessages converts domain messages to OpenAI wire format.
func openaiConvertMessages(messages []llm.Message, reasoningLevel int) []openAIMessage {
	apiMessages := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llm.RoleTool {
			apiMessages = append(apiMessages, openaiConvertToolResults(msg.Content)...)
			continue
		}

		apiMsg := openAIMessage{Role: string(msg.Role)}

		if msg.Role == llm.RoleAssistant && openaiHasToolCalls(msg.Content) {
			openaiConvertToolCalls(&apiMsg, msg.Content)
		} else {
			openaiConvertRegularContent(&apiMsg, msg.Content)
		}

		if msg.Role == llm.RoleAssistant {
			reasoningText := openaiExtractReasoning(msg.Content)
			if reasoningText != "" || reasoningLevel > config.ReasoningLevelOff {
				apiMsg.ReasoningContent = &reasoningText
			}
			if apiMsg.Content == nil && len(apiMsg.ToolCalls) == 0 {
				apiMsg.Content = ""
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}
	return apiMessages
}

// openaiConvertToolResults converts tool result content to multiple OpenAI messages
func openaiConvertToolResults(content []llm.ContentPart) []openAIMessage {
	results := make([]openAIMessage, 0, len(content))
	for _, part := range content {
		tr, ok := part.(llm.ToolResultPart)
		if !ok {
			continue
		}
		apiMsg := openAIMessage{
			Role:       string(llm.RoleTool),
			ToolCallID: tr.ToolCallID,
		}
		switch out := tr.Output.(type) {
		case llm.ToolResultOutputText:
			apiMsg.Content = out.Text
		case llm.ToolResultOutputError:
			apiMsg.Content = out.Error
		}
		results = append(results, apiMsg)
	}
	return results
}

// openaiHasToolCalls checks if content contains tool calls
func openaiHasToolCalls(content []llm.ContentPart) bool {
	for _, part := range content {
		if _, ok := part.(llm.ToolCallPart); ok {
			return true
		}
	}
	return false
}

// openaiExtractReasoning returns the concatenated text of all ReasoningParts.
func openaiExtractReasoning(content []llm.ContentPart) string {
	var text string
	for _, part := range content {
		if r, ok := part.(llm.ReasoningPart); ok {
			text += r.Text
		}
	}
	return text
}

// openaiConvertToolCalls handles conversion of assistant messages with tool calls.
func openaiConvertToolCalls(apiMsg *openAIMessage, content []llm.ContentPart) {
	apiMsg.ToolCalls = make([]openAIToolCall, 0)
	var textParts []string
	for _, part := range content {
		switch v := part.(type) {
		case llm.ToolCallPart:
			argsStr, err := json.Marshal(string(v.Input))
			if err != nil {
				argsStr = []byte("{}")
			}
			apiMsg.ToolCalls = append(apiMsg.ToolCalls, openAIToolCall{
				ID:   v.ToolCallID,
				Type: "function",
				Function: openAIFunction{
					Name:      v.ToolName,
					Arguments: argsStr,
				},
			})
		case llm.TextPart:
			textParts = append(textParts, v.Text)
		}
	}
	if len(textParts) > 0 {
		apiMsg.Content = strings.Join(textParts, "")
	}
}

// openaiConvertRegularContent handles conversion of regular text content.
func openaiConvertRegularContent(apiMsg *openAIMessage, content []llm.ContentPart) {
	var contentParts []map[string]any
	for _, part := range content {
		if v, ok := part.(llm.TextPart); ok {
			contentParts = append(contentParts, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		}
	}
	switch len(contentParts) {
	case 1:
		apiMsg.Content = contentParts[0]["text"]
	case 0:
		// No content parts
	default:
		apiMsg.Content = contentParts
	}
}
