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
	OnTextDelta      func(delta string, streamID uint64) error
	OnReasoningDelta func(delta string, streamID uint64) error
	// OnToolUseStart fires when a tool name and ID are known (before args stream).
	OnToolUseStart func(toolCallID, toolName string, streamID uint64) error
	// OnToolUseInput fires when tool arguments finish streaming.
	OnToolUseInput  func(toolCallID string, input json.RawMessage, streamID uint64) error
	OnToolConfirm   func(requests []ToolConfirmRequest) <-chan ToolConfirmResponse
	OnToolUseOutput func(toolCallID string, content []ContentPart, err error, streamID uint64) error
	OnStepStart     func(step int) error
	OnStepFinish    func(messages []Message, usage Usage) error

	// IDGen provides unique stream IDs. Called once per content block
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

// Stream executes the agent with streaming callbacks
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
		updated, stepUsage, taskDone, truncated, err := a.executeStep(ctx, step, allMessages, callbacks)
		if err != nil {
			return nil, err
		}
		allMessages = updated

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens

		if taskDone {
			result := &StreamResult{Messages: allMessages, Usage: totalUsage}
			if truncated {
				return result, ErrResponseTruncated
			}
			return result, nil
		}
	}

	return &StreamResult{Messages: allMessages, Usage: totalUsage}, ErrMaxStepsExceeded
}

