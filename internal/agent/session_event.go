// session_event.go

package agent

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on stateCh.
// The run() goroutine processes them by type-switching in its main loop.
// This keeps all cross-goroutine communication explicit and auditable
// — the entire package uses channels and atomics for synchronization.

import "github.com/alayacore/alayacore/internal/llm"

// TaskEvent is a state mutation sent from the task goroutine to run().
// Each concrete type carries only its own fields — no shared struct.
type TaskEvent interface {
	taskEvent()
}

// StepStartEvent signals that a new agent step has started.
type StepStartEvent struct {
	Step int
}

func (StepStartEvent) taskEvent() {}

// StepFinishEvent signals that an agent step has completed.
type StepFinishEvent struct {
	Messages            []llm.Message
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (StepFinishEvent) taskEvent() {}

// SaveRequestEvent signals that the session should be auto-saved.
type SaveRequestEvent struct{}

func (SaveRequestEvent) taskEvent() {}

// sendEvent sends a task event to the run() goroutine.
// Non-blocking if the channel buffer is full — the event is dropped.
func (s *Session) sendEvent(ev TaskEvent) {
	select {
	case s.stateCh <- ev:
	default:
	}
}
