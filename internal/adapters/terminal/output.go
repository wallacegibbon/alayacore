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
	"strconv"
	"sync"
	"sync/atomic"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

// userContentPart is a single piece of user content (text or media label)
// awaiting assembly into a display window. Parts arrive in order via TLV
// frames (UT, UI/UV/UA/UD) and are flushed into one window on MB or
// before the next non-user tag.
type userContentPart struct {
	tag     string // original TLV tag (UT, UI, UV, UA, UD)
	content string // text content or media URI
}

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

	// Pending user content — ordered list of parts accumulated until MB
	// or next non-user tag triggers flushUserContent.
	pendingUserParts []userContentPart
	pendingUserID    string // history ID of the first part (used as window ID)
	pendingUserMaxID uint64 // max history ID across all parts (used for history ordering)
}

func NewTerminalOutput(styles *Styles) *outputWriter { //nolint:revive // tests need access to internal methods
	to := &outputWriter{
		windowBuffer: NewWindowBuffer(DefaultWidth, styles),
		status:       sessionState{mu: &sync.Mutex{}},
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

// WriteError adds an error message to the display buffer with error styling.
// Styling is stored raw — it's applied during render by styleByTag.
func (to *outputWriter) WriteError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSE, id, msg)
}

// WriteNotify writes a notification message to the display.
// Styling is stored raw — it's applied during render by styleByTag.
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, msg)
	to.triggerUpdateForTag(TagWindowSN)
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
//
//nolint:gocyclo // dispatch over many tag types; each case is simple
func (to *outputWriter) writeColored(tag string, value string) {
	to.triggerUpdateForTag(tag)

	// Flush pending user content before any non-user tag.
	if len(to.pendingUserParts) > 0 && !userTag(tag) {
		to.flushUserContent()
	}

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

	// User text tag — may carry NUL-delimited historyID
	// User content tags — buffer until MB or next non-user tag.
	case stream.TagUserT:
		id, content, ok := stream.UnwrapDelta(value)
		if !ok {
			id = to.generateWindowID()
			content = value
		}
		to.bufferUserContent(id, content, stream.TagUserT)

	case stream.TagUserI, stream.TagUserV, stream.TagUserA, stream.TagUserD:
		id, _, ok := stream.UnwrapDelta(value)
		if !ok {
			id = to.generateWindowID()
		}
		to.bufferUserContent(id, mediaLabel(tag), tag)

	// Message boundary — flush pending user content as one window.
	case stream.TagMessageBoundary:
		if len(to.pendingUserParts) > 0 {
			to.flushUserContent()
		}

	// Function lifecycle (JSON: id, type, name, input, status)
	// May carry NUL-delimited historyID prefix
	case stream.TagAssistantF:
		id, payload, ok := stream.UnwrapDelta(value)
		if !ok {
			payload = value
		}
		var fd stream.ToolInputData
		if err := json.Unmarshal([]byte(payload), &fd); err != nil {
			return
		}

		// Resolve the tool name — prefer the existing window (input frame)
		// over the frame itself (start/combined).
		name := fd.Name
		if info := to.windowBuffer.GetFunctionInfo(fd.ID); info != nil {
			name = info.Name
		}

		// Format the input for display.
		if len(fd.Input) == 0 {
			fd.Input = json.RawMessage(name + ": \n")
		} else {
			handler := GetHandler(name)
			fd.Input = json.RawMessage(handler.FormatCall(fd.Input))
		}

		to.windowBuffer.HandleToolInputEvent(fd, parseHistoryID(id))

	// Function result (JSON: id, content, is_error)
	// May carry NUL-delimited historyID prefix
	case stream.TagUserF:
		id, payload, ok := stream.UnwrapDelta(value)
		if !ok {
			payload = value
		}
		var tr stream.ToolOutputData
		if err := json.Unmarshal([]byte(payload), &tr); err != nil {
			return
		}
		// Extract display text from the content JSON array
		displayText := extractToolOutputDisplayText(tr.Output)
		to.windowBuffer.HandleToolOutput(tr.ID, displayText, tr.IsError, parseHistoryID(id))

	// System tags
	case stream.TagSystemMsg:
		to.handleSystemMsg(value)
		return

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(tag, id, value)
	}
}

