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

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
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

	// Active user window — set on first user frame (UT/UI/UV/UA/UD),
	// cleared on next non-user tag. Each new frame updates the
	// window incrementally and marks dirty for immediate render.
	activeUserWindowIdx int    // index into windowBuffer.windows, -1 = none
	activeUserWindowID  string // window ID (for LookupID fallback)
	pendingUserMaxID    uint64 // max history ID across all parts
}

func NewTerminalOutput(styles *Styles) *outputWriter { //nolint:revive // tests need access to internal methods
	to := &outputWriter{
		windowBuffer:        NewWindowBuffer(DefaultWidth, styles),
		status:              sessionState{mu: &sync.Mutex{}},
		activeUserWindowIdx: -1,
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
	to.dirty.Store(true)
}

// WriteNotify writes a notification message to the display.
// Styling is stored raw — it's applied during render by styleByTag.
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, msg)
	to.dirty.Store(true)
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
	if to.activeUserWindowIdx >= 0 && !userTag(tag) {
		to.flushUserContent()
	}

	switch tag {
	// Streaming delta frames — incremental content appended to existing window.
	// Use uppercase tag for styling (lowercase/uppercase share same display).
	case tlv.TagAssistantTDelta, tlv.TagAssistantRDelta:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			id = to.generateWindowID()
			content = value
		}
		// Map lowercase delta tag to uppercase for display styling.
		displayTag := tlv.TagAssistantT
		if tag == tlv.TagAssistantRDelta {
			displayTag = tlv.TagAssistantR
		}
		to.windowBuffer.AppendOrUpdate(displayTag, id, content)

	// Complete authoritative frames — content already delivered via deltas
	// during live streaming, but carries the authoritative content during
	// replay. Only display if no window exists yet (replay case).
	case tlv.TagAssistantT, tlv.TagAssistantR:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			id = to.generateWindowID()
			content = value
		}
		if !to.windowBuffer.HasWindow(id) {
			to.windowBuffer.AppendOrUpdate(tag, id, content)
		}

	// User text tag — may carry NUL-delimited historyID
	// User content tags — accumulated until next non-user tag triggers flush.
	case tlv.TagUserT:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			id = to.generateWindowID()
			content = value
		}
		to.bufferUserContent(id, content, tlv.TagUserT)

	case tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		id, _, ok := tlv.UnwrapID(value)
		if !ok {
			id = to.generateWindowID()
		}
		to.bufferUserContent(id, mediaLabel(tag), tag)

	// Function lifecycle (JSON: id, type, name, input, status)
	// May carry NUL-delimited historyID prefix
	case tlv.TagAssistantF:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			payload = value
		}
		var fd protocol.ToolInputData
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

	// Tool input delta — partial JSON arguments during streaming.
	case tlv.TagAssistantFDelta:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			payload = value
		}
		var fd protocol.ToolInputDeltaData
		if err := json.Unmarshal([]byte(payload), &fd); err != nil {
			return
		}
		// Resolve tool name from existing window if known.
		name := ""
		if info := to.windowBuffer.GetFunctionInfo(fd.ID); info != nil {
			name = info.Name
		}
		to.windowBuffer.HandleToolInputDelta(fd.ID, name, fd.Delta, parseHistoryID(id))

	// Function result (JSON: id, content, is_error)
	// May carry NUL-delimited historyID prefix
	case tlv.TagUserF:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			payload = value
		}
		var tr protocol.ToolOutputData
		if err := json.Unmarshal([]byte(payload), &tr); err != nil {
			return
		}
		// Extract display text from the content JSON array
		displayText := extractToolOutputDisplayText(tr.Output)
		to.windowBuffer.HandleToolOutput(tr.ID, displayText, tr.IsError, parseHistoryID(id))

	// System tags
	case tlv.TagSystemMsg:
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
	case tlv.TagAssistantT, tlv.TagAssistantR, tlv.TagAssistantF,
		tlv.TagAssistantTDelta, tlv.TagAssistantRDelta, tlv.TagAssistantFDelta,
		tlv.TagUserT, tlv.TagUserF, tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		to.dirty.Store(true)
	}
}

