// session_event.go

package agent

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on stateCh.
// The run() goroutine processes them in order in its main select loop.
// This keeps all cross-goroutine communication explicit and auditable
// — the entire package uses channels and atomics, never sync.Mutex.

import "github.com/alayacore/alayacore/internal/llm"

// eventType classifies a taskEvent.
type eventType int

const (
	eventStepStart eventType = iota
	eventStepFinish
	eventAppendMessages // generic message append (cancel message, etc.)
	eventCleanMessages
	eventSetPaused
	eventSaveRequest
	eventTaskDone
	eventSyncThink
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

	// eventAppendMessages
	appendMsgs []llm.Message

	// eventSetPaused
	paused bool

	// eventSaveRequest (payload not needed — uses s.Messages)

	// eventSyncThink
	thinkLevel int
}

// sendEvent sends a task event to the run() goroutine.
// Non-blocking if the channel buffer is full — the event is dropped.
func (s *Session) sendEvent(ev taskEvent) {
	select {
	case s.stateCh <- ev:
	default:
	}
}
