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
}

// NewProvider creates a provider based on configuration
func NewProvider(config ProviderConfig) (llm.Provider, error) {
	switch strings.ToLower(config.Type) {
	case "anthropic":
		opts := []providers.AnthropicOption{
			providers.WithAPIKey(config.APIKey),
		}
		if config.BaseURL != "" {
			opts = append(opts, providers.WithBaseURL(config.BaseURL))
		}
		if config.HTTPClient != nil {
			opts = append(opts, providers.WithHTTPClient(config.HTTPClient))
		}
		if config.Model != "" {
			opts = append(opts, providers.WithAnthropicModel(config.Model))
		}
		return providers.NewAnthropic(opts...)

	case "openai":
		opts := []providers.OpenAIOption{
			providers.WithOpenAIAPIKey(config.APIKey),
		}
		if config.BaseURL != "" {
			opts = append(opts, providers.WithOpenAIBaseURL(config.BaseURL))
		}
		if config.HTTPClient != nil {
			opts = append(opts, providers.WithOpenAIHTTPClient(config.HTTPClient))
		}
		if config.Model != "" {
			opts = append(opts, providers.WithOpenAIModel(config.Model))
		}
		return providers.NewOpenAI(opts...)

	default:
		return nil, fmt.Errorf("unknown provider type: %s", config.Type)
	}
}
