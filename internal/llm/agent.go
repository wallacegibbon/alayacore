package llm

// Agent Tool-Calling Gotchas:
//
// 1. TOOL RESULT MESSAGE ORDERING: OnStepFinish callback receives complete step messages.
//    For tool-using steps, this includes both the assistant message (with tool calls) AND
//    the tool result message. The OnToolResult callback should only send UI notifications,
//    not append to session messages - the agent loop handles message assembly.
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
	OnToolResult     func(toolCallID string, output ToolResultOutput) error
	OnStepStart      func(step int) error
	OnStepFinish     func(messages []Message, usage Usage) error
}

// StreamResult is the final result of streaming
type StreamResult struct {
	Messages []Message
	Usage    Usage
}

// Stream executes the agent with streaming callbacks
func (a *Agent) Stream(ctx context.Context, messages []Message, callbacks StreamCallbacks) (*StreamResult, error) {
	var (
		allMessages = make([]Message, len(messages))
		totalUsage  Usage
		step        int
	)

	copy(allMessages, messages)

	// Normalize: 0 means unlimited, map to max int so the loop condition stays simple.
	maxSteps := a.config.MaxSteps
	if maxSteps == 0 {
		maxSteps = math.MaxInt
	}

	var truncErr error // non-nil when response hit output token limit

	for step = 1; step <= maxSteps; step++ {
		// Check for context cancellation between steps
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if err := a.invokeStepStart(callbacks, step); err != nil {
			return nil, err
		}

		// Stream from provider
		events, err := a.config.Provider.StreamMessages(
			ctx,
			allMessages,
			a.toolDefinitions(),
			a.config.SystemPrompt,
			a.config.ExtraSystemPrompt,
		)
		if err != nil {
			return nil, fmt.Errorf("provider stream failed: %w", err)
		}

		// Process events
		stepMessages, stepUsage, toolCalls, err := a.processStreamEvents(events, callbacks)
		if err != nil {
			if !errors.Is(err, ErrResponseTruncated) {
				return nil, err
			}
			truncErr = err
		}

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens

		// Truncation or no tool calls: finalize the step and stop
		if truncErr != nil || len(toolCalls) == 0 {
			if callbacks.OnStepFinish != nil {
				if cbErr := callbacks.OnStepFinish(stepMessages, stepUsage); cbErr != nil {
					return nil, fmt.Errorf("OnStepFinish callback failed: %w", cbErr)
				}
			}
			allMessages = append(allMessages, stepMessages...)
			break
		}

		// Execute tools and continue the loop
		allMessages, err = a.executeToolStep(ctx, stepMessages, toolCalls, stepUsage, callbacks, allMessages)
		if err != nil {
			return nil, err
		}
	}

	result := &StreamResult{Messages: allMessages, Usage: totalUsage}

	if truncErr != nil {
		return result, truncErr
	}

	// If the loop completed without a break (no final text-only response), we exceeded max steps.
	if step > maxSteps {
		return result, ErrMaxStepsExceeded
	}

	return result, nil
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

// executeToolStep runs tool calls, appends assistant + tool result messages,
// and fires the OnStepFinish callback.
func (a *Agent) executeToolStep(ctx context.Context, stepMessages []Message, toolCalls []ToolCallPart, stepUsage Usage, callbacks StreamCallbacks, allMessages []Message) ([]Message, error) {
	toolResults := a.executeTools(ctx, toolCalls, callbacks)
	toolResultMsg := Message{Role: RoleTool, Content: toolResults}

	if len(stepMessages) == 0 {
		stepMessages = []Message{{Role: RoleAssistant, Content: toolCallsToContent(toolCalls)}}
	}

	allMessages = append(allMessages, stepMessages...)
	allMessages = append(allMessages, toolResultMsg)

	if callbacks.OnStepFinish != nil {
		stepWithResults := make([]Message, len(stepMessages), len(stepMessages)+1)
		copy(stepWithResults, stepMessages)
		stepWithResults = append(stepWithResults, toolResultMsg)
		if err := callbacks.OnStepFinish(stepWithResults, stepUsage); err != nil {
			return nil, fmt.Errorf("OnStepFinish callback failed: %w", err)
		}
	}

	return allMessages, nil
}

// processStreamEvents handles streaming events from the provider
func (a *Agent) processStreamEvents(events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) ([]Message, Usage, []ToolCallPart, error) {
	var (
		stepMessages []Message
		stepUsage    Usage
		toolCalls    []ToolCallPart
	)

	for event, err := range events {
		if err != nil {
			return nil, Usage{}, nil, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta); err != nil {
					return nil, Usage{}, nil, fmt.Errorf("OnTextDelta callback failed: %w", err)
				}
			}

		case ReasoningDeltaEvent:
			if err := fireOnReasoningDelta(callbacks, e); err != nil {
				return nil, Usage{}, nil, err
			}

		case ToolCallStartEvent:
			if err := a.fireOnToolCallStart(callbacks, e); err != nil {
				return nil, Usage{}, nil, err
			}

		case ToolCallEvent:
			toolCalls = append(toolCalls, ToolCallPart{
				Type:       ContentPartToolUse,
				ToolCallID: e.ToolCallID,
				ToolName:   e.ToolName,
				Input:      e.Input,
			})
			if err := a.fireOnToolCall(callbacks, e); err != nil {
				return nil, Usage{}, nil, err
			}

		case StepCompleteEvent:
			stepMessages = e.Messages
			stepUsage = e.Usage
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				return stepMessages, stepUsage, nil, ErrResponseTruncated
			}
		}
	}

	return stepMessages, stepUsage, toolCalls, nil
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

