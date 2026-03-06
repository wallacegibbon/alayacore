package terminal

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     byte           // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
}

// WindowBuffer holds a sequence of windows in order of creation.
type WindowBuffer struct {
	mu          sync.Mutex
	Windows     []*Window
	idIndex     map[string]int
	width       int
	borderStyle lipgloss.Style
	cursorStyle lipgloss.Style
	lineHeights []int // cached line heights for each window (after rendering)
	totalLines  int   // total lines across all windows
}

// NewWindowBuffer creates a new window buffer with given width.
func NewWindowBuffer(width int) *WindowBuffer {
	// Dimmed border: rounded border with subtle color
	dimmedBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6c7086")).
		Padding(0, 1)

	// Highlighted border for cursor
	cursorBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#89b4fa")).
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
	wb.width = width
}

// AppendOrUpdate adds content to an existing window identified by id,
// or creates a new window if id not found.
// tag is the TLV tag, content is the styled string (already styled by writeColored).
func (wb *WindowBuffer) AppendOrUpdate(id string, tag byte, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		// Append to existing window
		window := wb.Windows[idx]
		window.Content += content
		return
	}
	// Create new window
	window := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Style:   wb.borderStyle,
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
}

// GetAll returns the concatenated rendered windows as a single string.
// Each window is rendered with its border and padded to the current width.
// If cursorIndex >= 0, that window is highlighted with cursor border style.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	var sb strings.Builder
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		innerWidth := max(0, wb.width-4)
		wrapped := lipgloss.Wrap(w.Content, innerWidth, " ")

		// Use cursor style for highlighted window
		style := w.Style
		if i == cursorIndex {
			style = wb.cursorStyle
		}
		styled := style.Width(wb.width).Render(wrapped)
		sb.WriteString(styled)

		// Track line height for this window
		lineCount := strings.Count(styled, "\n") + 1
		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount
	}
	return sb.String()
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
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
	for i := 0; i < windowIndex; i++ {
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
