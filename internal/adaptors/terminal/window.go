package terminal

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/coreclaw/internal/stream"
)

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     byte           // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
	Wrapped bool           // true if window is in wrapped (3-row) mode

	// Cached rendering state
	lastContentLen int    // length of content when last rendered (for quick change detection)
	cachedRender   string // cached rendered output
}

// WindowBuffer holds a sequence of windows in order of creation.
type WindowBuffer struct {
	mu           sync.Mutex
	Windows      []*Window
	idIndex      map[string]int
	width        int
	borderStyle  lipgloss.Style
	cursorStyle  lipgloss.Style
	lineHeights  []int  // cached line heights for each window (after rendering)
	totalLines   int    // total lines across all windows
	dirty        bool   // true if content has changed and needs re-render
	cachedRender string // cached full render of all windows
}

// NewWindowBuffer creates a new window buffer with given width.
func NewWindowBuffer(width int) *WindowBuffer {
	// Dimmed border: rounded border with invisible color (matches background)
	dimmedBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#1e1e2e")).
		Padding(0, 1)

	// Highlighted border for cursor
	cursorBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#fab387")).
		Padding(0, 1)

	return &WindowBuffer{
		Windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		borderStyle: dimmedBorder,
		cursorStyle: cursorBorder,
		lineHeights: []int{},
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
		wb.dirty = true
	}
}

// AppendOrUpdate adds content to an existing window identified by id,
// or creates a new window if id not found.
// tag is the TLV tag, content is the styled string (already styled by writeColored).
// Reasoning windows are wrapped by default.
func (wb *WindowBuffer) AppendOrUpdate(id string, tag byte, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		// Append to existing window
		window := wb.Windows[idx]
		window.Content += content
		wb.dirty = true
		return
	}
	// Create new window - reasoning windows are wrapped by default
	window := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Style:   wb.borderStyle,
		Wrapped: tag == stream.TagReasoning,
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.dirty = true
}

// GetAll returns the concatenated rendered windows as a single string.
// Each window is rendered with its border and padded to the current width.
// If cursorIndex >= 0, that window is highlighted with cursor border style.
// If a window is in Wrapped mode, it shows only the last 3 lines of content.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Check if we need to re-render
	needsRender := wb.dirty
	if !needsRender {
		// Check if any window content has changed (quick length check)
		for _, w := range wb.Windows {
			if len(w.Content) != w.lastContentLen {
				needsRender = true
				break
			}
		}
	}

	if needsRender {
		wb.rebuildCache()
		wb.dirty = false
	}

	// If no cursor or cursor out of range, return cached render
	if cursorIndex < 0 || cursorIndex >= len(wb.Windows) {
		return wb.cachedRender
	}

	// Cursor is active - rebuild with cursor highlighting on just that window
	// We use the cached wrapped content but apply different border style
	return wb.renderWithCursor(cursorIndex)
}

// rebuildCache rebuilds the cached render for all windows (without cursor)
func (wb *WindowBuffer) rebuildCache() {
	var sb strings.Builder
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		innerWidth := max(0, wb.width-4)
		contentToRender := wb.renderWindowContent(w, innerWidth)

		styled := w.Style.Width(wb.width).Render(contentToRender)
		sb.WriteString(styled)

		// Track line height and cache rendered content
		lineCount := strings.Count(styled, "\n") + 1
		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount
		w.cachedRender = styled
		w.lastContentLen = len(w.Content)
	}
	wb.cachedRender = sb.String()
}

// renderWithCursor renders all windows with cursor highlighting on the specified window
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Use cached render for non-cursor windows
		if i != cursorIndex && w.cachedRender != "" && len(w.Content) == w.lastContentLen {
			sb.WriteString(w.cachedRender)
			continue
		}

		// Render cursor window (or uncached window) with appropriate style
		innerWidth := max(0, wb.width-4)
		contentToRender := wb.renderWindowContent(w, innerWidth)

		style := w.Style
		if i == cursorIndex {
			style = wb.cursorStyle
		}
		styled := style.Width(wb.width).Render(contentToRender)
		sb.WriteString(styled)
	}
	return sb.String()
}

// renderWindowContent renders the content of a window (wrapping, truncation for wrapped mode)
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	if w.Wrapped {
		// In wrapped mode, show only last 3 visual lines
		// Grab last 3 logical lines (enough since wrapping only expands, never merges)
		lastLinesRaw := getLastLines(w.Content, 3)
		wrappedContent := lipgloss.Wrap(lastLinesRaw, innerWidth, " ")

		// Show wrap indicator if original content was truncated
		if len(lastLinesRaw) < len(w.Content) {
			contentToRender := getLastLines(wrappedContent, 3)
			wrapIndicator := lipgloss.NewStyle().
				Background(lipgloss.Color("#45475a")).
				Render(" Wrapped - Space to expand ")
			if contentToRender != "" {
				return wrapIndicator + "\n" + contentToRender
			}
			return wrapIndicator
		}
		// Content fits in 3 logical lines, just show wrapped content
		return wrappedContent
	}
	return lipgloss.Wrap(w.Content, innerWidth, " ")
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.cachedRender = ""
	wb.dirty = true
}

// GetWindowCount returns the number of windows.
func (wb *WindowBuffer) GetWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.Windows)
}

// GetWindowStartLine returns the starting line number (0-indexed) for the window at given index.
func (wb *WindowBuffer) GetWindowStartLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	startLine := 0
	for i := range windowIndex {
		startLine += wb.lineHeights[i]
	}
	return startLine
}

// GetWindowEndLine returns the ending line number (0-indexed, exclusive) for the window at given index.
func (wb *WindowBuffer) GetWindowEndLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	endLine := 0
	for i := 0; i <= windowIndex; i++ {
		endLine += wb.lineHeights[i]
	}
	return endLine
}

// GetTotalLines returns the total number of lines across all windows.
func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.totalLines
}

// ToggleWrap toggles the wrap state of the window at the given index.
// Returns true if toggled successfully, false if index is invalid.
func (wb *WindowBuffer) ToggleWrap(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return false
	}

	wb.Windows[windowIndex].Wrapped = !wb.Windows[windowIndex].Wrapped
	wb.dirty = true
	return true
}

// getLastLines returns the last n lines from an already-wrapped string.
// It finds the nth-to-last newline and returns everything after it.
func getLastLines(wrapped string, n int) string {
	if n <= 0 {
		return ""
	}
	idx := len(wrapped)
	for i := 0; i < n && idx > 0; i++ {
		idx = strings.LastIndex(wrapped[:idx], "\n")
		if idx == -1 {
			return wrapped
		}
	}
	if idx >= 0 && idx < len(wrapped) {
		return wrapped[idx+1:]
	}
	return wrapped
}
