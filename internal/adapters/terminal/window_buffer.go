package terminal

// WindowBuffer manages multiple Windows with virtual rendering support.
// It coordinates line height tracking for cursor navigation and provides
// virtual rendering (only visible windows are rendered) for performance.

import (
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// WindowBuffer - Manages Multiple Windows with Virtual Rendering
// ============================================================================

// WindowBuffer holds a sequence of windows with virtual rendering support.
// WindowBuffer.mu only protects window data and is never nested inside
// outputWriter.mu — SnapshotStatus et al. use atomic fields to avoid
// lock ordering inversions. See output.go for details.
type WindowBuffer struct {
	mu          sync.Mutex
	windows     []*Window
	idIndex     map[string]int
	width       int
	styles      *Styles
	borderStyle lipgloss.Style
	cursorStyle lipgloss.Style

	// Line height tracking (for cursor navigation)
	lineHeights []int
	totalLines  int
	dirty       bool // true if lineHeights needs rebuild
	dirtyIndex  int  // index of single dirty window, -1 = clean, -2 = full rebuild

	// Virtual rendering state
	viewportYOffset int
	viewportHeight  int
}

// Sentinel values for dirtyIndex
const (
	dirtyClean       = -1 // no dirty windows
	dirtyFullRebuild = -2 // multiple windows dirty, need full rebuild
)

// NewWindowBuffer creates a new window buffer with given width and styles.
func NewWindowBuffer(width int, styles *Styles) *WindowBuffer {
	return &WindowBuffer{
		windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		styles:      styles,
		borderStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1),
		cursorStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1),
		lineHeights: []int{},
		dirtyIndex:  dirtyClean,
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
		// Invalidate all windows
		for _, w := range wb.windows {
			w.Invalidate()
		}
		wb.dirty = true
		wb.dirtyIndex = dirtyFullRebuild // all windows affected
	}
}

// Width returns the current window width.
func (wb *WindowBuffer) Width() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.width
}

// SetStyles updates the styles for the window buffer.
func (wb *WindowBuffer) SetStyles(styles *Styles) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.styles = styles
	wb.borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1)
	wb.cursorStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1)
	// Invalidate all windows to pick up new styles
	for _, w := range wb.windows {
		w.styles = styles // Update window's styles reference
		w.Invalidate()
	}
	wb.dirty = true
	wb.dirtyIndex = dirtyFullRebuild
}

// AppendOrUpdate adds content to an existing window or creates a new one.
// Used for text content (UT, AT, AR, SE, SN) and replayed UF sessions.
// Tool windows use HandleToolUseEvent and HandleToolResult instead.
func (wb *WindowBuffer) AppendOrUpdate(tag string, id string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	innerWidth := max(0, wb.width-BorderInnerPadding)

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		w.AppendContent(content, innerWidth)
		// Update visibility for delta windows when new content arrives
		if !w.Visible && hasVisibleContent(content) {
			w.Visible = true
		}
		wb.markDirty(idx)
		return
	}

	// Create new window
	folded := tag != stream.TagUserT && tag != stream.TagAssistantT
	historyID := parseHistoryID(id)
	w := &Window{
		ID:        id,
		HistoryID: historyID,
		Tag:       tag,
		Content:   content,
		Folded:    folded,
		Visible:   hasVisibleContent(content),
		styles:    wb.styles,
	}
	wb.windows = append(wb.windows, w)
	wb.idIndex[id] = len(wb.windows) - 1
	wb.markDirty(len(wb.windows) - 1)
}

