package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text with lipgloss (or any
// ANSI styling), each segment must be rendered individually before concatenation.
// You cannot render a string that already contains ANSI codes with a new style and
// expect it to work - the outer styling will not wrap the inner styled segments.
// Always render segments separately, then join them.

// LOCK ORDERING:
//
// Three mutexes exist, but they are NEVER nested across goroutines:
//
//   outputWriter.mu  — protects the raw TLV buffer and processBuffer() call chain
//   WindowBuffer.mu  — protects window data (windows slice, line heights)
//   sessionState.mu  — protects status/model/queue snapshot fields
//
// Session goroutine call paths (inside outputWriter.mu):
//   outputWriter.mu → WindowBuffer.mu      (content tags, exclusive with sessionState)
//   outputWriter.mu → sessionState.mu      (TagSystemMsg, exclusive with WindowBuffer)
//
// Bubble Tea goroutine call paths:
//   WindowBuffer.mu  (via display update, tick — never nested with sessionState)
//   sessionState.mu  (via snapshot methods — never nested with WindowBuffer)
//
// WindowBuffer.mu and sessionState.mu are NEVER held simultaneously — they
// are acquired and released in separate, sequential calls. No deadlock is
// possible since each goroutine only nests one additional lock under its
// "root" lock (or no nesting at all for the Bubble Tea goroutine).

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
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

// WriteError adds an error message to the display buffer with error styling
func (to *outputWriter) WriteError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate("SE", id, styles.Error.Render(msg))
}

// WriteNotify writes a notification message to the display
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate("SN", id, styles.System.Render(msg))
	to.triggerUpdateForTag("SN")
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
	case stream.TagAssistantT, stream.TagAssistantR:
		id, content, ok := stream.UnwrapDelta(value)
		if !ok {
			// No stream ID (e.g. replayed from session file) — each message
			// gets its own window.
			id = to.generateWindowID()
			content = value
		}
		// Pass raw content - styling is applied during render
		to.windowBuffer.AppendOrUpdate(tag, id, content)

	// Function lifecycle (JSON: id, type, name, input, status)
	case stream.TagAssistantF:
		var fd stream.FunctionData
		if err := json.Unmarshal([]byte(value), &fd); err != nil {
			return
		}
		// Format the call input for display. For "start", format "{}" as a
		// placeholder so the tool name appears immediately. Safe because
		// HandleFunctionEvent only sets ToolInput for "start" when it's
		// empty — real arguments from a prior "call" are never overwritten.
		if fd.Type == "call" {
			handler := GetHandler(fd.Name)
			fd.Input = handler.FormatCall(json.RawMessage(fd.Input), to.styles.Load())
		} else if fd.Type == "start" {
			handler := GetHandler(fd.Name)
			fd.Input = handler.FormatCall(json.RawMessage("{}"), to.styles.Load())
		}
		to.windowBuffer.HandleFunctionEvent(fd)

	// Function result (JSON: id, output, status)
	case stream.TagUserF:
		var tr stream.ToolResultData
		if err := json.Unmarshal([]byte(value), &tr); err != nil {
			return
		}
		to.windowBuffer.HandleFunctionResult(tr.ID, tr.Output, tr.Status)

	// System tags
	case stream.TagSystemMsg:
		to.handleSystemMsg(value)
		return

	// User text tag
	case stream.TagUserT:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(tag, id, value)

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(tag, id, value)
	}
}

// triggerUpdateForTag marks the display as dirty for tags that modify the display
func (to *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	// Text content tags — TagSystemMsg is NOT listed here because
	// handleSystemMsg() calls to.dirty.Store(true) itself, after all
	// state (including pendingQueueItems) has been fully updated.
	// Setting dirty early would create a race where the Bubble Tea
	// goroutine sees dirty=true but pendingQueueItems has not been set yet.
	case stream.TagAssistantT, stream.TagAssistantR, stream.TagUserT,
		stream.TagAssistantF:
		to.dirty.Store(true)
	}
}

