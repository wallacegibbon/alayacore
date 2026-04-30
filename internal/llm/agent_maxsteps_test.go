package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
)

// mockProviderAlwaysToolCalls always returns tool calls, never a final text response.
// This simulates an agent that never converges to a final answer.
type mockProviderAlwaysToolCalls struct {
	callCount int
}

func (m *mockProviderAlwaysToolCalls) StreamMessages(_ context.Context, _ []Message, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	m.callCount++
	return func(yield func(StreamEvent, error) bool) {
		// Always emit a tool call, never a text-only response
		yield(ToolCallEvent{
			ToolCallID: "call_1",
			ToolName:   "repeat",
			Input:      []byte(`{}`),
		}, nil)
		yield(StepCompleteEvent{
			Messages: []Message{
				{
					Role: RoleAssistant,
					Content: []ContentPart{
						ToolCallPart{
							Type:       ContentPartToolUse,
							ToolCallID: "call_1",
							ToolName:   "repeat",
							Input:      []byte(`{}`),
						},
					},
				},
			},
			Usage: Usage{InputTokens: 10, OutputTokens: 5},
		}, nil)
	}, nil
}

func (m *mockProviderAlwaysToolCalls) SetReasoningLevel(_ int) {}

func TestAgentMaxStepsExceeded(t *testing.T) {
	provider := &mockProviderAlwaysToolCalls{}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "repeat", Description: "Repeat", Schema: []byte(`{"type":"object"}`)},
				Execute: func(_ context.Context, _ json.RawMessage) (ToolResultOutput, error) {
					return ToolResultOutputText{Type: "text", Text: "repeated"}, nil
				},
			},
		},
		MaxSteps: 3,
	})

	result, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "go"}}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult even when max steps exceeded")
	}

	if provider.callCount != 3 {
		t.Fatalf("expected 3 provider calls (max steps), got %d", provider.callCount)
	}

	t.Logf("PASS: Agent correctly returns ErrMaxStepsExceeded after %d steps", provider.callCount)
}

func TestAgentCompletesWithinMaxSteps(t *testing.T) {
	// Provider returns tool call on first call, then text-only on second call
	provider := &mockProviderWithTextAndTools{
		responses: []mockResponse{
			{
				toolCalls: []ToolCallPart{{Type: "tool_use", ToolCallID: "call_1", ToolName: "ping", Input: []byte(`{}`)}},
			},
			{
				text: "Done!",
			},
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "ping", Description: "Ping", Schema: []byte(`{"type":"object"}`)},
				Execute: func(_ context.Context, _ json.RawMessage) (ToolResultOutput, error) {
					return ToolResultOutputText{Type: "text", Text: "pong"}, nil
				},
			},
		},
		MaxSteps: 3,
	})

	result, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "go"}}},
	}, StreamCallbacks{})

	if err != nil {
		t.Fatalf("expected no error when agent completes within max steps, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	t.Log("PASS: Agent completes normally within max steps without error")
}

// mockProviderTruncated returns a text response with a truncation stop reason.
type mockProviderTruncated struct {
	stopReason string
}

func (m *mockProviderTruncated) StreamMessages(_ context.Context, _ []Message, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "Partial response..."}, nil)
		yield(StepCompleteEvent{
			Messages: []Message{
				{
					Role:    RoleAssistant,
					Content: []ContentPart{TextPart{Type: "text", Text: "Partial response..."}},
				},
			},
			Usage:      Usage{InputTokens: 10, OutputTokens: 5},
			StopReason: m.stopReason,
		}, nil)
	}, nil
}

func (m *mockProviderTruncated) SetReasoningLevel(_ int) {}

func TestAgentTruncatedMaxTokens(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "max_tokens"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "Write a novel"}}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("expected ErrResponseTruncated, got: %v", err)
	}

	if result == nil || len(result.Messages) == 0 {
		t.Fatalf("expected non-nil StreamResult with partial messages, got: %+v", result)
	}

	t.Log("PASS: Agent returns ErrResponseTruncated for max_tokens with partial messages")
}

func TestAgentTruncatedLength(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "length"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "Write a novel"}}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("expected ErrResponseTruncated, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult even when truncated")
	}

	t.Log("PASS: Agent returns ErrResponseTruncated for length with partial messages")
}

func TestAgentNoTruncationOnEndTurn(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "end_turn"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "Hello"}}},
	}, StreamCallbacks{})

	if err != nil {
		t.Fatalf("expected no error for end_turn, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	t.Log("PASS: Agent does not return ErrResponseTruncated for end_turn")
}