// fireOnToolCall invokes the OnToolCall callback if set.
func (a *Agent) fireOnToolCall(callbacks StreamCallbacks, e ToolCallEvent) error {
	if callbacks.OnToolCall != nil {
		if err := callbacks.OnToolCall(e.ToolCallID, e.ToolName, e.Input); err != nil {
			return fmt.Errorf("OnToolCall callback failed: %w", err)
		}
	}
	return nil
}

// executeTools executes all tool calls and returns the results
func (a *Agent) executeTools(ctx context.Context, toolCalls []ToolCallPart, callbacks StreamCallbacks) []ContentPart {
	toolResults := make([]ContentPart, len(toolCalls))
	for i, tc := range toolCalls {
		// Find the tool
		var tool *Tool
		for _, t := range a.config.Tools {
			if t.Definition.Name == tc.ToolName {
				tool = &t
				break
			}
		}

		if tool == nil {
			toolResults[i] = ToolResultPart{
				Type:       ContentPartToolResult,
				ToolCallID: tc.ToolCallID,
				Output: ToolResultOutputError{
					Type:  "error",
					Error: fmt.Sprintf("unknown tool: %s", tc.ToolName),
				},
			}
			continue
		}

		// Execute tool
		output, err := tool.Execute(ctx, tc.Input)
		if err != nil {
			output = ToolResultOutputError{
				Type:  "error",
				Error: err.Error(),
			}
		}

		toolResults[i] = ToolResultPart{
			Type:       ContentPartToolResult,
			ToolCallID: tc.ToolCallID,
			Output:     output,
		}

		// Notify callback about tool result
		if callbacks.OnToolResult != nil {
			//nolint:errcheck // callback error shouldn't prevent tool result from being recorded
			callbacks.OnToolResult(tc.ToolCallID, output)
		}
	}
	return toolResults
}

// toolCallsToContent converts tool calls to content parts
func toolCallsToContent(toolCalls []ToolCallPart) []ContentPart {
	content := make([]ContentPart, len(toolCalls))
	for i, tc := range toolCalls {
		content[i] = tc
	}
	return content
}
