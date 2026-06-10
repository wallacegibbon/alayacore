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
// the result on taskResult so the main loop can update s.Content and s.Messages.
//
// The task goroutine receives a snapshot of messages at task start. All
// state mutations during execution (step progress, new messages, token
// counts) are sent to run() via stateCh. The task goroutine never writes
// to s.Content or s.Messages directly.
func (s *Session) runTask(ctx context.Context, item QueueItem, taskMessages []llm.Message) {
	var entries []ContentItem // accumulated ContentItems for this task

	defer func() {
		// Return the final state to run() so it can update
		// s.Content and s.Messages.
		select {
		case s.taskResult <- TaskResult{Messages: taskMessages, Entries: entries}:
		default:
		}
	}()

	// Echo user prompts before any work so output ordering is correct even if
	// the task is canceled during initialization.
	if item.Type == TaskTypePrompt {
		for _, img := range item.Images {
			id := s.histIncAndGet()
			entries = append(entries, ContentItem{
				ID:   id,
				Tag:  stream.TagUserI,
				Part: llm.ImagePart{DataURL: img},
			})
			s.writeTLVStr(stream.TagUserI, stream.WrapDelta(strconv.FormatUint(id, 10), img))
		}
		id := s.histIncAndGet()
		entries = append(entries, ContentItem{
			ID:   id,
			Tag:  stream.TagUserT,
			Part: llm.TextPart{Text: item.Content},
		})
		s.writeTLVStr(stream.TagUserT, stream.WrapDelta(strconv.FormatUint(id, 10), item.Content))
	}

	s.requestSystemInfo()

	s.currentStep.Store(0)

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

// runTaskCommand handles a deferred command in the task goroutine.
// Unlike immediate commands (handled by handleCommand in run()'s goroutine),
// deferred commands operate on the task's local message copy and return it.
func (s *Session) runTaskCommand(ctx context.Context, messages []llm.Message, entries []ContentItem, cmd string) ([]llm.Message, []ContentItem) {
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

func (s *Session) appendCancelMessage(messages []llm.Message, entries []ContentItem) ([]llm.Message, []ContentItem) {
	messages = append(messages, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{llm.TextPart{Text: cancelMessage}},
	})
	id := s.histIncAndGet()
	entries = append(entries, ContentItem{
		ID:   id,
		Tag:  stream.TagAssistantT,
		Part: llm.TextPart{Text: cancelMessage},
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
