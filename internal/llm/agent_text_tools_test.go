package llm

import (
	"context"
	"encoding/json"
	"testing"
)

// TestAgentPreservesTextWithToolCalls verifies that the agent preserves
// text content when tool calls are present in the assistant message
func TestAgentPreservesTextWithToolCalls(t *testing.T) {
	// Create mock provider that returns text + tool call
	provider := &mockProviderWithTextAndTools{
		responses: []mockResponse{
			{
				text:      "Let me check that for you.",
				toolCalls: []ToolCallPart{{Type: "tool_use", ToolCallID: "call_123", ToolName: "get_weather", Input: []byte(`{"location":"SF"}`)}},
			},
			{
				text: "The weather in SF is sunny.",
			},
		},
	}

	// Create agent
	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "get_weather", Description: "Get weather", Schema: []byte(`{"type":"object"}`)},
				Execute: func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error) {
					return ToolResultOutputText{Type: "text", Text: "Sunny, 72F"}, nil
				},
			},
		},
		MaxSteps: 5,
	})

	// Track messages received via OnStepFinish callback
	var allStepMessages [][]Message
	_, err := agent.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: []ContentPart{TextPart{Type: "text", Text: "What's the weather?"}}},
	}, StreamCallbacks{
		OnStepFinish: func(messages []Message, usage Usage) error {
			allStepMessages = append(allStepMessages, messages)
			return nil
		},
	})

	if err != nil {
		t.Fatalf("Agent.Stream failed: %v", err)
	}

	// Verify first step has both text and tool call
	if len(allStepMessages) < 1 {
		t.Fatal("No step messages received")
	}

	firstStep := allStepMessages[0]
	// First step should have assistant message (with text + tool call) and tool result message
	if len(firstStep) != 2 {
		t.Fatalf("Expected 2 messages in first step (assistant + tool result), got %d", len(firstStep))
	}

	assistantMsg := firstStep[0]
	if assistantMsg.Role != RoleAssistant {
		t.Fatalf("Expected assistant message, got %s", assistantMsg.Role)
	}

	// Check that assistant message has BOTH text and tool call
	hasText := false
	hasToolCall := false
	for _, part := range assistantMsg.Content {
		switch p := part.(type) {
		case TextPart:
			hasText = true
			if p.Text != "Let me check that for you." {
				t.Errorf("Text content mismatch: %q", p.Text)
			}
		case ToolCallPart:
			hasToolCall = true
			if p.ToolName != "get_weather" {
				t.Errorf("Tool name mismatch: %s", p.ToolName)
			}
		}
	}

	if !hasText {
		t.Error("CRITICAL BUG: Assistant message missing text content! Text lost when tool calls present")
	}

	if !hasToolCall {
		t.Error("Assistant message missing tool call")
	}

	if hasText && hasToolCall {
		t.Log("PASS: Agent preserves text content when tool calls are present")
	}
}

// mockProviderWithTextAndTools returns responses with both text and tool calls
type mockProviderWithTextAndTools struct {
	responses []mockResponse
	callCount int
}

type mockResponse struct {
	text      string
	toolCalls []ToolCallPart
}

func (m *mockProviderWithTextAndTools) StreamMessages(ctx context.Context, messages []Message, tools []ToolDefinition, systemPrompt, extraSystemPrompt string) (<-chan StreamEvent, error) {
	eventChan := make(chan StreamEvent)

	go func() {
		defer close(eventChan)

		resp := m.responses[m.callCount]
		m.callCount++

		// Send text delta
		if resp.text != "" {
			eventChan <- TextDeltaEvent{Delta: resp.text}
		}

		// Send tool call events
		for _, tc := range resp.toolCalls {
			eventChan <- ToolCallEvent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Input:      tc.Input,
			}
		}

		// Send step complete with message containing BOTH text and tool calls
		content := []ContentPart{}
		if resp.text != "" {
			content = append(content, TextPart{Type: "text", Text: resp.text})
		}
		for _, tc := range resp.toolCalls {
			content = append(content, tc)
		}

		eventChan <- StepCompleteEvent{
			Messages: []Message{
				{
					Role:    RoleAssistant,
					Content: content,
				},
			},
			Usage: Usage{InputTokens: 10, OutputTokens: 20},
		}
	}()

	return eventChan, nil
}
