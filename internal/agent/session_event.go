// session_event.go

package agent

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on stateCh.
// The run() goroutine processes them by type-switching in its main loop.
// This keeps all cross-goroutine communication explicit and auditable
// — the entire package uses channels and atomics for synchronization.

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
// Carries only token usage metadata. The final message state and
// ContentParts are returned together via taskResult on task completion.
type StepFinishEvent struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (StepFinishEvent) taskEvent() {}

// SetContextTokensEvent sets ContextTokens on the run() goroutine.
// Used by summarize() to correct the value after the StepFinishEvent
// from processPrompt overwrites it with the full old-context token count.
type SetContextTokensEvent struct {
	Tokens int64
}

func (SetContextTokensEvent) taskEvent() {}

// sendEvent sends a task event to the run() goroutine.
// Blocks until the event is received. The buffered channel (capacity 64)
// means this only blocks when run() is seriously backed up.
func (s *Session) sendEvent(ev TaskEvent) {
	s.stateCh <- ev
}
