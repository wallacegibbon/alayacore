package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text with lipgloss (or any
// ANSI styling), each segment must be rendered individually before concatenation.
// You cannot render a string that already contains ANSI codes with a new style and
// expect it to work - the outer styling will not wrap the inner styled segments.
// Always render segments separately, then join them.

// LOCK ORDERING DESIGN:
//
// outputWriter.mu protects only the raw TLV buffer (buffer field) and the
// processBuffer() → writeColored() → windowBuffer.* call chain. The lock
// ordering is:
//
//	outputWriter.mu → WindowBuffer.mu   (inside Write / processBuffer)
//
// SNAPSHOT METHODS (SnapshotStatus, SnapshotModels, GetQueueItems) never
// acquire outputWriter.mu. All fields they read use atomic operations so
// they are safe to call while WindowBuffer.mu is held. This eliminates the
// lock ordering inversion that would occur if Bubble Tea's tick handler
// held WindowBuffer.mu and then tried to acquire outputWriter.mu.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
)

// modelsHolder wraps a models slice for atomic.Pointer storage.
type modelsHolder struct {
	models []agentpkg.ModelInfo
}

// queueItemsHolder wraps a queue items slice for atomic.Pointer storage.
type queueItemsHolder struct {
	items []QueueItem
}

// stringHolder wraps a string for atomic.Pointer storage.
type stringHolder struct {
	s string
}

// outputWriter parses TLV from the session and writes styled content to the WindowBuffer.
// It implements io.Writer for the agent/session output stream.
//
// All fields read by SnapshotStatus, SnapshotModels, and GetQueueItems are
// atomic — these methods never acquire outputWriter.mu.
type outputWriter struct {
	windowBuffer *WindowBuffer
	buffer       []byte
	mu           sync.Mutex // protects buffer and processBuffer; NOT used by snapshot methods
	dirty        atomic.Bool
	styles       atomic.Pointer[Styles]
	nextWindowID atomic.Int64

	// Status fields — read atomically by SnapshotStatus
	contextTokens   atomic.Int64
	contextLimit    atomic.Int64
	queueCount      atomic.Int32
	inProgress      atomic.Bool
	currentStep     atomic.Int32
	maxSteps        atomic.Int32
	lastCurrentStep atomic.Int32
	lastMaxSteps    atomic.Int32
	lastTaskError   atomic.Bool
	thinkLevel      atomic.Int32

	// Model fields — read atomically by SnapshotModels
	models          atomic.Pointer[modelsHolder]
	activeModelID   atomic.Int32
	activeModelName atomic.Pointer[stringHolder]
	hasModels       atomic.Bool
	modelConfigPath atomic.Pointer[stringHolder]

	// Queue items — read atomically by GetQueueItems
	pendingQueueItems atomic.Pointer[queueItemsHolder]
}

func NewTerminalOutput(styles *Styles) *outputWriter { //nolint:revive // tests need access to internal methods
	to := &outputWriter{
		windowBuffer: NewWindowBuffer(DefaultWidth, styles),
	}
	to.styles.Store(styles)
	return to
}

// SetStyles updates the styles for the output writer
func (to *outputWriter) SetStyles(styles *Styles) {
	to.styles.Store(styles)
	to.windowBuffer.SetStyles(styles)
}

// Close cleans up resources (no background goroutine to stop)
func (to *outputWriter) Close() error {
	return nil
}

func (to *outputWriter) Write(p []byte) (n int, err error) {
	to.mu.Lock()
	to.buffer = append(to.buffer, p...)
	to.processBuffer()
	to.mu.Unlock()
	return len(p), nil
}

func (to *outputWriter) WriteString(s string) (int, error) {
	return to.Write([]byte(s))
}

func (to *outputWriter) Flush() error {
	return nil
}

// AppendError adds an error message to the display buffer with error styling
func (to *outputWriter) AppendError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate(id, stream.TagSystemError, styles.Error.Render(msg))
}

// WriteNotify writes a notification message to the display
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate(id, stream.TagSystemNotify, styles.System.Render(msg))
	to.triggerUpdateForTag(stream.TagSystemNotify)
}

// processBuffer parses TLV-encoded data from the buffer
func (to *outputWriter) processBuffer() {
	for len(to.buffer) >= 6 {
		tag := string(to.buffer[0:2])
		length := int(binary.BigEndian.Uint32(to.buffer[2:6]))

		if len(to.buffer) < 6+length {
			break
		}

		value := string(to.buffer[6 : 6+length])
		to.writeColored(tag, value)
		to.buffer = to.buffer[6+length:]
	}
}

// writeColored writes styled content based on the TLV tag
func (to *outputWriter) writeColored(tag string, value string) {
	to.triggerUpdateForTag(tag)

	switch tag {
	// Text content tags — may carry NUL-delimited stream ID for live deltas,
	// or plain text when replayed from a saved session file.
	case stream.TagTextAssistant, stream.TagTextReasoning:
		id, content, ok := stream.UnwrapDelta(value)
		if !ok {
			// No stream ID (e.g. replayed from session file) — each message
			// gets its own window.
			id = to.generateWindowID()
			content = value
		}
		// Pass raw content - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, content)

	// Function call (JSON: id, name, input)
	case stream.TagFunctionCall:
		var tc stream.ToolCallData
		if err := json.Unmarshal([]byte(value), &tc); err != nil {
			return
		}
		handler := GetHandler(tc.Name)
		formatted := handler.FormatCall(json.RawMessage(tc.Input), to.styles.Load())

		// Pass formatted but unstyled content - styling is applied during render
		to.windowBuffer.AppendToolCall(tc.ID, tc.Name, formatted)

	// Function result (JSON: id, output)
	case stream.TagFunctionResult:
		var tr stream.ToolResultData
		if err := json.Unmarshal([]byte(value), &tr); err != nil {
			return
		}
		// Pass raw output - styling is applied during render
		to.windowBuffer.AppendOrUpdate(tr.ID, tag, tr.Output)

	// Function output status indicator
	case stream.TagFunctionState:
		id, content, ok := stream.UnwrapDelta(value)
		if !ok {
			return
		}
		// Update the tool window with status indicator
		to.windowBuffer.UpdateToolStatus(id, ParseToolStatus(content))

	// System tags
	case stream.TagSystemError:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	case stream.TagSystemNotify:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	case stream.TagSystemData:
		to.handleSystemTag(value)
		return

	// User text tag
	case stream.TagTextUser:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(id, tag, value)
	}
}