// triggerUpdateForTag marks the display as dirty for tags that modify the display.
//
// TagSystemMsg is NOT listed here because handleSystemMsg() calls dirty.Store(true)
// after processing, so it doesn't need the early dirty flag from this function.
func (to *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	case stream.TagAssistantT, stream.TagAssistantR, stream.TagAssistantF, stream.TagUserT, stream.TagUserF, stream.TagUserI, stream.TagUserV, stream.TagUserA, stream.TagUserD, stream.TagMessageBoundary:
		to.dirty.Store(true)
	}
}

// handleSystemMsg processes a TagSystemMsg frame.
// Called from processBuffer which holds outputWriter.mu.
func (to *outputWriter) handleSystemMsg(value string) {
	env, err := stream.ParseSystemMsg(value)
	if err != nil {
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
	case "video_config":
		to.handleSystemVideoConfig(env.Data)
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
	to.windowBuffer.AppendOrUpdate(TagWindowSE, id, m.Text)
}

func (to *outputWriter) handleSystemNotify(data json.RawMessage) {
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, m.Text)
}

func (to *outputWriter) handleSystemTask(data json.RawMessage) {
	var m struct {
		InProgress  bool  `json:"in_progress"`
		CurrentStep int   `json:"current_step"`
		MaxSteps    int   `json:"max_steps"`
		Context     int64 `json:"context"`
		TaskError   bool  `json:"task_error"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTask(m.InProgress, m.CurrentStep, m.MaxSteps, m.Context, m.TaskError)
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

// handleSystemVideoConfig updates the video FPS and resolution in session status.
func (to *outputWriter) handleSystemVideoConfig(data json.RawMessage) {
	var m struct {
		FPS int `json:"fps"`
		Res int `json:"res"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateVideoConfig(m.FPS, m.Res)
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

// generateWindowID returns a unique window ID for non-delta messages.
func (to *outputWriter) generateWindowID() string {
	return fmt.Sprintf("win%d", to.nextWindowID.Add(1))
}

// userTag returns true if tag is a user content tag.
func userTag(tag string) bool {
	return tag == stream.TagUserT || tag == stream.TagUserI ||
		tag == stream.TagUserV || tag == stream.TagUserA || tag == stream.TagUserD
}

// mediaLabel returns a display label for media tags.
func mediaLabel(tag string) string {
	switch tag {
	case stream.TagUserI:
		return "📎 Image"
	case stream.TagUserV:
		return "🎬 Video"
	case stream.TagUserA:
		return "🎵 Audio"
	case stream.TagUserD:
		return "📄 Document"
	}
	return ""
}

// bufferUserContent accumulates a user content part. When flushed,
// all accumulated parts appear as a single window with text first.
// tag is the original TLV tag (UT, UI, UV, UA, UD) so the renderer
// can distinguish text from different media types.
func (to *outputWriter) bufferUserContent(id, content string, tag string) {
	to.pendingUserParts = append(to.pendingUserParts, userContentPart{
		tag:     tag,
		content: content,
	})

	// Track the max history ID from the TLV delta.
	if hid, err := strconv.ParseUint(id, 10, 64); err == nil && hid > to.pendingUserMaxID {
		to.pendingUserMaxID = hid
	}
	// First part's history ID becomes the window ID.
	if to.pendingUserID == "" {
		to.pendingUserID = id
	}
}

// flushUserContent creates a single window from all accumulated user content.
func (to *outputWriter) flushUserContent() {
	if len(to.pendingUserParts) == 0 {
		return
	}

	// Create the window and feed all parts in order.
	// The userRenderer accumulates text and media separately and
	// renders them correctly at display time.
	idx := to.windowBuffer.AppendOrUpdate(stream.TagUserT, to.pendingUserID, "")
	for _, p := range to.pendingUserParts {
		to.windowBuffer.windows[idx].AppendFromTLV(p.tag, p.content)
	}
	to.windowBuffer.windows[idx].Visible = true
	to.windowBuffer.SetHistoryID(idx, to.pendingUserMaxID)

	to.pendingUserParts = nil
	to.pendingUserID = ""
	to.pendingUserMaxID = 0
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.SetWidth(width)
}

// joinLines joins non-empty strings with newlines, skipping empty entries.
func joinLines(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	n := 0
	for _, p := range parts {
		n += len(p) + 1 // content + possible newline
	}
	buf := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, p...)
	}
	return string(buf)
}

// extractToolOutputDisplayText extracts display text from a tool result content JSON array.
// The content is a JSON array of {"type":"text","text":"..."} items.
func extractToolOutputDisplayText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(content, &items); err != nil {
		return string(content)
	}
	for _, item := range items {
		if item.Type == "text" {
			return item.Text
		}
	}
	return "(non-text result)"
}
