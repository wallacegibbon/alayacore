package llm

// Agent Tool-Calling Gotchas:
//
// 1. ONSTEPFINISH RECEIVES FULL HISTORY: OnStepFinish callback receives
//    the complete allMessages slice (full conversation history), not just
//    the current step's messages. The session layer replaces its state
//    from this rather than appending increments. OnToolUseOutput should only
//    send UI notifications, not append to session messages.
//
// 2. INCOMPLETE TOOL CALLS ON CANCEL: When user cancels mid-tool-call, messages may have
//    tool_use without matching tool_result. Clean up these orphaned tool uses before the
//    next API request to prevent errors.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
)

// ErrMaxStepsExceeded is returned when the agent loop reaches the configured maximum number of steps
// without the model producing a final text-only response.
var ErrMaxStepsExceeded = errors.New("agent loop exceeded maximum steps")

// ErrResponseTruncated is returned when the model's response was cut short
// due to hitting the output token limit (max_tokens / length).
var ErrResponseTruncated = errors.New("response truncated: hit output token limit")

// Tool represents an executable tool
type Tool struct {
	Definition ToolDefinition
	Execute    func(ctx context.Context, input json.RawMessage) ([]ContentPart, error)
}

// AgentConfig configures the agent
type AgentConfig struct {
	Provider          Provider
	Tools             []Tool
	SystemPrompt      string // Default system prompt (base)
	ExtraSystemPrompt string // User-provided extra system prompt via --system flag
	MaxSteps          int
}

// Agent orchestrates tool-calling loops
type Agent struct {
	config AgentConfig
}

// NewAgent creates a new agent
func NewAgent(config AgentConfig) *Agent {
	return &Agent{config: config}
}

// StreamCallbacks receives streaming events
type StreamCallbacks struct {
	OnTextDelta      func(delta string, historyID uint64) error
	OnReasoningDelta func(delta string, historyID uint64) error
	OnToolUseStart   func(toolCallID, toolName string, historyID uint64) error
	OnToolUseInput   func(toolCallID string, input json.RawMessage, historyID uint64) error
	OnToolUseOutput  func(toolCallID string, content []ContentPart, err error, historyID uint64) error
	OnToolConfirm    func(requests []ToolConfirmRequest) <-chan ToolConfirmResponse
	OnStepStart      func(step int) error
	OnStepFinish     func(messages []Message, usage Usage) error

	// IDGen provides unique history IDs. Called once per content block
	// (first delta for AT/AR, once for each AF/UF). The returned ID is
	// passed to callbacks and stored on the ContentPart.
	IDGen func() uint64
}

