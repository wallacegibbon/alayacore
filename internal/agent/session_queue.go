package agent

// Session task queue: submit, enqueue, run, and manage queued tasks.
//
// The main loop (run()) manages the queue. Tasks are executed in their
// own goroutine via runTask(). The task queue is owned exclusively by
// the run() goroutine — no mutex needed.
//
// The task goroutine communicates state changes (step progress, new
// ContentParts, token counts) back to run() via stateCh.

import (
	"context"
	"fmt"
	"strconv"
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

// submitTaskCommand enqueues a task command at the front of the task queue.
// Task commands (e.g. :continue, :summarize) can only run when no task is
// currently in progress, or when the current task is paused on error (which also
// means inProgress is false — the task goroutine has returned).
// They are placed at the front so they run ahead of any accumulated user prompts.
func (s *Session) submitTaskCommand(cmd string) {
	if s.inProgress {
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
// the result on taskResult so the main loop can update s.Content and s.Messages.
//
// The task goroutine receives a snapshot of messages at task start. All
// state mutations during execution (step progress, new messages, token
// counts) are sent to run() via stateCh. The task goroutine never writes
// to s.Content or s.Messages directly.
func (s *Session) runTask(ctx context.Context, item QueueItem, taskMessages []llm.Message) {
	var entries []llm.ContentPart // accumulated ContentParts for this task

	defer func() {
		// Return the final state to run() so it can update
		// s.Content and s.Messages. Buffered channel (capacity 1),
		// only blocks if run() is backlogged on task results.
		s.taskResult <- TaskResult{Messages: taskMessages, Entries: entries}
	}()

	s.requestSystemInfo()

	switch item.Type {
	case TaskTypePrompt:
		taskMessages, entries = s.handleUserPrompt(ctx, taskMessages, entries, item.Content, item.Images)
	case TaskTypeCommand:
		taskMessages, entries = s.runTaskCommand(ctx, taskMessages, entries, item.Content)
	}

	if ctx.Err() == context.Canceled {
		taskMessages, entries = s.appendCancelMessage(taskMessages, entries)
	}
}

// runTaskCommand handles a task command in the task goroutine.
// Unlike commands dispatched directly by handleInputMsg in run(),
// task commands operate on the task's local message copy and return
// updated messages.
func (s *Session) runTaskCommand(ctx context.Context, messages []llm.Message, entries []llm.ContentPart, cmd string) ([]llm.Message, []llm.ContentPart) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return messages, entries
	}
	switch parts[0] {
	case CommandNameSummarize:
		return s.summarize(ctx, messages, entries)
	case CommandNameContinue:
		return s.handleContinue(ctx, messages, entries, parts[1:])
	default:
		s.writeError(domainerrors.NewSessionErrorf("command", "unknown cmd <%s>", parts[0]).Error())
		return messages, entries
	}
}

// cancelMessage is inserted into the conversation history and displayed in the
// message window when a task is canceled by the user.
const cancelMessage = "Canceled"

func (s *Session) appendCancelMessage(messages []llm.Message, entries []llm.ContentPart) ([]llm.Message, []llm.ContentPart) {
	messages = append(messages, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{&llm.TextPart{Text: cancelMessage}},
	})
	id := s.histIncAndGet()
	entries = append(entries, &llm.TextPart{
		Text: cancelMessage,
		ContentMeta: llm.ContentMeta{
			HistoryID: id,
			Role:      llm.RoleAssistant,
		},
	})
	s.writeTLVStr(stream.TagAssistantT, stream.WrapDelta(strconv.FormatUint(id, 10), cancelMessage))
	return messages, entries
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