// HandleToolUseEvent processes a TagAssistantF (AF) frame.
// A frame with Name non-empty and Input empty is a "start" that sets
// the tool name. All other frames carry actual tool arguments.
// Status defaults to "pending" when a tool window is created —
// the final status arrives via HandleToolResult (UF).
func (wb *WindowBuffer) HandleToolUseEvent(data stream.ToolUseData, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[data.ID]; ok {
		w := wb.windows[idx]
		if data.Name != "" && len(data.Input) == 0 {
			w.ToolName = data.Name
			if w.ToolInput == "" {
				w.ToolInput = string(data.Input)
			}
		} else {
			if data.Name != "" {
				w.ToolName = data.Name
			}
			w.ToolInput = string(data.Input)
		}
		if historyID > w.HistoryID {
			w.HistoryID = historyID
		}
		if w.Status == ToolStatusNone {
			w.Status = ToolStatusPending
		}
		w.Invalidate()
		wb.markDirty(idx)
		return
	}

	// Create new window. Status defaults to pending — the result will
	// update it to success/error when it arrives.
	w := &Window{
		ID:        data.ID,
		HistoryID: historyID,
		Tag:       stream.TagAssistantF,
		ToolName:  data.Name,
		ToolInput: string(data.Input),
		Folded:    true,
		Visible:   true,
		Status:    ToolStatusPending,
		styles:    wb.styles,
	}
	wb.windows = append(wb.windows, w)
	wb.idIndex[data.ID] = len(wb.windows) - 1
	wb.markDirty(len(wb.windows) - 1)
}

// HandleToolResult processes a TagUserF (UF) frame.
// Sets ToolOutput and updates Status from the result.
func (wb *WindowBuffer) HandleToolResult(id, output string, isError bool, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		w.ToolOutput = output
		if historyID > w.HistoryID {
			w.HistoryID = historyID
		}
		if isError {
			w.Status = ToolStatusError
		} else {
			w.Status = ToolStatusSuccess
		}
		w.Invalidate()
		wb.markDirty(idx)
		return
	}

	// No prior AF window (e.g. replayed from session file) — create one.
	status := ToolStatusSuccess
	if isError {
		status = ToolStatusError
	}
	w := &Window{
		ID:         id,
		HistoryID:  historyID,
		Tag:        stream.TagUserF,
		ToolOutput: output,
		Status:     status,
		Folded:     true,
		Visible:    true,
		styles:     wb.styles,
	}
	wb.windows = append(wb.windows, w)
	wb.idIndex[id] = len(wb.windows) - 1
	wb.markDirty(len(wb.windows) - 1)
}

// markDirty marks that line heights need rebuilding.
// Uses sentinel values to track single vs multiple dirty windows:
//   - dirtyClean (-1): no dirty windows
//   - dirtyFullRebuild (-2): multiple windows dirty, need full rebuild
//   - >= 0: index of the single dirty window
//
// This enables incremental updates during streaming (same window repeatedly)
// while correctly triggering full rebuild for session loading (multiple windows rapidly).
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.dirtyIndex == dirtyFullRebuild {
		// Already marked for full rebuild, keep it
		return
	}
	if wb.dirtyIndex >= 0 && wb.dirtyIndex != idx {
		// Different window already dirty - need full rebuild
		wb.dirtyIndex = dirtyFullRebuild
	} else {
		// Either clean or same window - mark just this one
		wb.dirtyIndex = idx
	}
	wb.dirty = true
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.dirty = true
	wb.dirtyIndex = dirtyClean
}

// WindowCount returns the number of windows.
func (wb *WindowBuffer) WindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.windows)
}

// WindowAt returns the window at the given index, or nil if out of bounds.
func (wb *WindowBuffer) WindowAt(index int) *Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if index < 0 || index >= len(wb.windows) {
		return nil
	}
	wb.windows[index].ensureContent()
	return wb.windows[index]
}

// AllWindows returns a copy of the windows slice for snapshotting.
// The returned slice contains the same *Window pointers (no deep copy).
// Each window's Content is built from parts before returning.
func (wb *WindowBuffer) AllWindows() []*Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	result := make([]*Window, len(wb.windows))
	for i, w := range wb.windows {
		w.ensureContent()
		result[i] = w
	}
	return result
}

