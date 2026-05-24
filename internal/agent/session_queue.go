package agent

// Session task queue: submit, enqueue, run, and manage queued tasks.
//
// The main loop (run()) manages the queue. Tasks are executed in their
// own goroutine via runTask(). Shared state (taskQueue) is protected by
// sync.Mutex on the Session struct. Simple flags (inProgress, pausedOnError)
// use atomic.Bool for lock-free access.

import (
	"context"
	"fmt"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Task Submission
// ============================================================================

func (s *Session) submitTask(task Task) {
	s.mu.Lock()
	queueEmpty := len(s.taskQueue) == 0
	s.mu.Unlock()
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
	if s.inProgress.Load() && !s.pausedOnError.Load() {
		s.writeError("Cannot run command while a task is running. Please wait or cancel first.")
		return
	}
	s.enqueueTask(CommandPrompt{Command: cmd}, true)
}

// enqueueTask adds a task to the queue. When front is true, the task is
// placed at the front so it runs before previously queued items.
//
// nextQueueID is goroutine-local (only accessed from the run() goroutine),
// so it's updated outside the mutex.
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

	s.mu.Lock()
	if front {
		s.taskQueue = append([]QueueItem{item}, s.taskQueue...)
	} else {
		s.taskQueue = append(s.taskQueue, item)
	}
	s.mu.Unlock()

	s.sendSystemInfo()
}

// ============================================================================
// Task Runner
// ============================================================================

// runTask executes a single task in its own goroutine. It is called from
// run() via "go s.runTask(item)". On completion it sends on taskDone so
// the main loop can start the next task.
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
		// Signal the main loop that this task has completed.
		// Non-blocking send — the channel is buffered (capacity 1).
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

	errMsg := s.ensureAgentInitialized()
	if errMsg != "" {
		s.writeError(errMsg)
		s.requestSystemInfo()
		return
	}

	s.mu.Lock()
	s.currentStep = 0
	s.mu.Unlock()

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
func (s *Session) autoSaveIfEnabled() {
	if s.SessionFile == "" {
		return
	}
	if err := s.saveSessionToFile(s.SessionFile); err != nil {
		s.writeNotifyf("Auto-save failed: %v", err)
	}
}

func (s *Session) appendCancelMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Messages) == 0 {
		return
	}
	if s.Messages[len(s.Messages)-1].Role == llm.RoleUser {
		s.Messages = append(s.Messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "The user canceled."}},
		})
	}
}

// GetQueueItems returns all queued items
func (s *Session) GetQueueItems() []QueueItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]QueueItem, len(s.taskQueue))
	copy(items, s.taskQueue)
	return items
}

// DeleteQueueItem removes a queue item by ID
func (s *Session) DeleteQueueItem(queueID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	s.mu.Lock()
	defer s.mu.Unlock()

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
