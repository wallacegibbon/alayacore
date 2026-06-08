package providers

// Shared streaming state for LLM providers.
// Both OpenAI and Anthropic providers accumulate usage and stop reasons
// during streaming. This file contains the common parts.
//
// IMPORTANT: All methods are called from within a single goroutine
// (the iterator consumed by streamEvents in agent.go), so no
// locking is needed.

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// streamUsage tracks token usage and stop reason during streaming.
// Embedded by provider-specific stream states.
type streamUsage struct {
	usage      llm.Usage
	stopReason string
}

func (s *streamUsage) setUsage(usage llm.Usage) {
	s.usage = usage
}

func (s *streamUsage) getUsage() llm.Usage {
	return s.usage
}

func (s *streamUsage) setStopReason(reason string) {
	s.stopReason = reason
}

func (s *streamUsage) getStopReason() string {
	return s.stopReason
}