// GetVisibleWindowCount returns the number of visible windows.
func (wb *WindowBuffer) GetVisibleWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	count := 0
	for _, w := range wb.windows {
		if w.Visible {
			count++
		}
	}
	return count
}

// ToggleFold toggles the fold state of a window.
func (wb *WindowBuffer) ToggleFold(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.windows) {
		return false
	}
	wb.windows[windowIndex].Folded = !wb.windows[windowIndex].Folded
	wb.markDirty(windowIndex)
	return true
}

// FunctionInfo holds details about a tool call window.
type FunctionInfo struct {
	ID    string // tool call ID
	Name  string // tool name (e.g. "read_file")
	Input string // tool call input/arguments (formatted for display)
}

// GetFunctionInfo returns tool call information for a given tool call ID.
// Returns nil if no window with that ID exists or if it's not a tool window.
func (wb *WindowBuffer) GetFunctionInfo(id string) *FunctionInfo {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		if w.ToolName != "" {
			return &FunctionInfo{
				ID:    w.ID,
				Name:  w.ToolName,
				Input: w.ToolInput,
			}
		}
	}
	return nil
}

// GetWindowContent returns the raw content of a window by index.
// For tool windows, returns tool input + tool output combined.
// Returns empty string if index is out of bounds.
func (wb *WindowBuffer) GetWindowContent(windowIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.windows) {
		return ""
	}

	w := wb.windows[windowIndex]
	if w.IsToolWindow() {
		if w.ToolOutput != "" {
			return w.ToolInput + "\n" + w.ToolOutput
		}
		return w.ToolInput
	}
	w.ensureContent()
	return w.Content
}

// ============================================================================
// Line Height Tracking
// ============================================================================

// ensureLineHeights rebuilds line heights if dirty.
// Supports incremental update when only one window changed.
//
// During incremental updates, only UpdateLineCount is called (fast path using
// len(wrappedLines) instead of join+count). The full window Render is deferred
// to GetAll → renderVirtual, which needs the rendered content for the viewport.
// This avoids an O(n) render in ensureLineHeights that would be immediately
// overwritten by renderVirtual's own w.Render() call.
func (wb *WindowBuffer) ensureLineHeights() {
	if !wb.dirty && len(wb.lineHeights) == len(wb.windows) {
		return
	}

	// Extend lineHeights slice if needed
	for len(wb.lineHeights) < len(wb.windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Incremental update: only re-render the dirty window
	if wb.dirtyIndex >= 0 && wb.dirtyIndex < len(wb.windows) {
		w := wb.windows[wb.dirtyIndex]
		// Only render and count lines for visible windows
		if w.Visible {
			// Fast path: UpdateLineCount uses len(wrappedLines) to avoid
			// an O(n) join. If wrappedLines is nil (first render or after
			// invalidation), UpdateLineCount returns false and we fall back
			// to the full Render which populates wrappedLines.
			if !w.UpdateLineCount(wb.width) {
				w.Render(wb.width, false, wb.styles, wb.borderStyle, wb.cursorStyle)
			}
			oldHeight := wb.lineHeights[wb.dirtyIndex]
			newHeight := w.LineCount()
			wb.lineHeights[wb.dirtyIndex] = newHeight
			wb.totalLines += newHeight - oldHeight
		} else {
			// Non-visible windows contribute 0 lines
			oldHeight := wb.lineHeights[wb.dirtyIndex]
			wb.lineHeights[wb.dirtyIndex] = 0
			wb.totalLines -= oldHeight
		}
	} else {
		// Full rebuild (dirtyIndex == dirtyFullRebuild or first init)
		wb.totalLines = 0
		for i, w := range wb.windows {
			// Only render and count lines for visible windows
			if w.Visible {
				w.Render(wb.width, false, wb.styles, wb.borderStyle, wb.cursorStyle)
				wb.lineHeights[i] = w.LineCount()
				wb.totalLines += wb.lineHeights[i]
			} else {
				// Non-visible windows contribute 0 lines
				wb.lineHeights[i] = 0
			}
		}
	}
	wb.dirty = false
	wb.dirtyIndex = dirtyClean
}

// GetWindowLineRange returns the start and end line numbers for a window.
// Returns (0, 0) if windowIndex is out of bounds.
// IMPORTANT: This calls ensureLineHeights() to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowLineRange(windowIndex int) (start, end int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0, 0
	}

	for i := range windowIndex {
		start += wb.lineHeights[i]
	}
	return start, start + wb.lineHeights[windowIndex]
}

