package llm

// Agent Tool-Calling Gotchas:
//
// 1. ONSTEPFINISH RECEIVES FULL HISTORY: OnStepFinish callback receives
//    the complete allMessages slice (full conversation history), not just
//    the current step's messages. The session layer replaces its state
//    from this rather than appending increments. OnToolResult should only
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
	OnTextDelta      func(delta string) error
	OnReasoningDelta func(delta string) error
	OnToolCallStart  func(toolCallID, toolName string) error
	OnToolCall       func(toolCallID, toolName string, input json.RawMessage) error
	OnToolConfirm    func(toolCallID, toolName string, input json.RawMessage) (bool, error)
	OnToolResult     func(toolCallID string, output ToolResultOutput) error
	OnStepStart      func(step int) error
	OnStepFinish     func(messages []Message, usage Usage) error
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

	stepMessage, stepUsage, err := a.processStreamEvents(events, callbacks)
	truncated := errors.Is(err, ErrResponseTruncated)
	if err != nil && !truncated {
		return nil, Usage{}, false, false, err
	}

	allMessages = append(allMessages, stepMessage)

	toolCalls := extractToolCalls(stepMessage.Content)
	if len(toolCalls) > 0 && !truncated {
		toolResults := a.executeTools(ctx, toolCalls, callbacks)
		allMessages = append(allMessages, Message{Role: RoleTool, Content: toolResults})
	}

	if err := fireOnStepFinish(callbacks, allMessages, stepUsage); err != nil {
		return nil, Usage{}, false, false, err
	}

	taskDone := truncated || len(toolCalls) == 0
	return allMessages, stepUsage, taskDone, truncated, nil
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

// processStreamEvents iterates streaming events from the provider and returns
// the assistant Message from StepCompleteEvent (all content parts accumulated)
// and token usage. Tool calls in Message.Content drive execution in executeStep.
// Returns ErrResponseTruncated if stop_reason is "max_tokens" or "length".
func (a *Agent) processStreamEvents(events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) (Message, Usage, error) {
	var (
		stepMessage Message
		stepUsage   Usage
	)

	for event, err := range events {
		if err != nil {
			return Message{}, Usage{}, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta); err != nil {
					return Message{}, Usage{}, fmt.Errorf("OnTextDelta callback failed: %w", err)
				}
			}

		case ReasoningDeltaEvent:
			if err := fireOnReasoningDelta(callbacks, e); err != nil {
				return Message{}, Usage{}, err
			}

		case ToolCallStartEvent:
			if err := a.fireOnToolCallStart(callbacks, e); err != nil {
				return Message{}, Usage{}, err
			}

		case ToolCallEvent:
			if err := a.fireOnToolCall(callbacks, e); err != nil {
				return Message{}, Usage{}, err
			}

		case StepCompleteEvent:
			stepMessage = e.Message
			stepUsage = e.Usage
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				return stepMessage, stepUsage, ErrResponseTruncated
			}
		}
	}

	return stepMessage, stepUsage, nil
}

// fireOnToolCallStart invokes the OnToolCallStart callback if set.
func (a *Agent) fireOnToolCallStart(callbacks StreamCallbacks, e ToolCallStartEvent) error {
	if callbacks.OnToolCallStart != nil {
		if err := callbacks.OnToolCallStart(e.ToolCallID, e.ToolName); err != nil {
			return fmt.Errorf("OnToolCallStart callback failed: %w", err)
		}
	}
	return nil
}

// fireOnReasoningDelta invokes the OnReasoningDelta callback if set.
func fireOnReasoningDelta(callbacks StreamCallbacks, e ReasoningDeltaEvent) error {
	if callbacks.OnReasoningDelta != nil {
		if err := callbacks.OnReasoningDelta(e.Delta); err != nil {
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

// fireOnToolCall invokes the OnToolCall callback if set.
func (a *Agent) fireOnToolCall(callbacks StreamCallbacks, e ToolCallEvent) error {
	if callbacks.OnToolCall != nil {
		if err := callbacks.OnToolCall(e.ToolCallID, e.ToolName, e.Input); err != nil {
			return fmt.Errorf("OnToolCall callback failed: %w", err)
		}
	}
	return nil
}

// executeTool executes a single tool call and returns the result.
func (a *Agent) executeTool(ctx context.Context, tc ToolCallPart, callbacks StreamCallbacks) ContentPart {
	var tool *Tool
	for _, t := range a.config.Tools {
		if t.Definition.Name == tc.ToolName {
			tool = &t
			break
		}
	}

	if tool == nil {
		return ToolResultPart{
			Type:       ContentPartToolResult,
			ToolCallID: tc.ToolCallID,
			Output: ToolResultOutputFailed{
				Type:   "error",
				Reason: fmt.Sprintf("unknown tool: %s", tc.ToolName),
			},
		}
	}

	output, err := tool.Execute(ctx, tc.Input)
	if err != nil {
		output = ToolResultOutputFailed{
			Type:   "error",
			Reason: err.Error(),
		}
	}

	if callbacks.OnToolResult != nil {
		//nolint:errcheck // callback error shouldn't prevent tool result from being recorded
		callbacks.OnToolResult(tc.ToolCallID, output)
	}

	return ToolResultPart{
		Type:       ContentPartToolResult,
		ToolCallID: tc.ToolCallID,
		Output:     output,
	}
}

// executeTools executes all tool calls after user confirmation and returns the results.
// Each tool call is first sent to OnToolConfirm (if set) for user approval.
// If confirmation is denied (returns false), a failed result is returned instead
// of executing the tool.
func (a *Agent) executeTools(ctx context.Context, toolCalls []ToolCallPart, callbacks StreamCallbacks) []ContentPart {
	results := make([]ContentPart, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if callbacks.OnToolConfirm != nil {
			allowed, err := callbacks.OnToolConfirm(tc.ToolCallID, tc.ToolName, tc.Input)
			if err != nil {
				failed := ToolResultOutputFailed{
					Type:   "error",
					Reason: err.Error(),
				}
				if callbacks.OnToolResult != nil {
					callbacks.OnToolResult(tc.ToolCallID, failed) //nolint:errcheck
				}
				results = append(results, ToolResultPart{
					Type:       ContentPartToolResult,
					ToolCallID: tc.ToolCallID,
					Output:     failed,
				})
				continue
			}
			if !allowed {
				denied := ToolResultOutputFailed{
					Type:   "error",
					Reason: "Tool execution denied by user",
				}
				if callbacks.OnToolResult != nil {
					callbacks.OnToolResult(tc.ToolCallID, denied) //nolint:errcheck
				}
				results = append(results, ToolResultPart{
					Type:       ContentPartToolResult,
					ToolCallID: tc.ToolCallID,
					Output:     denied,
				})
				continue
			}
		}
		results = append(results, a.executeTool(ctx, tc, callbacks))
	}
	return results
}

// extractToolCalls extracts ToolCallParts from message content.
func extractToolCalls(content []ContentPart) []ToolCallPart {
	var calls []ToolCallPart
	for _, part := range content {
		if tc, ok := part.(ToolCallPart); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}
