package agent

// Session task queue: submit, enqueue, run, and manage queued tasks.

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
	// Clear paused-on-error only if queue was empty (new task will run immediately).
	// This must happen before enqueueTask signals the condition variable so
	// taskRunner sees consistent state when it wakes.
	if queueEmpty {
		s.pausedOnError = false
	}
	s.mu.Unlock()

	s.enqueueTask(task, false)
}

// submitDeferredCommand enqueues a deferred command at the front of the task queue.
// Deferred commands (e.g. :continue, :summarize) can only run when no task is
// currently in progress. They are placed at the front so they run ahead of
// any accumulated user prompts.
func (s *Session) submitDeferredCommand(cmd string) {
	s.mu.Lock()
	if s.inProgress && !s.pausedOnError {
		s.mu.Unlock()
		s.writeError("Cannot run command while a task is running. Please wait or cancel first.")
		return
	}
	s.mu.Unlock()

	s.enqueueTask(CommandPrompt{Command: cmd}, true)
}

// enqueueTask adds a task to the queue. When front is true, the task is
// placed at the front so it runs before previously queued items.
func (s *Session) enqueueTask(task Task, front bool) {
	s.mu.Lock()

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
	s.cond.Signal()
	s.mu.Unlock()
	s.sendSystemInfo()
}

// ============================================================================
// Task Runner
// ============================================================================

func (s *Session) taskRunner() {
	defer close(s.runnerDone)
	for {
		task, ok := s.waitForNextTask()
		if !ok {
			return
		}
		s.runTask(task)
		if !s.hasQueuedTasks() {
			s.setInProgress(false)
		}
	}
}

func (s *Session) waitForNextTask() (QueueItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if !s.hasRunnableItemLocked() {
			if s.sessionCtx.Err() != nil {
				return QueueItem{}, false
			}
			s.cond.Wait()
			continue
		}
		item := s.taskQueue[0]
		s.taskQueue = s.taskQueue[1:]
		s.inProgress = true
		return item, true
	}
}

// hasRunnableItemLocked reports whether the front of the task queue can be
// dequeued right now.  Commands are always runnable; other tasks require
// pausedOnError to be clear.
//
// Must be called with s.mu held.
func (s *Session) hasRunnableItemLocked() bool {
	if len(s.taskQueue) == 0 {
		return false
	}
	_, isCommand := s.taskQueue[0].Task.(CommandPrompt)
	return isCommand || !s.pausedOnError
}

func (s *Session) hasQueuedTasks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taskQueue) > 0
}

func (s *Session) setInProgress(v bool) {
	s.mu.Lock()
	changed := s.inProgress != v
	s.inProgress = v
	s.mu.Unlock()
	if changed {
		s.sendSystemInfo()
	}
}

func (s *Session) runTask(item QueueItem) {
	s.taskWg.Add(1)
	defer s.taskWg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancelCurrent = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancelCurrent = nil
		s.mu.Unlock()
	}()

	// Echo user prompts before any work so output ordering is correct even if
	// the task is canceled during initialization.
	if prompt, ok := item.Task.(UserPrompt); ok {
		s.signalPromptStart(prompt.Text)
	}

	s.sendSystemInfo()

	errMsg := s.ensureAgentInitialized()
	if errMsg != "" {
		s.writeError(errMsg)
		s.sendSystemInfo()
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