// ToolConfirmRequest represents a single tool call awaiting user confirmation.
type ToolConfirmRequest struct {
	ID       string          `json:"id"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

// ToolConfirmResponse represents the user's decision for a specific tool call.
// If Error is non-empty, the tool result is recorded as failed with that reason.
// If Allowed is false (and Error is empty), the tool is recorded as denied by user.
type ToolConfirmResponse struct {
	ID      string `json:"id"`
	Allowed bool   `json:"allowed"`
	Error   string `json:"error,omitempty"`
}

// StreamResult is the final result of streaming.
// Messages is the full conversation history (allMessages).
// Usage is the total token usage summed across all steps.
//
// Note: Both fields are also available per-step via OnStepFinish callback.
// StreamResult serves as a convenience return for callers that don't use
// the callback or want a final summary after Stream() returns.
type StreamResult struct {
	Messages []Message
	Usage    Usage
}

// Stream executes the agent with streaming callbacks.
// Tools are confirmed and executed as soon as their arguments finish streaming
// (on ToolUseCompleteEvent), overlapping with other tools still being streamed.
func (a *Agent) Stream(ctx context.Context, messages []Message, callbacks StreamCallbacks) (*StreamResult, error) {
	allMessages := make([]Message, len(messages))
	copy(allMessages, messages)

	var totalUsage Usage

	// 0 means unlimited, map to MaxInt so the loop runs as long as needed.
	maxSteps := a.config.MaxSteps
	if maxSteps == 0 {
		maxSteps = math.MaxInt
	}

	for step := 1; step <= maxSteps; step++ {
		if callbacks.OnStepStart != nil {
			if err := callbacks.OnStepStart(step); err != nil {
				return nil, err
			}
		}

		events, err := a.config.Provider.StreamMessages(ctx, allMessages, a.toolDefinitions(), a.config.SystemPrompt, a.config.ExtraSystemPrompt)
		if err != nil {
			return nil, fmt.Errorf("provider stream failed: %w", err)
		}

		stepMessages, stepUsage, truncated, err := a.streamEvents(ctx, events, callbacks)
		if err != nil {
			return nil, err
		}

		allMessages = append(allMessages, stepMessages...)

		if callbacks.OnStepFinish != nil {
			if err := callbacks.OnStepFinish(allMessages, stepUsage); err != nil {
				return nil, err
			}
		}

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens

		// len(stepMessages) == 1 means no more tooluse.
		if truncated || len(stepMessages) == 1 {
			result := &StreamResult{Messages: allMessages, Usage: totalUsage}
			if truncated {
				return result, ErrResponseTruncated
			}
			return result, nil
		}
	}

	return &StreamResult{Messages: allMessages, Usage: totalUsage}, ErrMaxStepsExceeded
}

// streamEvents iterates streaming events, firing callbacks and collecting
// tool calls. Returns the assembled messages (assistant response + optional
// tool results), usage, and whether the response was truncated.
// Assigns unique history IDs via IDGen on first touch of each content block,
// passes them to callbacks, and stores them on ContentParts.
//
//nolint:gocyclo // extracting further would harm readability
func (a *Agent) streamEvents(ctx context.Context, events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) ([]Message, Usage, bool, error) {
	var (
		stepMessage Message
		stepUsage   Usage
		truncated   bool
		deferred    []*ToolUsePart
		results     []ContentPart
	)

	// Channel for collecting all tool execution results.
	// Buffered so sender goroutines exit immediately after execution.
	// Capacity 16 covers all no-confirm + deferred results in practice.
	// Errors/denials are sent synchronously before the collection loop,
	// so the buffer must be large enough for all of them.
	// Unbuffered would also work (execution already done by time of send).
	resultCh := make(chan ContentPart, 16)
	execCount := 0

	// Track history IDs for content blocks (keyed by index for AT/AR/AF).
	idByIndex := make(map[int]uint64)

	for event, err := range events {
		if err != nil {
			return nil, Usage{}, false, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ReasoningDeltaEvent:
			if callbacks.OnReasoningDelta != nil {
				if err := callbacks.OnReasoningDelta(e.Delta, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolUseStartEvent:
			if callbacks.OnToolUseStart != nil {
				if err := callbacks.OnToolUseStart(e.ID, e.ToolName, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolUseCompleteEvent:
			id := getOrAssignID(callbacks, idByIndex, e.Index)
			if callbacks.OnToolUseInput != nil {
				if err := callbacks.OnToolUseInput(e.ID, e.Input, id); err != nil {
					return nil, Usage{}, false, err
				}
			}
			execCount++
			tc := toolUseEventToPart(e, id)
			deferred = a.handleStreamedToolUse(ctx, tc, callbacks, deferred, resultCh)

		case StepCompleteEvent:
			stepMessage = e.Message
			stepUsage = e.Usage
			// Set IDs on final content parts from tracked values.
			for i := range stepMessage.Content {
				if id, ok := idByIndex[i]; ok {
					stepMessage.Content[i].UpdateContentPartMeta(id, RoleAssistant)
				}
			}
			// Strip empty placeholders that providers may have inserted
			// to keep delta indices aligned with content positions.
			stepMessage.Content = stripEmptyPlaceholders(stepMessage.Content)
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				truncated = true
			}
		}
	}

	a.executeDeferredTools(ctx, deferred, callbacks, resultCh)

	for i := 0; i < execCount; i++ {
		results = append(results, <-resultCh)
	}

	// Re-order results by tool call ID to match the LLM's intended order.
	// toolUses are extracted from stepMessage.Content, which preserves the
	// SSE index order (0, 1, 2...) from the streaming response. Each result
	// carries its tool call ID, so we place them at the correct position
	// regardless of execution or collection order.
	toolUses := extractToolUses(stepMessage.Content)
	finalResults := make([]ContentPart, len(toolUses))
	idToTool := make(map[string]int, len(toolUses))
	for i, tc := range toolUses {
		idToTool[tc.ID] = i
	}
	for _, r := range results {
		if tr, ok := r.(*ToolResultPart); ok {
			if idx, ok := idToTool[tr.ID]; ok {
				finalResults[idx] = r
			}
		}
	}

	stepMessages := []Message{stepMessage}
	if len(finalResults) > 0 && !truncated {
		stepMessages = append(stepMessages, Message{Role: RoleTool, Content: finalResults})
	}

	return stepMessages, stepUsage, truncated, nil
}

// handleStreamedToolUse processes a completed tool use during streaming.
// If no confirmation is needed, the tool executes immediately in a goroutine
// and sends the result through resultCh. Otherwise, it's deferred.
func (a *Agent) handleStreamedToolUse(ctx context.Context, tc *ToolUsePart, callbacks StreamCallbacks, deferred []*ToolUsePart, resultCh chan<- ContentPart) []*ToolUsePart {
	if callbacks.OnToolConfirm == nil {
		historyID := uint64(0)
		if callbacks.IDGen != nil {
			historyID = callbacks.IDGen()
		}
		go func(tc *ToolUsePart, historyID uint64) {
			resultCh <- a.executeTool(ctx, tc, callbacks, historyID)
		}(tc, historyID)
	} else {
		deferred = append(deferred, tc)
	}
	return deferred
}

// executeDeferredTools sends deferred tools for confirmation and executes
// confirmed tools concurrently as confirm responses arrive.
// Results (errors, denials, and successful executions) are all sent through resultCh.
// Exactly len(deferred) items are sent to the channel.
func (a *Agent) executeDeferredTools(ctx context.Context, deferred []*ToolUsePart, callbacks StreamCallbacks, resultCh chan<- ContentPart) {
	if len(deferred) == 0 {
		return
	}

	requests := make([]ToolConfirmRequest, len(deferred))
	idToIdx := make(map[string]int, len(deferred))
	for i, tc := range deferred {
		requests[i] = ToolConfirmRequest{
			ID:       tc.ID,
			ToolName: tc.ToolName,
			Input:    tc.Input,
		}
		idToIdx[tc.ID] = i
	}

	confirmCh := callbacks.OnToolConfirm(requests)

	// Process confirm results as they arrive. Each response produces
	// exactly one item sent to resultCh (error, denial, or execution result).
	pendingConfirm := len(deferred)

	for pendingConfirm > 0 {
		resp := <-confirmCh
		pendingConfirm--

		if resp.Error != "" {
			historyID := uint64(0)
			if callbacks.IDGen != nil {
				historyID = callbacks.IDGen()
			}
			resultCh <- newToolResult(callbacks, resp.ID, nil, fmt.Errorf("denied: %s", resp.Error), historyID)
			continue
		}
		if !resp.Allowed {
			historyID := uint64(0)
			if callbacks.IDGen != nil {
				historyID = callbacks.IDGen()
			}
			resultCh <- newToolResult(callbacks, resp.ID, nil, fmt.Errorf("Tool execution denied by user"), historyID)
			continue
		}

		// Confirmed — execute concurrently.
		tc := deferred[idToIdx[resp.ID]]
		historyID := uint64(0)
		if callbacks.IDGen != nil {
			historyID = callbacks.IDGen()
		}
		go func(tc *ToolUsePart, historyID uint64) {
			resultCh <- a.executeTool(ctx, tc, callbacks, historyID)
		}(tc, historyID)
	}
}

// toolDefinitions returns the tool definitions from the agent config.
func (a *Agent) toolDefinitions() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.config.Tools))
	for i, tool := range a.config.Tools {
		defs[i] = tool.Definition
	}
	return defs
}

// getOrAssignID returns the history ID for the given content block index.
// If no ID has been assigned yet and IDGen is available, it generates one.
func getOrAssignID(callbacks StreamCallbacks, idByIndex map[int]uint64, index int) uint64 {
	if id, ok := idByIndex[index]; ok && id != 0 {
		return id
	}
	if callbacks.IDGen != nil {
		id := callbacks.IDGen()
		idByIndex[index] = id
		return id
	}
	return 0
}

// stripEmptyPlaceholders removes empty ReasoningPart and TextPart placeholders
// from the content array. OpenAI emits these slots at fixed indices (0 and 1)
// to keep delta indices aligned with content positions, even when absent.
func stripEmptyPlaceholders(content []ContentPart) []ContentPart {
	filtered := make([]ContentPart, 0, len(content))
	for _, part := range content {
		switch p := part.(type) {
		case *ReasoningPart:
			if p.Text != "" {
				filtered = append(filtered, part)
			}
		case *TextPart:
			if p.Text != "" {
				filtered = append(filtered, part)
			}
		default:
			filtered = append(filtered, part)
		}
	}
	return filtered
}

// toolUseEventToPart converts a ToolUseCompleteEvent to a ToolUsePart,
// carrying over the history ID assigned during streaming.
func toolUseEventToPart(e ToolUseCompleteEvent, historyID uint64) *ToolUsePart {
	return &ToolUsePart{
		ID:        e.ID,
		ToolName:  e.ToolName,
		Input:     e.Input,
		HistoryID: historyID,
	}
}

// executeTool executes a single tool call and returns the result.
func (a *Agent) executeTool(ctx context.Context, tc *ToolUsePart, callbacks StreamCallbacks, historyID uint64) ContentPart {
	var tool *Tool
	for _, t := range a.config.Tools {
		if t.Definition.Name == tc.ToolName {
			tool = &t
			break
		}
	}

	if tool == nil {
		return newToolResult(callbacks, tc.ID, nil, fmt.Errorf("unknown tool: %s", tc.ToolName), historyID)
	}

	content, err := tool.Execute(ctx, tc.Input)
	return newToolResult(callbacks, tc.ID, content, err, historyID)
}

// newToolResult creates a ToolResultPart and fires the OnToolUseOutput callback
// so the UI is notified immediately as each tool finishes.
//
// Note: content is processed (nil → empty, error → TextPart) BEFORE the
// callback fires, so the callback always receives meaningful display text.
func newToolResult(callbacks StreamCallbacks, id string, content []ContentPart, err error, historyID uint64) *ToolResultPart {
	if content == nil {
		content = []ContentPart{}
	}
	isError := err != nil
	if isError && len(content) == 0 {
		content = []ContentPart{&TextPart{Text: err.Error()}}
	}
	if callbacks.OnToolUseOutput != nil {
		callbacks.OnToolUseOutput(id, content, err, historyID) //nolint:errcheck
	}
	return &ToolResultPart{ID: id, Content: content, IsError: isError, HistoryID: historyID, Role: RoleTool}
}

// extractToolUses extracts ToolUseParts from message content.
func extractToolUses(content []ContentPart) []ToolUsePart {
	var uses []ToolUsePart
	for _, part := range content {
		if tc, ok := part.(*ToolUsePart); ok {
			uses = append(uses, *tc)
		}
	}
	return uses
}
