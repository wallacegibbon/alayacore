// Package providers implements LLM provider clients.
//
// This file contains the shared HTTP streaming infrastructure used by
// both Anthropic and OpenAI providers. It handles:
//   - HTTP request construction and dispatch
//   - Response body management and error handling
//   - SSE line scanning (both event-named and data-only formats)
//
// Provider-specific wire formats (message conversion, event parsing,
// tool formatting) live in anthropic.go and openai.go respectively.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ============================================================================
// Shared Provider Boilerplate
// ============================================================================

// baseProvider holds the common fields shared by all LLM providers.
// Embedded by AnthropicProvider and OpenAIProvider.
type baseProvider struct {
	apiKey         string
	baseURL        string
	client         *http.Client
	model          string
	maxTokens      int
	reasoningLevel int // 0=off, 1=normal, 2=max
}

// newBaseProvider initializes common provider fields with defaults.
func newBaseProvider(apiKey, baseURL, defaultModel string, maxTokens int) baseProvider {
	return baseProvider{
		apiKey:    apiKey,
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		client:    &http.Client{},
		model:     defaultModel,
		maxTokens: maxTokens,
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

// ============================================================================
// SSE Line Scanning
// ============================================================================

// sseScanner reads SSE-formatted lines from an io.Reader.
// It supports two modes:
//   - Named-event SSE: lines with "event: <type>" followed by "data: <payload>"
//     terminated by a blank line (Anthropic format).
//   - Data-only SSE: lines with "data: <payload>", one per event (OpenAI format).
//
// The scanner emits one sseLine per complete event.

// SSE scanner buffer size constants.
const (
	// sseScannerInitBuf is the initial buffer size for the SSE scanner.
	sseScannerInitBuf = 64 * 1024 // 64KB

	// sseScannerMaxBuf is the maximum token size the SSE scanner can handle.
	sseScannerMaxBuf = 1024 * 1024 // 1MB
)

type sseScanner struct {
	scanner *bufio.Scanner

	// Accumulation state for named-event SSE
	eventType strings.Builder
	eventData strings.Builder
	hasEvent  bool // true if we've seen an "event:" line without its blank line terminator
}

// newSSEScanner creates an SSE scanner over the given reader.
func newSSEScanner(reader io.Reader) *sseScanner {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, sseScannerInitBuf)
	scanner.Buffer(buf, sseScannerMaxBuf)
	return &sseScanner{scanner: scanner}
}

// Next advances to the next complete SSE event.
// Returns false when the stream is exhausted or an error occurs.
func (s *sseScanner) Next() bool {
	for s.scanner.Scan() {
		line := s.scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			// Named-event SSE (Anthropic format)
			s.eventType.Reset()
			s.eventType.WriteString(strings.TrimPrefix(line, "event: "))
			s.eventData.Reset()
			s.hasEvent = true
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if s.hasEvent {
				s.eventData.WriteString(data)
			} else {
				// Data-only SSE (OpenAI format): emit immediately.
				// This is a complete event — there is no blank line terminator.
				s.eventType.Reset()
				s.eventData.Reset()
				s.eventData.WriteString(data)
				return true
			}
			continue
		}

		// Blank line terminates a named event (Anthropic format).
		// Reset hasEvent so the EOF-drain path doesn't re-emit this event.
		if line == "" && s.hasEvent {
			s.hasEvent = false
			return true
		}
	}

	// EOF reached. Drain any pending event that wasn't terminated by a blank line.
	// This handles truncated streams (e.g. server closes mid-event).
	if s.hasEvent {
		s.hasEvent = false
		return true
	}

	return false
}

// Err returns any error encountered during scanning.
func (s *sseScanner) Err() error {
	return s.scanner.Err()
}

// Event returns the current event's type and data payload.
// For data-only SSE (OpenAI format), eventType is empty.
func (s *sseScanner) Event() (eventType string, data string) {
	return s.eventType.String(), s.eventData.String()
}

// ============================================================================
// Common Types
// ============================================================================

// reasoningConfig holds the thinking/reasoning configuration for a provider request.
// Both Anthropic and OpenAI use parallel fields for this.
type reasoningConfig struct {
	Enabled bool   // true if reasoning is enabled
	Effort  string // "high", "max", "xhigh" — provider-dependent semantics
}

// computeReasoningConfig returns the reasoning config based on reasoning level.
// level 0 = off, 1 = normal (high), 2 = max/xhigh.
func computeReasoningConfig(level int) reasoningConfig {
	if level > 0 {
		effort := "high"
		if level >= 2 {
			effort = "max" // will be overridden by OpenAI to "xhigh"
		}
		return reasoningConfig{Enabled: true, Effort: effort}
	}
	return reasoningConfig{Enabled: false, Effort: ""}
}
