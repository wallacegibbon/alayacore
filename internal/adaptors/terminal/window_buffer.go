package terminal

// WindowBuffer manages multiple Windows with virtual rendering support.
// It coordinates line height tracking for cursor navigation and provides
// virtual rendering (only visible windows are rendered) for performance.

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// WindowBuffer - Manages Multiple Windows with Virtual Rendering
// ============================================================================

// WindowBuffer holds a sequence of windows with virtual rendering support.
type WindowBuffer struct {
	mu          sync.Mutex
	Windows     []*Window // public for tests
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
		Windows:     []*Window{},
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
		for _, w := range wb.Windows {
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
	for _, w := range wb.Windows {
		w.styles = styles // Update window's styles reference
		w.Invalidate()
	}
	wb.dirty = true
	wb.dirtyIndex = dirtyFullRebuild
}

// AppendOrUpdate adds content to an existing window or creates a new one.
func (wb *WindowBuffer) AppendOrUpdate(id string, tag string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	innerWidth := max(0, wb.width-4)

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.Windows[idx]
		// Separator between call and result for content-heavy tool windows.
		if w.ToolName == "write_file" || w.ToolName == "edit_file" {
			w.AppendContent("\n"+toolResultSentinel+"\n", innerWidth)
		}
		w.AppendContent(content, innerWidth)
		// Update visibility for delta windows when new content arrives
		// Only need to check the delta (not accumulated content) since if Visible=false,
		// we know all previous content was whitespace
		if !w.IsToolWindow() && !w.Visible && hasVisibleContent(content) {
			w.Visible = true
		}
		wb.markDirty(idx)
		return
	}

	// Create new window
	folded := tag != stream.TagTextUser && tag != stream.TagTextAssistant
	w := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Folded:  folded,
		Visible: true, // Will be updated below for delta windows
		styles:  wb.styles,
	}
	// Tool windows are always visible; delta windows only when has visible content
	if !w.IsToolWindow() {
		w.Visible = hasVisibleContent(content)
	}
	wb.Windows = append(wb.Windows, w)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// AppendToolCall adds a tool call window with tool name.
// If a window with the same ID already exists (e.g. from a ToolCallStart event),
// its content is replaced with the full content.
func (wb *WindowBuffer) AppendToolCall(id string, toolName string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.Windows[idx]
		// Replace content — the window was pre-created by a ToolCallStart event;
		// now we have the full tool call content.
		w.ReplaceContent(content)
		wb.markDirty(idx)
		return
	}

	w := &Window{
		ID:       id,
		Tag:      stream.TagFunctionCall,
		ToolName: toolName,
		Content:  content,
		Folded:   true,
		Visible:  true, // Tool windows are always visible
		styles:   wb.styles,
	}
	wb.Windows = append(wb.Windows, w)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
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
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.dirty = true
	wb.dirtyIndex = dirtyClean
}

// GetWindowCount returns the number of windows.
func (wb *WindowBuffer) GetWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.Windows)
}

// GetVisibleWindowCount returns the number of visible windows.
func (wb *WindowBuffer) GetVisibleWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	count := 0
	for _, w := range wb.Windows {
		if w.Visible {
			count++
		}
	}
	return count
}

// GetWindow returns the window at the given index (for testing).
func (wb *WindowBuffer) GetWindow(index int) *Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if index < 0 || index >= len(wb.Windows) {
		return nil
	}
	return wb.Windows[index]
}

// ToggleFold toggles the fold state of a window.
func (wb *WindowBuffer) ToggleFold(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return false
	}
	wb.Windows[windowIndex].Folded = !wb.Windows[windowIndex].Folded
	wb.markDirty(windowIndex)
	return true
}

// GetWindowContent returns the raw content of a window by index.
// Returns empty string if index is out of bounds.
func (wb *WindowBuffer) GetWindowContent(windowIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return ""
	}

	return wb.Windows[windowIndex].Content
}

// UpdateToolStatus updates the status indicator for a tool window.
func (wb *WindowBuffer) UpdateToolStatus(toolCallID string, status ToolStatus) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[toolCallID]; ok {
		w := wb.Windows[idx]
		w.Status = status
		w.Invalidate()
		if status == ToolStatusSuccess || status == ToolStatusError {
			if w.ToolName == "write_file" {
				w.Folded = true
			}
		}
		wb.markDirty(idx)
	}
}

// ============================================================================
// Line Height Tracking
// ============================================================================

// ensureLineHeights rebuilds line heights if dirty.
// Supports incremental update when only one window changed.
func (wb *WindowBuffer) ensureLineHeights() {
	if !wb.dirty && len(wb.lineHeights) == len(wb.Windows) {
		return
	}

	// Extend lineHeights slice if needed
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Incremental update: only re-render the dirty window
	if wb.dirtyIndex >= 0 && wb.dirtyIndex < len(wb.Windows) {
		w := wb.Windows[wb.dirtyIndex]
		// Only render and count lines for visible windows
		if w.Visible {
			w.Render(wb.width, false, wb.styles, wb.borderStyle, wb.cursorStyle)
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
		for i, w := range wb.Windows {
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

// GetWindowStartLine returns the starting line number for a window.
// IMPORTANT: This calls ensureLineHeights() to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowStartLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	start := 0
	for i := range windowIndex {
		start += wb.lineHeights[i]
	}
	return start
}

// GetWindowEndLine returns the ending line number for a window.
// IMPORTANT: This calls ensureLineHeights() to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowEndLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	end := 0
	for i := 0; i <= windowIndex; i++ {
		end += wb.lineHeights[i]
	}
	return end
}

// GetTotalLines returns total lines across all windows.
func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
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

	if len(wb.Windows) == 0 {
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
	endWindow = min(len(wb.Windows)-1, endWindow+bufferWindows)

	var sb strings.Builder
	firstWritten := false
	for i := range wb.Windows {
		// Skip non-visible windows entirely
		if !wb.Windows[i].Visible {
			continue
		}

		if firstWritten {
			sb.WriteString("\n")
		}

		if i >= startWindow && i <= endWindow {
			// Render actual content
			sb.WriteString(wb.Windows[i].Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, wb.cursorStyle))
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
	for i, w := range wb.Windows {
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
	return len(wb.Windows) - 1
}

// RenderWindowContent renders the content of a window (for testing).
func (wb *WindowBuffer) RenderWindowContent(w *Window, innerWidth int) string {
	return w.renderGenericContent(innerWidth, wb.styles, w.Content)
}
