// Package factory creates LLM providers from configuration.
package factory

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// ProviderConfig configures a provider
type ProviderConfig struct {
	Type       string // "anthropic", "openai"
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	MaxTokens  int // Maximum output tokens (0 = provider default)
}

// NewProvider creates a provider based on configuration
func NewProvider(config ProviderConfig) (llm.Provider, error) {
	cfg := providers.BaseConfig{
		APIKey:     config.APIKey,
		BaseURL:    config.BaseURL,
		Model:      config.Model,
		HTTPClient: config.HTTPClient,
		MaxTokens:  config.MaxTokens,
	}

	switch strings.ToLower(config.Type) {
	case "anthropic":
		return providers.NewAnthropicWithConfig(cfg)
	case "openai":
		return providers.NewOpenAIWithConfig(cfg)
	default:
		return nil, fmt.Errorf("provider: unknown provider type: %s", config.Type)
	}
}
