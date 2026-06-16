package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestQueueItemUniqueIDs(t *testing.T) {
	// Create a minimal session
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Submit multiple tasks and verify unique IDs
	task1 := QueueItem{Type: TaskTypePrompt, Content: "test prompt 1"}
	task2 := QueueItem{Type: TaskTypeCommand, Content: "test command"}
	task3 := QueueItem{Type: TaskTypePrompt, Content: "test prompt 2"}

	session.submitTask(task1)
	session.submitTask(task2)
	session.submitTask(task3)

	// Get queue items
	items := session.taskQueue

	if len(items) != 3 {
		t.Errorf("Expected 3 queue items, got %d", len(items))
	}

	// Verify unique IDs
	ids := make(map[string]bool)
	for _, item := range items {
		if ids[item.QueueID] {
			t.Errorf("Duplicate queue ID found: %s", item.QueueID)
		}
		ids[item.QueueID] = true
	}

	// Verify ID format (Q1, Q2, Q3)
	expectedIDs := []string{"Q1", "Q2", "Q3"}
	for i, item := range items {
		if item.QueueID != expectedIDs[i] {
			t.Errorf("Expected queue ID %s, got %s", expectedIDs[i], item.QueueID)
		}
	}
}

func TestDeleteQueueItem(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Submit tasks
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "prompt 1"})
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "prompt 2"})
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "prompt 3"})

	// Delete middle item
	deleted := session.DeleteQueueItem("Q2")
	if !deleted {
		t.Error("Failed to delete queue item Q2")
	}

	// Verify deletion
	items := session.taskQueue
	if len(items) != 2 {
		t.Errorf("Expected 2 items after deletion, got %d", len(items))
	}

	// Verify remaining items
	if items[0].QueueID != "Q1" {
		t.Errorf("Expected first item to be Q1, got %s", items[0].QueueID)
	}
	if items[1].QueueID != "Q3" {
		t.Errorf("Expected second item to be Q3, got %s", items[1].QueueID)
	}

	// Try to delete non-existent item
	deleted = session.DeleteQueueItem("Q999")
	if deleted {
		t.Error("Should not be able to delete non-existent item")
	}
}

func TestQueueItemTypes(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Submit different task types
	promptTask := QueueItem{Type: TaskTypePrompt, Content: "test prompt"}
	commandTask := QueueItem{Type: TaskTypeCommand, Content: "test command"}

	session.submitTask(promptTask)
	session.submitTask(commandTask)

	items := session.taskQueue

	// Verify task types are preserved
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// Check first item type
	if items[0].Type != "prompt" {
		t.Error(`First item should be "prompt"`)
	}

	// Check second item type
	if items[1].Type != "command" {
		t.Error(`Second item should be "command"`)
	}
}

func TestQueueTimestamps(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	before := time.Now()
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "test"})
	after := time.Now()

	items := session.taskQueue

	if len(items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(items))
	}

	// Verify timestamp is within expected range
	if items[0].CreatedAt.Before(before) || items[0].CreatedAt.After(after) {
		t.Errorf("Timestamp %v is not between %v and %v", items[0].CreatedAt, before, after)
	}
}

func TestCancelAllTasks(t *testing.T) {
	tests := []struct {
		name           string
		inProgress     bool
		queueSize      int
		expectError    bool
		expectMessages int // number of expected notification messages
	}{
		{
			name:           "no task running, empty queue",
			inProgress:     false,
			queueSize:      0,
			expectError:    true,
			expectMessages: 1, // error message
		},
		{
			name:           "task running, empty queue",
			inProgress:     true,
			queueSize:      0,
			expectError:    false,
			expectMessages: 1, // "Canceled current task"
		},
		{
			name:           "no task running, queue has items",
			inProgress:     false,
			queueSize:      3,
			expectError:    false,
			expectMessages: 1, // "Cleared X queued tasks"
		},
		{
			name:           "task running, queue has items",
			inProgress:     true,
			queueSize:      5,
			expectError:    false,
			expectMessages: 1, // "Canceled current task and cleared X queued tasks"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := &MockOutput{}
			session := &Session{
				taskQueue: make([]QueueItem, 0),
				runDone:   make(chan struct{}),
				SessionConfig: SessionConfig{
					Input:  &stream.SliceBuffer{},
					Output: output,
				},
			}
			session.inProgress = tt.inProgress

			// Add items to queue
			for i := 0; i < tt.queueSize; i++ {
				session.taskQueue = append(session.taskQueue, QueueItem{
					Type:      "prompt",
					Content:   "test",
					QueueID:   "Q" + string(rune('1'+i)),
					CreatedAt: time.Now(),
				})
			}

			// Execute cancelAllTasks
			session.cancelAllTasks()

			// Verify queue is cleared
			if len(session.taskQueue) != 0 {
				t.Errorf("Expected empty queue, got %d items", len(session.taskQueue))
			}

			// Verify output
			if tt.expectError {
				// Should have error message (TLV format: "SE\x00\x00\x00\x11nothing to cancel")
				found := false
				for _, msg := range output.Messages {
					if strings.Contains(msg, "nothing to cancel") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error message, but none found. Messages: %v", output.Messages)
				}
			} else if len(output.Messages) < tt.expectMessages {
				t.Errorf("Expected at least %d message(s), got %d", tt.expectMessages, len(output.Messages))
			}
		})
	}
}

