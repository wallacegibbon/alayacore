package config

import "strings"

// ModelConfig represents a model configuration.
// JSON tags are used for TLV serialization to adapters.
type ModelConfig struct {
	ID           int    `json:"id" config:"-"`                                  // Runtime ID (generated, not persisted)
	Name         string `json:"name" config:"name"`                             // Display name
	ProtocolType string `json:"protocol_type" config:"protocol_type"`           // "openai" or "anthropic"
	BaseURL      string `json:"base_url" config:"base_url"`                     // API server URL
	APIKey       string `json:"api_key" config:"api_key"`                       // API key
	ModelName    string `json:"model_name" config:"model_name"`                 // Model identifier
	ContextLimit int    `json:"context_limit" config:"context_limit,omitempty"` // Maximum context length (0 means unlimited)
	MaxTokens    int    `json:"max_tokens" config:"max_tokens,omitempty"`       // Maximum output tokens (0 means use provider default)
}

// FormatModelList formats a slice of ModelConfig to key-value block format.
// Blocks are separated by "---". The output has no trailing blank line.
func FormatModelList(models []ModelConfig) string {
	if len(models) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(models))
	for _, m := range models {
		blocks = append(blocks, strings.TrimSuffix(FormatKeyValue(m), "\n"))
	}
	return strings.Join(blocks, "\n---\n") + "\n"
}
