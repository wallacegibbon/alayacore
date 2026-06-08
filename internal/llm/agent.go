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
	Execute    func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error)
}

// deferredTool represents a tool call that arrived during streaming
// and needs confirmation before execution.
type deferredTool struct {
	index int
	tc    ToolUsePart
}

// execResult carries a tool execution result tagged with its index
// so the receiver can place it in the correct order.
type execResult struct {
	index   int
	content ContentPart
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
	OnTextDelta      func(delta string, index int) error
	OnReasoningDelta func(delta string, index int) error
	OnToolUseStart   func(id, toolName string) error
	OnToolUseInput   func(id string, input json.RawMessage) error
	OnToolConfirm    func(requests []ToolConfirmRequest) <-chan ToolConfirmResponse
	OnToolUseOutput  func(id string, output ToolResultOutput) error
	OnStepStart      func(step int) error
	OnStepFinish     func(messages []Message, usage Usage) error
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
		var (
			stepUsage           Usage
			taskDone, truncated bool
			err                 error
		)
		allMessages, stepUsage, taskDone, truncated, err = a.executeStep(ctx, step, allMessages, callbacks)
		if err != nil {
			return nil, err
		}

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

	events, err := a.config.Provider.StreamMessages(
		ctx,
		allMessages,
		a.toolDefinitions(),
		a.config.SystemPrompt,
		a.config.ExtraSystemPrompt,
	)
	if err != nil {
		return nil, Usage{}, false, false, fmt.Errorf("provider stream failed: %w", err)
	}

	stepMessage, stepUsage, truncated, deferred, results, err := a.streamEvents(ctx, events, callbacks)
	if err != nil {
		return nil, Usage{}, false, false, err
	}

	// Handle deferred tools (those needing confirmation).
	a.executeDeferredTools(ctx, deferred, callbacks, &results, truncated)

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
		if tr, ok := r.(ToolResultPart); ok {
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
// tool calls. Tools needing confirmation are deferred; others execute immediately.
//
//nolint:gocyclo // 16 is borderline; extracting further would harm readability.
func (a *Agent) streamEvents(ctx context.Context, events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) (Message, Usage, bool, []deferredTool, []ContentPart, error) {
	var (
		stepMessage Message
		stepUsage   Usage
		truncated   bool
		deferred    []deferredTool
		results     []ContentPart
	)

	// Channel for collecting tool execution results during streaming.
	// Each result is tagged with its index so the receiver can order them.
	resultCh := make(chan execResult)
	toolCount := 0

	for event, err := range events {
		if err != nil {
			return Message{}, Usage{}, false, nil, nil, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta, e.Index); err != nil {
					return Message{}, Usage{}, false, nil, nil, err
				}
			}

		case ReasoningDeltaEvent:
			if err := fireOnReasoningDelta(callbacks, e); err != nil {
				return Message{}, Usage{}, false, nil, nil, err
			}

		case ToolUseStartEvent:
			if err := a.fireOnToolUseStart(callbacks, e); err != nil {
				return Message{}, Usage{}, false, nil, nil, err
			}

		case ToolUsePart:
			if err := a.fireOnToolUseInput(callbacks, e); err != nil {
				return Message{}, Usage{}, false, nil, nil, err
			}
			deferred, results, toolCount = a.handleStreamedToolUse(ctx, e, callbacks, deferred, results, resultCh, toolCount)

		case StepCompleteEvent:
			stepMessage = e.Message
			stepUsage = e.Usage
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				truncated = true
			}
		}
	}

	// Wait for all no-confirm executions and collect results.
	if toolCount > 0 {
		for i := 0; i < toolCount; i++ {
			r := <-resultCh
			results[r.index] = r.content
		}
	}

	return stepMessage, stepUsage, truncated, deferred, results, nil
}

// handleStreamedToolUse processes a completed tool use during streaming.
// If no confirmation is needed, the tool executes immediately in a goroutine
// and sends the result through resultCh. Otherwise, it's deferred.
func (a *Agent) handleStreamedToolUse(ctx context.Context, tc ToolUsePart, callbacks StreamCallbacks, deferred []deferredTool, results []ContentPart, resultCh chan<- execResult, toolCount int) ([]deferredTool, []ContentPart, int) {
	if callbacks.OnToolConfirm == nil {
		i := len(results)
		results = append(results, ToolResultPart{})
		toolCount++
		go func(i int, tc ToolUsePart) {
			resultCh <- execResult{i, a.executeTool(ctx, tc, callbacks)}
		}(i, tc)
	} else {
		deferred = append(deferred, deferredTool{index: len(deferred), tc: tc})
	}
	return deferred, results, toolCount
}