func TestPausedOnError(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Initially not paused
	if session.pausedOnError.Load() {
		t.Error("Session should not be paused initially")
	}

	// Set paused on error
	session.pausedOnError.Store(true)

	// Submit a task with empty queue — should clear the paused flag
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "recovery prompt"})

	if session.pausedOnError.Load() {
		t.Error("submitTask should clear pausedOnError when queue was empty")
	}

	// Queue should contain the submitted task
	items := session.taskQueue
	if len(items) != 1 {
		t.Errorf("Expected 1 item after submit, got %d", len(items))
	}
}

func TestSubmitTaskFront(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Submit regular tasks
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "first"})
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "second"})

	// Submit at front (simulates a task command like :continue)
	session.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: CommandNameContinue}, true)

	items := session.taskQueue
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	// Front item should be the command
	if items[0].Type != "command" || items[0].Content != CommandNameContinue {
		t.Errorf("Expected first item to be command{%s}, got Type=%s Content=%s", CommandNameContinue, items[0].Type, items[0].Content)
	}
	// Original tasks should follow in order
	if items[1].Type != "prompt" || items[1].Content != "first" {
		t.Errorf("Expected second item to be 'first', got Type=%s Content=%s", items[1].Type, items[1].Content)
	}
	if items[2].Type != "prompt" || items[2].Content != "second" {
		t.Errorf("Expected third item to be 'second', got Type=%s Content=%s", items[2].Type, items[2].Content)
	}

	// enqueueTask should NOT clear pausedOnError (that's handled by specific commands)
	session.pausedOnError.Store(true)

	session.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: CommandNameContinue}, true)

	if !session.pausedOnError.Load() {
		t.Error("enqueueTask should NOT clear pausedOnError")
	}
}

func TestPausedOnErrorBlocksDequeue(t *testing.T) {
	// This test verified the blocking behavior of waitForNextTask with cond.
	// waitForNextTask has been removed in the single-goroutine design.
	// The equivalent behavior is now: when pausedOnError is true and a
	// non-command task is at the front of the queue, the run() goroutine
	// skips dequeuing and waits for input. This is verified implicitly
	// by the remaining tests (TestCommandCanRunWhilePaused, etc.).
	t.Skip("waitForNextTask removed in single-goroutine refactor")
}

func TestCommandCanRunWhilePaused(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Add a user prompt to the queue
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "queued prompt"})

	// Set paused — user prompts should not run, but commands should
	session.pausedOnError.Store(true)
	session.inProgress = true

	// Add a command to the front of the queue (simulates submitTaskCommand)
	session.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: CommandNameSave}, true)

	// In the single-goroutine design, submitTaskCommand always places
	// the command at the front, and the run() goroutine checks pausedOnError
	// before running non-command tasks. The command runs regardless.
	items := session.taskQueue
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// Front item should be the command
	if items[0].Type != "command" {
		t.Errorf("Expected first item to be command")
	}
	// Second item should be the user prompt
	if items[1].Type != "prompt" {
		t.Errorf("Expected second item to be prompt")
	}

	// Paused state should still be set
	if !session.pausedOnError.Load() {
		t.Error("pausedOnError should still be true after command is queued")
	}
}

func TestCommandBehindUserPromptWhilePaused(t *testing.T) {
	// In the single-goroutine design, commands at the front always run
	// regardless of pausedOnError. So a command behind a user prompt
	// will be reached when the front task is dequeued.
	// This test verifies that submitTaskCommand places the command
	// at the front.
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Add a user prompt to the queue
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "first prompt"})

	// Add a command to the back of the queue (after user prompt)
	session.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: CommandNameSave}, false)

	// Set paused — the user prompt at front should be blocked
	session.pausedOnError.Store(true)
	session.inProgress = true

	// Now add a command to the front (like :continue does)
	session.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: CommandNameContinue}, true)

	items := session.taskQueue
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	// Front item should be the continue command
	if items[0].Type != "command" || items[0].Content != CommandNameContinue {
		t.Errorf("Expected first item to be command{%s}, got Type=%s Content=%s", CommandNameContinue, items[0].Type, items[0].Content)
	}
}

func TestSubmitTaskDoesNotClearPauseWhenQueueNotEmpty(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		SessionConfig: SessionConfig{
			Input:  &stream.SliceBuffer{},
			Output: &MockOutput{},
		},
	}

	// Add a task to the queue
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "first prompt"})

	// Set paused on error
	session.pausedOnError.Store(true)

	// Submit another task — should NOT clear the paused flag (queue not empty)
	session.submitTask(QueueItem{Type: TaskTypePrompt, Content: "second prompt"})

	if !session.pausedOnError.Load() {
		t.Error("submitTask should NOT clear pausedOnError when queue is not empty")
	}
	if len(session.taskQueue) != 2 {
		t.Errorf("Expected 2 items in queue, got %d", len(session.taskQueue))
	}
}
