// session_event.go

package agent

import (
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
)

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on taskEventCh.
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
// ContentParts are returned together via taskResultCh on task completion.
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

// MCPUpdateEvent carries MCP initialization or OAuth authorization results
// to the session's run() goroutine. It is sent internally (not from the
// adapter — all adapter communication goes through TLV frames). The run()
// goroutine applies the updates (tools + system prompt) and manages the
// pending-OAuth counter.
//
// The initial event is produced by startMCPInitWatcher after AsyncInit
// completes. Subsequent events are produced by the OAuth goroutine in
// handleMCPAuth when a server authorization finishes.
//
// PendingOAuthCount is the number of OAuth servers that still need user
// authorization. Set by the initial async init event; the session adds
// this to its internal pendingOAuth counter. Each skipMCPAuth or
// completed OAuth authorization decrements the counter. When it reaches
// zero, mcpReady becomes true and user messages are accepted.
type MCPUpdateEvent struct {
	Tools              []llm.Tool
	SystemPromptSuffix string
	Manager            *mcp.Manager
	PendingOAuthCount  int32 // number of OAuth servers needing auth; 0 if none
}

// sendEvent sends a task event to the run() goroutine.
// Blocks until the event is received. The buffered channel (capacity 64)
// means this only blocks when run() is seriously backed up.
func (s *Session) sendEvent(ev TaskEvent) {
	s.taskEventCh <- ev
}