// handleSystemMsg processes a TagSystemMsg frame.
// Called from processBuffer which holds outputWriter.mu.
func (to *outputWriter) handleSystemMsg(value string) {
	var env struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return
	}
	switch env.Type {
	case "error":
		to.handleSystemError(env.Data)
	case "notify":
		to.handleSystemNotify(env.Data)
	case "task":
		to.handleSystemTask(env.Data)
	case "model":
		to.handleSystemModel(env.Data)
	case "model_list":
		to.handleSystemModelList(env.Data)
	case "theme":
		to.handleSystemTheme(env.Data)
	case "theme_list":
		to.handleSystemThemeList(env.Data)
	case "reasoning":
		to.handleSystemReasoning(env.Data)
	case "tool_confirm":
		to.handleSystemToolConfirm(env.Data)
	}
	to.dirty.Store(true)
}

func (to *outputWriter) handleSystemError(data json.RawMessage) {
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate("SE", id, styles.Error.Render(m.Text))
}

func (to *outputWriter) handleSystemNotify(data json.RawMessage) {
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	id := to.generateWindowID()
	styles := to.styles.Load()
	to.windowBuffer.AppendOrUpdate("SN", id, styles.System.Render(m.Text))
}

func (to *outputWriter) handleSystemTask(data json.RawMessage) {
	var m struct {
		InProgress  bool        `json:"in_progress"`
		CurrentStep int         `json:"current_step"`
		MaxSteps    int         `json:"max_steps"`
		Context     int64       `json:"context"`
		TaskError   bool        `json:"task_error"`
		QueueItems  []QueueItem `json:"queue_items"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTask(m.InProgress, m.CurrentStep, m.MaxSteps, m.Context, m.TaskError, m.QueueItems)
}

func (to *outputWriter) handleSystemModel(data json.RawMessage) {
	var m struct {
		ActiveID     int    `json:"active_id"`
		ActiveName   string `json:"active_name"`
		ContextLimit int64  `json:"context_limit"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateModel(m.ActiveID, m.ActiveName, m.ContextLimit)
}

func (to *outputWriter) handleSystemModelList(data json.RawMessage) {
	var m struct {
		Models          []agentpkg.ModelInfo `json:"models"`
		ModelConfigPath string               `json:"model_config_path"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateModelList(m.Models, m.ModelConfigPath)
}

func (to *outputWriter) handleSystemTheme(data json.RawMessage) {
	var m struct {
		Name  string       `json:"name"`
		Theme *theme.Theme `json:"theme"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTheme(m.Name, m.Theme)
}

func (to *outputWriter) handleSystemThemeList(data json.RawMessage) {
	var m struct {
		Themes []struct {
			Name  string       `json:"name"`
			Theme *theme.Theme `json:"theme"`
		} `json:"themes"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	infos := make([]ThemeEntry, len(m.Themes))
	for i, t := range m.Themes {
		infos[i] = ThemeEntry{Name: t.Name, Theme: t.Theme}
	}
	to.status.updateThemeList(infos)
}

func (to *outputWriter) handleSystemReasoning(data json.RawMessage) {
	var m struct {
		Level int `json:"level"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateReasoning(m.Level)
}

// handleSystemToolConfirm processes a tool_confirm system message.
// Stores the pending state so the Terminal can open its confirm overlay.
func (to *outputWriter) handleSystemToolConfirm(data json.RawMessage) {
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &m) != nil || m.ID == "" {
		return
	}
	// Look up the tool info from the function data window for a richer message.
	toolName := ""
	toolInput := ""
	if info := to.windowBuffer.GetFunctionInfo(m.ID); info != nil {
		toolName = info.Name
		toolInput = info.Input
	}
	// Store pending state for the Terminal's confirm overlay.
	to.status.setToolConfirmPending(m.ID, toolName, toolInput)
}

// GetPendingToolConfirm returns any pending tool confirmation and clears it.
func (to *outputWriter) GetPendingToolConfirm() (id, toolName, toolInput string, ok bool) {
	return to.status.takeToolConfirmPending()
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