// executeDeferredTools sends deferred tools for confirmation and executes
// confirmed tools concurrently as confirm responses arrive.
// It waits for all deferred executions to finish before returning.
func (a *Agent) executeDeferredTools(ctx context.Context, deferred []deferredTool, callbacks StreamCallbacks, results *[]ContentPart, truncated bool) {
	if len(deferred) == 0 || truncated {
		return
	}

	requests := make([]ToolConfirmRequest, len(deferred))
	idToIdx := make(map[string]int, len(deferred))
	for i, d := range deferred {
		requests[i] = ToolConfirmRequest{ID: d.tc.ID, ToolName: d.tc.ToolName, Input: d.tc.Input}
		idToIdx[d.tc.ID] = i
	}

	confirmCh := callbacks.OnToolConfirm(requests)

	// Process confirm results as they arrive. Confirmed tools execute
	// concurrently via goroutines and report results through a channel.
	pendingConfirm := len(deferred)
	execCh := make(chan ContentPart, len(deferred))
	execCount := 0

	for pendingConfirm > 0 {
		resp := <-confirmCh
		pendingConfirm--

		if resp.Error != "" {
			failed := ToolResultOutputFailed{Reason: resp.Error}
			if callbacks.OnToolUseOutput != nil {
				callbacks.OnToolUseOutput(resp.ID, failed) //nolint:errcheck
			}
			*results = append(*results, ToolResultPart{ID: resp.ID, Output: failed})
			continue
		}
		if !resp.Allowed {
			denied := ToolResultOutputFailed{Reason: "Tool execution denied by user"}
			if callbacks.OnToolUseOutput != nil {
				callbacks.OnToolUseOutput(resp.ID, denied) //nolint:errcheck
			}
			*results = append(*results, ToolResultPart{ID: resp.ID, Output: denied})
			continue
		}

		// Confirmed — execute concurrently.
		d := deferred[idToIdx[resp.ID]]
		execCount++
		go func(tc ToolUsePart) {
			execCh <- a.executeTool(ctx, tc, callbacks)
		}(d.tc)
	}

	// Collect results from all executed goroutines.
	for i := 0; i < execCount; i++ {
		*results = append(*results, <-execCh)
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

// fireOnToolUseStart invokes the OnToolUseStart callback if set.
func (a *Agent) fireOnToolUseStart(callbacks StreamCallbacks, e ToolUseStartEvent) error {
	if callbacks.OnToolUseStart != nil {
		if err := callbacks.OnToolUseStart(e.ID, e.ToolName); err != nil {
			return fmt.Errorf("OnToolUseStart callback failed: %w", err)
		}
	}
	return nil
}

// fireOnReasoningDelta invokes the OnReasoningDelta callback if set.
func fireOnReasoningDelta(callbacks StreamCallbacks, e ReasoningDeltaEvent) error {
	if callbacks.OnReasoningDelta != nil {
		if err := callbacks.OnReasoningDelta(e.Delta, e.Index); err != nil {
			return fmt.Errorf("OnReasoningDelta callback failed: %w", err)
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

// fireOnToolUseInput invokes the OnToolUseInput callback if set.
func (a *Agent) fireOnToolUseInput(callbacks StreamCallbacks, e ToolUsePart) error {
	if callbacks.OnToolUseInput != nil {
		if err := callbacks.OnToolUseInput(e.ID, e.Input); err != nil {
			return fmt.Errorf("OnToolUseInput callback failed: %w", err)
		}
	}
	return nil
}

// executeTool executes a single tool call and returns the result.
func (a *Agent) executeTool(ctx context.Context, tc ToolUsePart, callbacks StreamCallbacks) ContentPart {
	var tool *Tool
	for _, t := range a.config.Tools {
		if t.Definition.Name == tc.ToolName {
			tool = &t
			break
		}
	}

	if tool == nil {
		return ToolResultPart{
			ID: tc.ID,
			Output: ToolResultOutputFailed{
				Reason: fmt.Sprintf("unknown tool: %s", tc.ToolName),
			},
		}
	}

	output, err := tool.Execute(ctx, tc.Input)
	if err != nil {
		output = ToolResultOutputFailed{
			Reason: err.Error(),
		}
	}

	if callbacks.OnToolUseOutput != nil {
		//nolint:errcheck // callback error shouldn't prevent tool result from being recorded
		callbacks.OnToolUseOutput(tc.ID, output)
	}

	return ToolResultPart{
		ID:     tc.ID,
		Output: output,
	}
}

// executeTools executes all tool calls after user confirmation and returns the results.
// If OnToolConfirm is set, all tools are sent for confirmation. The adapter returns a
// channel and sends responses asynchronously — as each tool is confirmed, it executes
// immediately (concurrently with other confirms and executions).
// Results are collected in the same order as toolUses.
// extractToolUses extracts ToolUseParts from message content.
func extractToolUses(content []ContentPart) []ToolUsePart {
	var uses []ToolUsePart
	for _, part := range content {
		if tc, ok := part.(ToolUsePart); ok {
			uses = append(uses, tc)
		}
	}
	return uses
}
