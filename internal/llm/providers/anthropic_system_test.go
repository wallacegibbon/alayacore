package providers

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestAnthropicSystemMessageArray(t *testing.T) {
	// Test that Anthropic API request structure supports system message array
	req := anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{},
		System: []anthropicSystemMessage{
			{Type: "text", Text: "Default system prompt"},
			{Type: "text", Text: "Extra system prompt 1\n\nExtra system prompt 2"},
		},
		MaxTokens: llm.DefaultMaxTokens,
		Stream:    true,
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	t.Logf("Anthropic request:\n%s", string(data))

	// Verify system is an array
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	system, ok := parsed["system"].([]any)
	if !ok {
		t.Fatal("Expected system to be an array")
	}

	if len(system) != 2 {
		t.Fatalf("Expected 2 system messages, got %d", len(system))
	}

	// Verify first message
	first, ok := system[0].(map[string]any)
	if !ok {
		t.Fatal("Expected system[0] to be a map")
	}
	if first["type"] != "text" {
		t.Errorf("Expected type 'text', got %v", first["type"])
	}
	if first["text"] != "Default system prompt" {
		t.Errorf("Expected 'Default system prompt', got %v", first["text"])
	}

	// Verify second message
	second, ok := system[1].(map[string]any)
	if !ok {
		t.Fatal("Expected system[1] to be a map")
	}
	if second["text"] != "Extra system prompt 1\n\nExtra system prompt 2" {
		t.Errorf("Expected merged extra prompts, got %v", second["text"])
	}
}

func TestAnthropicEmptyExtraPrompt(t *testing.T) {
	// Test that empty extra prompt results in only one system message
	req := anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{},
		System: []anthropicSystemMessage{
			{Type: "text", Text: "Default system prompt"},
		},
		MaxTokens: llm.DefaultMaxTokens,
		Stream:    true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	system, ok := parsed["system"].([]any)
	if !ok {
		t.Fatal("Expected system to be an array")
	}
	if len(system) != 1 {
		t.Errorf("Expected 1 system message when extra is empty, got %d", len(system))
	}
}

func TestAnthropicPromptCacheControl(t *testing.T) {
	// Test automatic caching: cache_control at top level of request
	req := anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{},
		System: []anthropicSystemMessage{
			{Type: "text", Text: "Default system prompt"},
			{Type: "text", Text: "Extra system prompt"},
		},
		MaxTokens:    4096,
		Stream:       true,
		CacheControl: &anthropicCacheControl{Type: "ephemeral"},
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	t.Logf("Anthropic request with automatic caching:\n%s", string(data))

	// Verify structure
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify top-level cache_control
	cacheControl, ok := parsed["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("Expected top-level cache_control field")
	}

	if cacheControl["type"] != "ephemeral" {
		t.Errorf("Expected cache_control type 'ephemeral', got %v", cacheControl["type"])
	}

	// Verify system messages do NOT have cache_control (automatic caching handles it)
	system, ok := parsed["system"].([]any)
	if !ok {
		t.Fatal("Expected system to be an array")
	}
	for i, msg := range system {
		m, ok := msg.(map[string]any)
		if !ok {
			t.Fatalf("Expected system[%d] to be a map", i)
		}
		if _, hasCache := m["cache_control"]; hasCache {
			t.Errorf("System message %d should NOT have cache_control in automatic caching mode", i)
		}
	}

	t.Logf("Verified: automatic caching with top-level cache_control")
}
