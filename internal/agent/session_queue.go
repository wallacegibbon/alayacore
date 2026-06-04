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
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
)

// ============================================================================
// Task Submission (called from run() goroutine only)
// ============================================================================

func (s *Session) submitTask(item QueueItem) {
	queueEmpty := len(s.taskQueue) == 0
	// Clear paused-on-error only if queue was empty (new task will run immediately).
	if queueEmpty {
		s.pausedOnError.Store(false)
	}
	s.enqueueTask(item, false)
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
	s.enqueueTask(QueueItem{Type: TaskTypeCommand, Content: cmd}, true)
}

// enqueueTask adds a task to the queue. When front is true, the task is
// placed at the front so it runs before previously queued items.
//
// nextQueueID is goroutine-local (only accessed from the run() goroutine),
// so it's updated without synchronization.
func (s *Session) enqueueTask(item QueueItem, front bool) {
	s.nextQueueID++
	item.QueueID = fmt.Sprintf("Q%d", s.nextQueueID)
	item.CreatedAt = time.Now()

	if front {
		s.taskQueue = append([]QueueItem{item}, s.taskQueue...)
	} else {
		s.taskQueue = append(s.taskQueue, item)
	}

	s.sendSystemInfo("task")
}

// ============================================================================
// Task Runner (runs in its own goroutine)
// ============================================================================

// runTask executes a single task in its own goroutine. It is called from
// run() via "go s.runTask(ctx, item, taskMessages)". On completion it sends
// taskMessages on taskResult so the main loop can update s.Messages.
//
// The task goroutine receives a snapshot of messages at task start. All
// state mutations during execution (step progress, new messages, token
// counts) are sent to run() via stateCh. The task goroutine never writes
// to s.Messages directly — it operates on its local taskMessages copy
// and returns the final state via taskResult.
func (s *Session) runTask(ctx context.Context, item QueueItem, taskMessages []llm.Message) {
	defer func() {
		// Return the final message state to run() so it can update
		// s.Messages with the task goroutine's final state.
		// Non-blocking send — the channel is buffered (capacity 1).
		select {
		case s.taskResult <- taskMessages:
		default:
		}
	}()

	// Echo user prompts before any work so output ordering is correct even if
	// the task is canceled during initialization.
	if item.Type == TaskTypePrompt {
		s.signalPromptStart(item.Content)
	}

	s.requestSystemInfo()

	s.currentStep.Store(0)

	switch item.Type {
	case TaskTypePrompt:
		taskMessages = s.handleUserPrompt(ctx, taskMessages, item.Content, item.Images)
	case TaskTypeCommand:
		taskMessages = s.runTaskCommand(ctx, taskMessages, item.Content)
	}

	if ctx.Err() == context.Canceled {
		taskMessages = s.appendCancelMessage(taskMessages)
	}
}

// runTaskCommand handles a deferred command in the task goroutine.
// Unlike immediate commands (handled by handleCommand in run()'s goroutine),
// deferred commands operate on the task's local message copy and return it.
func (s *Session) runTaskCommand(ctx context.Context, messages []llm.Message, cmd string) []llm.Message {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return messages
	}
	switch parts[0] {
	case CommandNameSummarize:
		return s.summarize(ctx, messages)
	case CommandNameContinue:
		return s.handleContinue(ctx, messages, parts[1:])
	default:
		s.writeError(domainerrors.NewSessionErrorf("command", "unknown cmd <%s>", parts[0]).Error())
		return messages
	}
}

// cancelMessage is inserted into the conversation history and displayed in the
// message window when a task is canceled by the user.
const cancelMessage = "Canceled"

func (s *Session) appendCancelMessage(messages []llm.Message) []llm.Message {
	messages = append(messages, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{llm.TextPart{Text: cancelMessage}},
	})
	// Also push to the output so the cancel message appears live in the UI,
	// matching the behavior on session restore where TLV chunks are replayed.
	s.writeTLVStr(stream.TagAssistantT, cancelMessage)
	return messages
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
			s.taskQueue[i].Content = newContent
			return true
		}
	}
	return false
}