// handleSystemMsg processes a TagSystemMsg frame.
// Called from processBuffer which holds outputWriter.mu.
func (to *outputWriter) handleSystemMsg(value string) {
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		return
	}
	switch protocol.SystemMsgType(env.Type) {
	case protocol.MsgTypeError:
		to.handleSystemError(env.Data)
	case protocol.MsgTypeNotify:
		to.handleSystemNotify(env.Data)
	case protocol.MsgTypeTask:
		to.handleSystemTask(env.Data)
	case protocol.MsgTypeModel:
		to.handleSystemModel(env.Data)
	case protocol.MsgTypeModelList:
		to.handleSystemModelList(env.Data)
	case protocol.MsgTypeTheme:
		to.handleSystemTheme(env.Data)
	case protocol.MsgTypeThemeList:
		to.handleSystemThemeList(env.Data)
	case protocol.MsgTypeReasoning:
		to.handleSystemReasoning(env.Data)
	case protocol.MsgTypeVideoConfig:
		to.handleSystemVideoConfig(env.Data)
	case protocol.MsgTypeToolConfirm:
		to.handleSystemToolConfirm(env.Data)
	case protocol.MsgTypeMCP:
		to.handleSystemMCP(env.Data)
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
		Models []config.ModelConfig `json:"models"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateModelList(m.Models)
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

// handleSystemMCP processes a single "mcp" system message.
// All MCP init progress (connecting, auth confirm, done) comes through here.
func (to *outputWriter) handleSystemMCP(data json.RawMessage) {
	var msg struct {
		Status string `json:"status"`
		Server string `json:"server,omitempty"`
		URL    string `json:"url,omitempty"`
		Error  string `json:"error,omitempty"`
		State  string `json:"state,omitempty"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}

	switch msg.Status {
	case "connecting", "connected", "failed":
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case "auth_confirm":
		if msg.Server != "" {
			to.status.updateMCPProgress("auth_confirm", msg.Server)
			to.status.setMCPAuthPending(msg.Server, msg.URL, msg.State)
		}
	case "auth_running":
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case "done":
		to.status.updateMCPProgress("done", "")
		// Note: takeMCPDone() is consumed by the Terminal tick handler
		// via outputWriter.ConsumeMCPDone().
	}
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

// GetPendingMCPAuth returns a pending MCP auth confirmation, if any.
func (to *outputWriter) GetPendingMCPAuth() (server, url, state string, ok bool) {
	return to.status.takeMCPAuthPending()
}

// ClearMCPAuths discards any pending MCP auth confirmations.
// Used when the user cancels MCP initialization mid-auth.
func (to *outputWriter) ClearMCPAuths() {
	to.status.clearMCPAuths()
}

// ConsumeMCPDone returns true if MCP init just completed,
// and resets the flag. The Terminal uses this to close the progress overlay.
// Also clears any stale MCP auth confirmations that may have been queued
// before the cancellation took effect.
func (to *outputWriter) ConsumeMCPDone() bool {
	done := to.status.takeMCPDone()
	if done {
		to.status.clearMCPAuths()
	}
	return done
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
	return tag == tlv.TagUserT || tag == tlv.TagUserI ||
		tag == tlv.TagUserV || tag == tlv.TagUserA || tag == tlv.TagUserD
}

// mediaLabel returns a display label for media tags.
func mediaLabel(tag string) string {
	switch tag {
	case tlv.TagUserI:
		return "📎 Image"
	case tlv.TagUserV:
		return "🎬 Video"
	case tlv.TagUserA:
		return "🎵 Audio"
	case tlv.TagUserD:
		return "📄 Document"
	}
	return ""
}

// bufferUserContent processes one user content frame.
// On the first frame, creates a new window. On subsequent frames,
// updates the existing window incrementally via AppendFromTLV.
// Always marks dirty so the next tick renders the latest state.
func (to *outputWriter) bufferUserContent(id, content string, tag string) {
	if to.activeUserWindowIdx < 0 {
		// First frame — create the window
		to.activeUserWindowID = id
		to.activeUserWindowIdx = to.windowBuffer.AppendOrUpdate(tlv.TagUserT, id, "")
		to.windowBuffer.SetWindowVisible(id)
	}

	// Track max history ID
	if hid, err := strconv.ParseUint(id, 10, 64); err == nil && hid > to.pendingUserMaxID {
		to.pendingUserMaxID = hid
	}

	// Feed the frame to the window's userRenderer via safe method
	to.windowBuffer.AppendUserContent(to.activeUserWindowID, tag, content)
	to.dirty.Store(true)
}

// flushUserContent finalizes the active user window (sets HistoryID)
// and resets the active window tracking.
func (to *outputWriter) flushUserContent() {
	if to.activeUserWindowIdx < 0 {
		return
	}
	to.windowBuffer.SetHistoryID(to.activeUserWindowIdx, to.pendingUserMaxID)
	to.activeUserWindowIdx = -1
	to.activeUserWindowID = ""
	to.pendingUserMaxID = 0
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.SetWidth(width)
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