// executeStep runs one iteration of the agent loop.
// step is 1-indexed. Returns updated allMessages, stepUsage, whether the task is
// done, whether the response was truncated, and any fatal error.
//
// Tools are confirmed and executed as soon as their arguments finish streaming
// (on ToolUsePart event), overlapping with other tools still being streamed.
func (a *Agent) executeStep(ctx context.Context, step int, allMessages []Message, callbacks StreamCallbacks) ([]Message, Usage, bool, bool, error) {
	if err := a.invokeStepStart(callbacks, step); err != nil {
		return nil, Usage{}, false, false, err
	}

	events, err := a.config.Provider.StreamMessages(ctx, allMessages, a.toolDefinitions(), a.config.SystemPrompt, a.config.ExtraSystemPrompt)
	if err != nil {
		return nil, Usage{}, false, false, fmt.Errorf("provider stream failed: %w", err)
	}

	stepMessage, stepUsage, truncated, results, err := a.streamEvents(ctx, events, callbacks)
	if err != nil {
		return nil, Usage{}, false, false, err
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

	allMessages = append(allMessages, stepMessage)
	if len(finalResults) > 0 && !truncated {
		allMessages = append(allMessages, Message{Role: RoleTool, Content: finalResults})
	}

	if err := fireOnStepFinish(callbacks, allMessages, stepUsage); err != nil {
		return nil, Usage{}, false, false, err
	}

	taskDone := truncated || len(toolUses) == 0
	return allMessages, stepUsage, taskDone, truncated, nil
}

// streamEvents iterates streaming events, firing callbacks and collecting
// tool calls. Assigns unique stream IDs via IDGen on first touch of each
// content block, passes them to callbacks, and stores them on ContentParts.
//
//nolint:gocyclo // 16 is borderline; extracting further would harm readability.
func (a *Agent) streamEvents(ctx context.Context, events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) (Message, Usage, bool, []ContentPart, error) {
	var (
		stepMessage Message
		stepUsage   Usage
		truncated   bool
		deferred    []ToolUsePart
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

	// Track stream IDs for content blocks (keyed by index for AT/AR/AF).
	idByIndex := make(map[int]uint64)

	for event, err := range events {
		if err != nil {
			return Message{}, Usage{}, false, nil, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			id := idByIndex[e.Index]
			if id == 0 && callbacks.IDGen != nil {
				id = callbacks.IDGen()
				idByIndex[e.Index] = id
			}
			if err := fireOnTextDelta(callbacks, e, id); err != nil {
				return Message{}, Usage{}, false, nil, err
			}

		case ReasoningDeltaEvent:
			id := idByIndex[e.Index]
			if id == 0 && callbacks.IDGen != nil {
				id = callbacks.IDGen()
				idByIndex[e.Index] = id
			}
			if err := fireOnReasoningDelta(callbacks, e, id); err != nil {
				return Message{}, Usage{}, false, nil, err
			}

		case ToolUseStartEvent:
			id := idByIndex[e.Index]
			if id == 0 && callbacks.IDGen != nil {
				id = callbacks.IDGen()
				idByIndex[e.Index] = id
			}
			if err := a.fireOnToolUseStart(callbacks, e, id); err != nil {
				return Message{}, Usage{}, false, nil, err
			}

		case ToolUseDeltaEvent:
			id := idByIndex[e.Index]
			if err := a.fireOnToolUseInput(callbacks, e, id); err != nil {
				return Message{}, Usage{}, false, nil, err
			}
			execCount++
			tc := &ToolUsePart{
				ID:        e.ID,
				ToolName:  e.ToolName,
				Input:     e.Input,
				HistoryID: id,
			}
			deferred = a.handleStreamedToolUse(ctx, *tc, callbacks, deferred, resultCh)

		case StepCompleteEvent:
			stepMessage = e.Message
			stepUsage = e.Usage
			// Set IDs on final content parts from tracked values.
			for i, part := range stepMessage.Content {
				if id, ok := idByIndex[i]; ok {
					stepMessage.Content[i].UpdateContentPartMeta(id, RoleAssistant)
				}
				_ = part // suppress unused
			}
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				truncated = true
			}
		}
	}

	a.executeDeferredTools(ctx, deferred, callbacks, resultCh)

	for i := 0; i < execCount; i++ {
		results = append(results, <-resultCh)
	}

	return stepMessage, stepUsage, truncated, results, nil
}

// handleStreamedToolUse processes a completed tool use during streaming.
// If no confirmation is needed, the tool executes immediately in a goroutine
// and sends the result through resultCh. Otherwise, it's deferred.
func (a *Agent) handleStreamedToolUse(ctx context.Context, tc ToolUsePart, callbacks StreamCallbacks, deferred []ToolUsePart, resultCh chan<- ContentPart) []ToolUsePart {
	if callbacks.OnToolConfirm == nil {
		streamID := uint64(0)
		if callbacks.IDGen != nil {
			streamID = callbacks.IDGen()
		}
		go func(tc ToolUsePart, streamID uint64) {
			resultCh <- a.executeTool(ctx, tc, callbacks, streamID)
		}(tc, streamID)
	} else {
		deferred = append(deferred, tc)
	}
	return deferred
}

// executeDeferredTools sends deferred tools for confirmation and executes
// confirmed tools concurrently as confirm responses arrive.
// Results (errors, denials, and successful executions) are all sent through resultCh.
// Exactly len(deferred) items are sent to the channel.
func (a *Agent) executeDeferredTools(ctx context.Context, deferred []ToolUsePart, callbacks StreamCallbacks, resultCh chan<- ContentPart) {
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
			streamID := uint64(0)
			if callbacks.IDGen != nil {
				streamID = callbacks.IDGen()
			}
			resultCh <- newToolResult(callbacks, resp.ID, nil, fmt.Errorf("denied: %s", resp.Error), streamID)
			continue
		}
		if !resp.Allowed {
			streamID := uint64(0)
			if callbacks.IDGen != nil {
				streamID = callbacks.IDGen()
			}
			resultCh <- newToolResult(callbacks, resp.ID, nil, fmt.Errorf("Tool execution denied by user"), streamID)
			continue
		}

		// Confirmed — execute concurrently.
		tc := deferred[idToIdx[resp.ID]]
		streamID := uint64(0)
		if callbacks.IDGen != nil {
			streamID = callbacks.IDGen()
		}
		go func(tc ToolUsePart, streamID uint64) {
			resultCh <- a.executeTool(ctx, tc, callbacks, streamID)
		}(tc, streamID)
	}
}

// invokeStepStart fires the OnStepStart callback if set.
func (a *Agent) invokeStepStart(callbacks StreamCallbacks, step int) error {
	if callbacks.OnStepStart != nil {
		if err := callbacks.OnStepStart(step); err != nil {
			return fmt.Errorf("OnStepStart callback failed: %w", err)
		}
	}
	return nil
}

