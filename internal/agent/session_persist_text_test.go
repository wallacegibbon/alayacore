package agent

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestSessionSavePreservesTextWithToolCalls verifies that text messages
// are preserved when saving/loading sessions with tool calls
func TestSessionSavePreservesTextWithToolCalls(t *testing.T) {
	// Create session data with an assistant message containing both text and tool calls
	data := &SessionData{
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					llm.TextPart{Type: "text", Text: "What's the weather?"},
				},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.TextPart{Type: "text", Text: "Let me check that for you."},
					llm.ToolCallPart{
						Type:       "tool_use",
						ToolCallID: "call_123",
						ToolName:   "get_weather",
						Input:      []byte(`{"location":"SF"}`),
					},
				},
			},
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{
					llm.ToolResultPart{
						Type:       "tool_result",
						ToolCallID: "call_123",
						Output:     llm.ToolResultOutputText{Type: "text", Text: "Sunny, 72F"},
					},
				},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.TextPart{Type: "text", Text: "The weather in SF is sunny and 72F."},
				},
			},
		},
	}

	// Format to markdown (TLV format)
	raw, err := formatSessionMarkdown(data)
	if err != nil {
		t.Fatalf("Failed to format session: %v", err)
	}

	t.Logf("Serialized session size: %d bytes", len(raw))

	// Parse back
	loaded, err := parseSessionMarkdown(raw)
	if err != nil {
		t.Fatalf("Failed to parse session: %v", err)
	}

	// Verify all messages are preserved
	if len(loaded.Messages) != len(data.Messages) {
		t.Fatalf("Message count mismatch: got %d, want %d", len(loaded.Messages), len(data.Messages))
	}

	// Check first assistant message (index 1) - should have BOTH text and tool call
	assistantMsg := loaded.Messages[1]
	if assistantMsg.Role != llm.RoleAssistant {
		t.Fatalf("Expected assistant message at index 1, got %s", assistantMsg.Role)
	}

	hasText := false
	hasToolCall := false
	for _, part := range assistantMsg.Content {
		switch p := part.(type) {
		case llm.TextPart:
			hasText = true
			if p.Text != "Let me check that for you." {
				t.Errorf("Assistant text mismatch: %q", p.Text)
			}
		case llm.ToolCallPart:
			hasToolCall = true
			if p.ToolName != "get_weather" {
				t.Errorf("Tool name mismatch: %s", p.ToolName)
			}
		}
	}

	if !hasText {
		t.Error("CRITICAL: Assistant message lost text content during save/load!")
	}

	if !hasToolCall {
		t.Error("Assistant message lost tool call during save/load!")
	}

	// Check second assistant message (index 3) - should have only text
	finalAssistantMsg := loaded.Messages[3]
	if finalAssistantMsg.Role != llm.RoleAssistant {
		t.Fatalf("Expected assistant message at index 3, got %s", finalAssistantMsg.Role)
	}

	if len(finalAssistantMsg.Content) != 1 {
		t.Errorf("Expected 1 content part in final assistant message, got %d", len(finalAssistantMsg.Content))
	}

	if textPart, ok := finalAssistantMsg.Content[0].(llm.TextPart); ok {
		if textPart.Text != "The weather in SF is sunny and 72F." {
			t.Errorf("Final assistant text mismatch: %q", textPart.Text)
		}
	} else {
		t.Error("Final assistant message content is not a TextPart")
	}

	if hasText && hasToolCall {
		t.Log("PASS: Text messages preserved during save/load with tool calls")
	}
}