// GetTotalLines returns total lines across all windows.
func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
}

// ForEachVisible iterates forward over visible windows starting from the
// given index (inclusive), calling fn with the window index and pointer.
// If fn returns false, iteration stops. Returns true if all visible windows
// were visited (fn never returned false).
//
// This is one of four core iteration methods:
//   - ForEachVisible(index, fn(i, w))        — forward from index
//   - ForEachVisibleBackward(index, fn(i, w)) — backward from index
//   - ForEachVisibleRanged(fn(i, start, end)) — forward from 0 with line ranges
//   - ForEachVisibleBackwardRanged(fn(i, start, end)) — backward from end with line ranges
//
// Use the non-ranged variants for property-based searches (j, k, f, b).
// Use the ranged variants for position-based searches (H, L, M, center helpers).
func (wb *WindowBuffer) ForEachVisible(start int, fn func(i int, w *Window) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()

	for i, w := range wb.windows {
		if i >= start && w.Visible && !fn(i, w) {
			return false
		}
	}
	return true
}

// ForEachVisibleBackward iterates backward over visible windows starting
// from the given index (inclusive). See ForEachVisible for callback semantics.
func (wb *WindowBuffer) ForEachVisibleBackward(start int, fn func(i int, w *Window) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()

	if start >= len(wb.windows) {
		start = len(wb.windows) - 1
	}
	if start < 0 {
		return true
	}

	for i := start; i >= 0; i-- {
		if wb.windows[i].Visible && !fn(i, wb.windows[i]) {
			return false
		}
	}
	return true
}

// ForEachVisibleRanged iterates forward over all visible windows, calling fn
// with the window index and its line range [start, end). If fn returns false,
// iteration stops. Returns true if all visible windows were visited.
//
// Use this variant for viewport-aware positioning (H, M, L, center helpers).
// For property-based searches, use ForEachVisible instead.
func (wb *WindowBuffer) ForEachVisibleRanged(fn func(i int, startLine, endLine int) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()

	pos := 0
	for i, w := range wb.windows {
		end := pos + wb.lineHeights[i]
		if w.Visible && !fn(i, pos, end) {
			return false
		}
		pos = end
	}
	return true
}

// ForEachVisibleBackwardRanged iterates backward over all visible windows,
// calling fn with the window index and its line range [start, end).
// If fn returns false, iteration stops. Returns true if all visible
// windows were visited.
func (wb *WindowBuffer) ForEachVisibleBackwardRanged(fn func(i int, startLine, endLine int) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()

	// Pass 1: compute total lines
	total := 0
	for _, h := range wb.lineHeights {
		total += h
	}

	// Pass 2: walk backward, deriving start/end from total
	pos := total
	for i := len(wb.windows) - 1; i >= 0; i-- {
		pos -= wb.lineHeights[i]
		if wb.windows[i].Visible && !fn(i, pos, pos+wb.lineHeights[i]) {
			return false
		}
	}
	return true
}

// FirstVisibleIndex returns the index of the first visible window, or -1.
func (wb *WindowBuffer) FirstVisibleIndex() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for i, w := range wb.windows {
		if w.Visible {
			return i
		}
	}
	return -1
}

// LastVisibleIndex returns the index of the last visible window, or -1.
func (wb *WindowBuffer) LastVisibleIndex() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for i := len(wb.windows) - 1; i >= 0; i-- {
		if wb.windows[i].Visible {
			return i
		}
	}
	return -1
}

