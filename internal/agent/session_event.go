// session_event.go

package agent

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on stateCh.
// The run() goroutine processes them in order in its main select loop.
// This keeps all cross-goroutine communication explicit and auditable
// — the entire package uses channels and atomics for synchronization.

import "github.com/alayacore/alayacore/internal/llm"

// eventType classifies a taskEvent.
type eventType int

const (
	eventStepStart eventType = iota
	eventStepFinish
	eventCleanMessages
	eventSetPaused
	eventSaveRequest
	eventTaskDone
	eventSyncReason
)

// taskEvent carries a state mutation from the task goroutine to run().
type taskEvent struct {
	typ eventType

	// eventStepStart
	step int

	// eventStepFinish
	messages            []llm.Message
	inputTokens         int64
	outputTokens        int64
	cacheReadTokens     int64
	cacheCreationTokens int64

	// eventSetPaused
	paused bool

	// eventSaveRequest (payload not needed — uses s.Messages)

	// eventSyncReason
	reasoningLevel int
}

// sendEvent sends a task event to the run() goroutine.
// Non-blocking if the channel buffer is full — the event is dropped.
func (s *Session) sendEvent(ev taskEvent) {
	select {
	case s.stateCh <- ev:
	default:
	}
}
