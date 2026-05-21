package factory

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm/providers"
)

func TestFactoryAnthropicProvider(t *testing.T) {
	// Test that Anthropic provider is created correctly
	config := ProviderConfig{
		Type:   "anthropic",
		APIKey: "test-key",
		Model:  "claude-3-5-sonnet-20241022",
	}

	provider, err := NewProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	anthropicProvider, ok := provider.(*providers.AnthropicProvider)
	if !ok {
		t.Fatalf("Expected AnthropicProvider, got %T", provider)
	}

	if anthropicProvider == nil {
		t.Fatal("AnthropicProvider is nil")
	}
}

func TestFactoryOpenAIProvider(t *testing.T) {
	// Test that OpenAI provider is created correctly
	config := ProviderConfig{
		Type:   "openai",
		APIKey: "test-key",
		Model:  "gpt-4o",
	}

	provider, err := NewProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	_, ok := provider.(*providers.OpenAIProvider)
	if !ok {
		t.Fatalf("Expected OpenAIProvider, got %T", provider)
	}
}

func TestFactoryAnthropicWithAllOptions(t *testing.T) {
	// Test that all options work together
	config := ProviderConfig{
		Type:    "anthropic",
		APIKey:  "test-key",
		BaseURL: "https://custom.anthropic.com",
		Model:   "claude-3-opus-20240229",
	}

	provider, err := NewProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	_, ok := provider.(*providers.AnthropicProvider)
	if !ok {
		t.Fatalf("Expected AnthropicProvider, got %T", provider)
	}
}
