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

func (m *mockProviderAlwaysToolCalls) SetReasoningEnabled(_ bool) {}

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
