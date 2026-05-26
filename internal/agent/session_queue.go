package agent

// Session task queue: submit, enqueue, run, and manage queued tasks.
//
// The main loop (run()) manages the queue. Tasks are executed in their
// own goroutine via runTask(). The task queue is owned exclusively by
// the run() goroutine — no mutex needed.
//
// The task goroutine communicates state changes (step progress, new
// messages, token counts) back to run() via stateCh.

import (
	"context"
	"fmt"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Task Submission (called from run() goroutine only)
// ============================================================================

func (s *Session) submitTask(task Task) {
	queueEmpty := len(s.taskQueue) == 0
	// Clear paused-on-error only if queue was empty (new task will run immediately).
	if queueEmpty {
		s.pausedOnError.Store(false)
	}
	s.enqueueTask(task, false)
}

// submitDeferredCommand enqueues a deferred command at the front of the task queue.
// Deferred commands (e.g. :continue, :summarize) can only run when no task is
// currently in progress. They are placed at the front so they run ahead of
// any accumulated user prompts.
func (s *Session) submitDeferredCommand(cmd string) {
	if s.inProgress && !s.pausedOnError.Load() {
		s.writeError("Cannot run command while a task is running. Please wait or cancel first.")
		return
	}
	s.enqueueTask(CommandPrompt{Command: cmd}, true)
}

// enqueueTask adds a task to the queue. When front is true, the task is
// placed at the front so it runs before previously queued items.
//
// nextQueueID is goroutine-local (only accessed from the run() goroutine),
// so it's updated without synchronization.
func (s *Session) enqueueTask(task Task, front bool) {
	s.nextQueueID++
	queueID := fmt.Sprintf("Q%d", s.nextQueueID)

	switch t := task.(type) {
	case UserPrompt:
		t.queueID = queueID
		task = t
	case CommandPrompt:
		t.queueID = queueID
		task = t
	}

	item := QueueItem{
		Task:      task,
		QueueID:   queueID,
		CreatedAt: time.Now(),
	}

	if front {
		s.taskQueue = append([]QueueItem{item}, s.taskQueue...)
	} else {
		s.taskQueue = append(s.taskQueue, item)
	}

	s.sendSystemInfo()
}

// ============================================================================
// Task Runner (runs in its own goroutine)
// ============================================================================

// runTask executes a single task in its own goroutine. It is called from
// run() via "go s.runTask(item)". On completion it sends on taskDone so
// the main loop can start the next task.
//
// The task goroutine receives a snapshot of messages at task start. All
// state mutations during execution (step progress, new messages, token
// counts) are sent to run() via stateCh.
func (s *Session) runTask(item QueueItem) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start a goroutine to forward cancellation signals from inputPump
	// to the task's context.
	go func() {
		select {
		case <-s.taskCancelCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	defer cancel()
	defer func() {
		// Return the final message state to run() so it can replace
		// runMessages with the task goroutine's final state.
		// Non-blocking send — the channel is buffered (capacity 1).
		finalMessages := make([]llm.Message, len(s.Messages))
		copy(finalMessages, s.Messages)
		select {
		case s.taskResult <- finalMessages:
		default:
		}

		// Signal the main loop that this task has completed.
		select {
		case s.taskDone <- struct{}{}:
		default:
		}
	}()

	// Echo user prompts before any work so output ordering is correct even if
	// the task is canceled during initialization.
	if prompt, ok := item.Task.(UserPrompt); ok {
		s.signalPromptStart(prompt.Text)
	}

	s.requestSystemInfo()

	s.currentStep.Store(0)

	switch t := item.Task.(type) {
	case UserPrompt:
		s.handleUserPrompt(ctx, t.Text)
	case CommandPrompt:
		s.handleCommand(ctx, t.Command)
	}

	if ctx.Err() == context.Canceled {
		s.appendCancelMessage()
	}

	s.autoSaveIfEnabled()
}

// autoSaveIfEnabled saves the session to file if a session file is set.
// Called from the task goroutine. Sends a save request to run() which
// has the authoritative copy of messages.
func (s *Session) autoSaveIfEnabled() {
	if s.SessionFile == "" {
		return
	}
	s.sendEvent(SaveRequestEvent{})
}

// cancelMessage is inserted into the conversation history and displayed in the
// message window when a task is canceled by the user.
const cancelMessage = "The user canceled."

func (s *Session) appendCancelMessage() {
	s.Messages = append(s.Messages, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: cancelMessage}},
	})
	// Also push to the output so the cancel message appears live in the UI,
	// matching the behavior on session restore where TLV chunks are replayed.
	s.writeTLVStr(stream.TagTextAssistant, cancelMessage)
	s.Output.Flush()
}

// ============================================================================
// Queue Accessors (called from run() goroutine only)
// ============================================================================

// DeleteQueueItem removes a queue item by ID
func (s *Session) DeleteQueueItem(queueID string) bool {
	for i, item := range s.taskQueue {
		if item.QueueID == queueID {
			s.taskQueue = append(s.taskQueue[:i], s.taskQueue[i+1:]...)
			return true
		}
	}
	return false
}

// UpdateQueueItem updates the content of a queue item by ID.
func (s *Session) UpdateQueueItem(queueID, newContent string) bool {
	for i, item := range s.taskQueue {
		if item.QueueID == queueID {
			switch t := item.Task.(type) {
			case UserPrompt:
				t.Text = newContent
				s.taskQueue[i].Task = t
			case CommandPrompt:
				t.Command = newContent
				s.taskQueue[i].Task = t
			}
			return true
		}
	}
	return false
}