// toolDefinitions returns the tool definitions from the agent config.
func (a *Agent) toolDefinitions() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.config.Tools))
	for i, tool := range a.config.Tools {
		defs[i] = tool.Definition
	}
	return defs
}

// fireOnReasoningDelta invokes the OnReasoningDelta callback if set.
func fireOnReasoningDelta(callbacks StreamCallbacks, e ReasoningDeltaEvent, streamID uint64) error {
	if callbacks.OnReasoningDelta != nil {
		if err := callbacks.OnReasoningDelta(e.Delta, streamID); err != nil {
			return fmt.Errorf("OnReasoningDelta callback failed: %w", err)
		}
	}
	return nil
}

// fireOnTextDelta invokes the OnTextDelta callback if set.
func fireOnTextDelta(callbacks StreamCallbacks, e TextDeltaEvent, streamID uint64) error {
	if callbacks.OnTextDelta != nil {
		if err := callbacks.OnTextDelta(e.Delta, streamID); err != nil {
			return fmt.Errorf("OnTextDelta callback failed: %w", err)
		}
	}
	return nil
}

// fireOnToolUseStart invokes the OnToolUseStart callback if set.
func (a *Agent) fireOnToolUseStart(callbacks StreamCallbacks, e ToolUseStartEvent, streamID uint64) error {
	if callbacks.OnToolUseStart != nil {
		if err := callbacks.OnToolUseStart(e.ID, e.ToolName, streamID); err != nil {
			return fmt.Errorf("OnToolUseStart callback failed: %w", err)
		}
	}
	return nil
}

// fireOnToolUseInput invokes the OnToolUseInput callback if set.
func (a *Agent) fireOnToolUseInput(callbacks StreamCallbacks, e ToolUseDeltaEvent, streamID uint64) error {
	if callbacks.OnToolUseInput != nil {
		if err := callbacks.OnToolUseInput(e.ID, e.Input, streamID); err != nil {
			return fmt.Errorf("OnToolUseInput callback failed: %w", err)
		}
	}
	return nil
}

// fireOnStepFinish invokes the OnStepFinish callback if set.
func fireOnStepFinish(callbacks StreamCallbacks, messages []Message, usage Usage) error {
	if callbacks.OnStepFinish != nil {
		if err := callbacks.OnStepFinish(messages, usage); err != nil {
			return fmt.Errorf("OnStepFinish callback failed: %w", err)
		}
	}
	return nil
}

// executeTool executes a single tool call and returns the result.
func (a *Agent) executeTool(ctx context.Context, tc ToolUsePart, callbacks StreamCallbacks, streamID uint64) ContentPart {
	var tool *Tool
	for _, t := range a.config.Tools {
		if t.Definition.Name == tc.ToolName {
			tool = &t
			break
		}
	}

	if tool == nil {
		return newToolResult(callbacks, tc.ID, nil, fmt.Errorf("unknown tool: %s", tc.ToolName), streamID)
	}

	content, err := tool.Execute(ctx, tc.Input)
	return newToolResult(callbacks, tc.ID, content, err, streamID)
}

// newToolResult creates a ToolResultPart and fires the OnToolUseOutput callback
// so the UI is notified immediately as each tool finishes.
//
// Note: content is processed (nil → empty, error → TextPart) BEFORE the
// callback fires, so the callback always receives meaningful display text.
func newToolResult(callbacks StreamCallbacks, id string, content []ContentPart, err error, streamID uint64) *ToolResultPart {
	if content == nil {
		content = []ContentPart{}
	}
	isError := err != nil
	if isError && len(content) == 0 {
		content = []ContentPart{&TextPart{Text: err.Error()}}
	}
	if callbacks.OnToolUseOutput != nil {
		callbacks.OnToolUseOutput(id, content, err, streamID) //nolint:errcheck
	}
	return &ToolResultPart{ID: id, Content: content, IsError: isError, HistoryID: streamID, Role: RoleTool}
}

// extractToolUses extracts ToolUseParts from message content.// Results are collected in the same order as toolUses.
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