// NearestVisibleIndex returns the index of a visible window nearest to the
// given index, searching forward first then backward, or -1 if no visible
// windows exist.
func (wb *WindowBuffer) NearestVisibleIndex(index int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	n := len(wb.windows)
	if n == 0 {
		return -1
	}
	// Clamp index to bounds
	if index < 0 {
		index = 0
	}
	if index >= n {
		index = n - 1
	}
	// Search forward first, then backward
	for i := index; i < n; i++ {
		if wb.windows[i].Visible {
			return i
		}
	}
	for i := index - 1; i >= 0; i-- {
		if wb.windows[i].Visible {
			return i
		}
	}
	return -1
}

// ============================================================================
// Virtual Rendering
// ============================================================================

// SetViewportPosition updates viewport state for virtual rendering.
func (wb *WindowBuffer) SetViewportPosition(yOffset, height int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.viewportYOffset = yOffset
	wb.viewportHeight = height
}

// GetAll returns rendered windows, using virtual rendering if viewport is set.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if len(wb.windows) == 0 {
		return ""
	}

	// Ensure line heights are current
	wb.ensureLineHeights()

	// Use virtual rendering if viewport is set
	if wb.viewportHeight > 0 {
		return wb.renderVirtual(cursorIndex)
	}

	// Full render
	return wb.renderAll(cursorIndex)
}

// renderVirtual renders only visible windows (with buffer)
func (wb *WindowBuffer) renderVirtual(cursorIndex int) string {
	// Calculate visible range with buffer
	bufferLines := wb.viewportHeight
	if bufferLines < 10 {
		bufferLines = 10
	}

	startLine := max(0, wb.viewportYOffset-bufferLines)
	endLine := wb.viewportYOffset + wb.viewportHeight + bufferLines

	startWindow := wb.findWindowAtLine(startLine)
	endWindow := wb.findWindowAtLine(endLine)

	// Add extra buffer windows
	bufferWindows := 5
	startWindow = max(0, startWindow-bufferWindows)
	endWindow = min(len(wb.windows)-1, endWindow+bufferWindows)

	var sb strings.Builder
	firstWritten := false
	for i := range wb.windows {
		// Skip non-visible windows entirely
		if !wb.windows[i].Visible {
			continue
		}

		if firstWritten {
			sb.WriteString("\n")
		}

		if i >= startWindow && i <= endWindow {
			// Render actual content
			sb.WriteString(wb.windows[i].Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, wb.cursorStyle))
		} else {
			// Render placeholder (blank lines)
			for j := 0; j < wb.lineHeights[i]; j++ {
				if j > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(" ")
			}
		}
		firstWritten = true
	}
	return sb.String()
}

// renderAll renders all visible windows
func (wb *WindowBuffer) renderAll(cursorIndex int) string {
	var sb strings.Builder
	firstWritten := false
	for i, w := range wb.windows {
		// Skip non-visible windows entirely
		if !w.Visible {
			continue
		}

		if firstWritten {
			sb.WriteString("\n")
		}
		sb.WriteString(w.Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, wb.cursorStyle))
		firstWritten = true
	}
	return sb.String()
}

// findWindowAtLine returns the window index containing the given line.
func (wb *WindowBuffer) findWindowAtLine(line int) int {
	current := 0
	for i, h := range wb.lineHeights {
		if current+h > line {
			return i
		}
		current += h
	}
	return len(wb.windows) - 1
}

// RenderWindowContent renders the content of a window (for testing).
func (wb *WindowBuffer) RenderWindowContent(w *Window, innerWidth int) string {
	if w.IsToolWindow() {
		return w.renderGenericContent(innerWidth, wb.styles, w.ToolInput)
	}
	return w.renderGenericContent(innerWidth, wb.styles, w.Content)
}

// parseHistoryID parses a history ID string (from the wire format) to uint64.
// Returns 0 if the string is not a valid number.
func parseHistoryID(id string) uint64 {
	if id == "" {
		return 0
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
