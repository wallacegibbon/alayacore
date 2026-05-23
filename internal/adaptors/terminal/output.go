package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text with lipgloss (or any
// ANSI styling), each segment must be rendered individually before concatenation.
// You cannot render a string that already contains ANSI codes with a new style and
// expect it to work - the outer styling will not wrap the inner styled segments.
// Always render segments separately, then join them.

// LOCK ORDERING DESIGN:
//
// outputWriter.mu protects only the raw TLV buffer and the
// processBuffer() → writeColored() → windowBuffer call chain.
//
// The sessionState embedded in outputWriter has its own mutex protecting
// status/model/queue fields. Lock ordering across goroutines:
//
//   outputWriter.mu → sessionState.mu      (session goroutine)
//   WindowBuffer.mu  → sessionState.mu      (Bubble Tea goroutine)
//
// No path holds both outputWriter.mu and WindowBuffer.mu simultaneously
// with sessionState.mu, so no deadlock is possible.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
)

// outputWriter parses TLV from the session and writes styled content to the WindowBuffer.
// It implements io.Writer for the agent/session output stream.
type outputWriter struct {
	windowBuffer *WindowBuffer
	buffer       []byte
	mu           sync.Mutex // protects buffer and processBuffer
	dirty        atomic.Bool
	styles       atomic.Pointer[Styles]
	nextWindowID atomic.Int64
	status       sessionState // cached session state (status, models, queue)
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
// Called from processBuffer which holds outputWriter.mu.
// Delegates to sessionState for thread-safe field updates.
func (to *outputWriter) handleSystemTag(value string) {
	var info agentpkg.SystemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	to.status.updateFromSystemInfo(info)
	to.dirty.Store(true)
}

// SnapshotStatus returns a consistent point-in-time view of session status.
func (to *outputWriter) SnapshotStatus() StatusSnapshot {
	return to.status.snapshotStatus()
}

// SnapshotModels returns a consistent point-in-time view of model state.
func (to *outputWriter) SnapshotModels() ModelSnapshot {
	return to.status.snapshotModels()
}

// GetQueueItems returns and clears the pending queue items.
func (to *outputWriter) GetQueueItems() []QueueItem {
	return to.status.takeQueueItems()
}

// generateWindowID returns a unique window ID for non-delta messages.
func (to *outputWriter) generateWindowID() string {
	return fmt.Sprintf("win%d", to.nextWindowID.Add(1))
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.SetWidth(width)
}