// triggerUpdateForTag marks the display as dirty for tags that modify the display
func (to *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	// Text content tags
	case stream.TagTextAssistant, stream.TagTextReasoning, stream.TagTextUser,
		stream.TagFunctionCall,
		// System tags
		stream.TagSystemError, stream.TagSystemNotify, stream.TagSystemData:
		to.dirty.Store(true)
	}
}

// handleSystemTag processes system information tags.
// Called from processBuffer which holds outputWriter.mu, but all field
// writes use atomic operations so snapshot methods (called without mu)
// see consistent values.
func (to *outputWriter) handleSystemTag(value string) {
	var info agentpkg.SystemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	to.updateStepTracking(info)
	to.updateModelState(info)
	to.updateQueueItems(info)
	to.currentStep.Store(int32(info.CurrentStep))
	to.maxSteps.Store(int32(info.MaxSteps))
	to.thinkLevel.Store(int32(info.ThinkLevel))
	to.dirty.Store(true)
}

// updateStepTracking handles the step-info bookkeeping around task transitions.
func (to *outputWriter) updateStepTracking(info agentpkg.SystemInfo) {
	// Save step info when task completes (transition from in-progress to done)
	if to.inProgress.Load() && !info.InProgress && to.maxSteps.Load() > 0 {
		to.lastCurrentStep.Store(to.currentStep.Load())
		to.lastMaxSteps.Store(to.maxSteps.Load())
		to.lastTaskError.Store(info.TaskError)
	}
	// Reset last step info when new task starts (transition from not-in-progress to in-progress)
	if !to.inProgress.Load() && info.InProgress {
		to.lastCurrentStep.Store(0)
		to.lastMaxSteps.Store(0)
		to.lastTaskError.Store(false)
	}
	to.inProgress.Store(info.InProgress)
	to.queueCount.Store(int32(len(info.QueueItems)))
	to.contextTokens.Store(info.ContextTokens)
	to.contextLimit.Store(info.ContextLimit)
}

// updateModelState updates cached model-related fields from SystemInfo.
func (to *outputWriter) updateModelState(info agentpkg.SystemInfo) {
	to.models.Store(&modelsHolder{models: info.Models})
	to.activeModelID.Store(int32(info.ActiveModelID))
	to.hasModels.Store(info.HasModels)
	to.modelConfigPath.Store(&stringHolder{s: info.ModelConfigPath})
	to.activeModelName.Store(&stringHolder{s: info.ActiveModelName})
}

// updateQueueItems converts and stores the queue items from SystemInfo.
func (to *outputWriter) updateQueueItems(info agentpkg.SystemInfo) {
	items := make([]QueueItem, len(info.QueueItems))
	for i, item := range info.QueueItems {
		createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
		if err != nil {
			createdAt = time.Now()
		}
		items[i] = QueueItem{
			QueueID:   item.QueueID,
			Type:      item.Type,
			Content:   item.Content,
			CreatedAt: createdAt,
		}
	}
	to.pendingQueueItems.Store(&queueItemsHolder{items: items})
}

// SnapshotStatus returns a consistent point-in-time view of session status.
// All fields use atomic operations — no mutex needed.
func (to *outputWriter) SnapshotStatus() StatusSnapshot {
	return StatusSnapshot{
		ContextTokens:   to.contextTokens.Load(),
		ContextLimit:    to.contextLimit.Load(),
		QueueCount:      int(to.queueCount.Load()),
		InProgress:      to.inProgress.Load(),
		CurrentStep:     int(to.currentStep.Load()),
		MaxSteps:        int(to.maxSteps.Load()),
		LastCurrentStep: int(to.lastCurrentStep.Load()),
		LastMaxSteps:    int(to.lastMaxSteps.Load()),
		TaskError:       to.lastTaskError.Load(),
		ThinkLevel:      int(to.thinkLevel.Load()),
	}
}

// SnapshotModels returns a consistent point-in-time view of model state.
// All fields use atomic operations — no mutex needed.
func (to *outputWriter) SnapshotModels() ModelSnapshot {
	snap := ModelSnapshot{
		ActiveID:   int(to.activeModelID.Load()),
		HasModels:  to.hasModels.Load(),
		ConfigPath: "",
		ActiveName: "",
	}
	if h := to.modelConfigPath.Load(); h != nil {
		snap.ConfigPath = h.s
	}
	if h := to.activeModelName.Load(); h != nil {
		snap.ActiveName = h.s
	}
	if h := to.models.Load(); h != nil {
		snap.Models = h.models
	}
	return snap
}

// GetQueueItems returns and clears the pending queue items.
// Uses atomic swap — no mutex needed.
func (to *outputWriter) GetQueueItems() []QueueItem {
	h := to.pendingQueueItems.Swap(nil)
	if h == nil {
		return nil
	}
	return h.items
}

// generateWindowID returns a unique window ID for non-delta messages.
func (to *outputWriter) generateWindowID() string {
	return fmt.Sprintf("win%d", to.nextWindowID.Add(1))
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.SetWidth(width)
}
