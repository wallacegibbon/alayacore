// Package providers implements LLM provider clients.
//
// This file contains the shared provider infrastructure used by
// both Anthropic and OpenAI providers. It handles:
//   - Common configuration (BaseConfig)
//   - Shared provider fields (baseProvider)
//   - HTTP request construction and response handling
//
// Provider-specific wire formats (message conversion, event parsing,
// tool formatting, and SSE scanning) live in anthropic.go and openai.go
// respectively.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Shared Provider Boilerplate
// ============================================================================

// BaseConfig holds the common configuration shared by all LLM providers.
type BaseConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	MaxTokens  int // 0 means use provider default
}

// baseProvider holds the common fields shared by all LLM providers.
// Embedded by AnthropicProvider and OpenAIProvider.
type baseProvider struct {
	apiKey         string
	baseURL        string
	client         *http.Client
	model          string
	maxTokens      int
	reasoningLevel int // 0=off, 1=normal, 2=max
	videoFPS       int // frames per second for video attachments; 0 means default (2)
	videoRes       int // video resolution mode: 0 or 1
}

// setBaseConfig applies the common config to a baseProvider.
func (b *baseProvider) setBaseConfig(cfg BaseConfig, defaultModel string) {
	b.apiKey = cfg.APIKey
	b.baseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	b.model = cfg.Model
	if b.model == "" {
		b.model = defaultModel
	}
	b.client = cfg.HTTPClient
	if b.client == nil {
		b.client = &http.Client{}
	}
	b.maxTokens = cfg.MaxTokens
	if b.maxTokens == 0 {
		b.maxTokens = llm.DefaultMaxTokens
	}
}

// buildRequest creates an HTTP POST request with common headers.
func (b *baseProvider) buildRequest(ctx context.Context, urlSuffix string, body any) (*http.Request, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+urlSuffix, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// doRequest sends the request and handles non-200 responses.
// Returns the response body reader (caller must close).
func (b *baseProvider) doRequest(req *http.Request) (io.ReadCloser, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("API error (status %d): failed to read error body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}
