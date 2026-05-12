package providers

// Shared streaming state for LLM providers.
// Both OpenAI and Anthropic providers accumulate usage and stop reasons
// during streaming. This file contains the common parts.

import (
	"github.com/alayacore/alayacore/internal/llm"
	"sync"
)

// streamUsage tracks token usage and stop reason during streaming.
// Embedded by provider-specific stream states.
type streamUsage struct {
	mu         sync.Mutex
	usage      llm.Usage
	stopReason string
}

func (s *streamUsage) setUsage(usage llm.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = usage
}

func (s *streamUsage) getUsage() llm.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

func (s *streamUsage) setStopReason(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopReason = reason
}

func (s *streamUsage) getStopReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopReason
}
