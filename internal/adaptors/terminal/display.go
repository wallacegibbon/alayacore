package terminal

// DisplayModel provides a viewport over WindowBuffer content.
// It manages scrolling, cursor navigation, and auto-follow behavior.

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// DisplayModel holds the viewport over WindowBuffer content.
type DisplayModel struct {
	viewport       viewport.Model
	windowBuffer   *WindowBuffer
	styles         *Styles
	width          int
	height         int
	windowCursor   int
	autoFollow     bool // true on init and after G; cleared by navigation except j/J/L while active
	displayFocused bool
	lastContent    string
}

// NewDisplayModel creates a new display model
func NewDisplayModel(windowBuffer *WindowBuffer, styles *Styles) DisplayModel {
	vp := viewport.New(viewport.WithWidth(DefaultWidth), viewport.WithHeight(DefaultHeight))
	return DisplayModel{
		viewport:       vp,
		windowBuffer:   windowBuffer,
		styles:         styles,
		width:          DefaultWidth,
		height:         DefaultHeight,
		windowCursor:   -1,
		autoFollow:     true,
		displayFocused: false,
	}
}

// Init initializes the display
func (m *DisplayModel) Init() tea.Cmd { return nil }

// Update handles messages for the display
func (m *DisplayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if windowMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = windowMsg.Width
		m.viewport.SetWidth(max(0, windowMsg.Width))
	}
	return m, nil
}

// View renders the display
func (m *DisplayModel) View() tea.View {
	return tea.NewView(m.viewport.View())
}

// SetHeight sets the viewport height
func (m *DisplayModel) SetHeight(height int) {
	m.height = height
	m.viewport.SetHeight(max(0, height))
}

// GetHeight returns the current viewport height
func (m *DisplayModel) GetHeight() int {
	return m.viewport.Height()
}

// SetWidth sets the viewport width
func (m *DisplayModel) SetWidth(width int) {
	m.width = width
	m.viewport.SetWidth(max(0, width))
}

// SetDisplayFocused sets whether the display is focused
func (m *DisplayModel) SetDisplayFocused(focused bool) {
	m.displayFocused = focused
}

// SetStyles updates the styles for the display
func (m *DisplayModel) SetStyles(styles *Styles) {
	m.styles = styles
}

// YOffset returns the current scroll position
func (m *DisplayModel) YOffset() int {
	return m.viewport.YOffset()
}

// updateContent updates the viewport content from the window buffer
func (m *DisplayModel) updateContent() {
	cursorIndex := -1
	if m.displayFocused {
		cursorIndex = m.windowCursor
	}

	totalLines := m.windowBuffer.GetTotalLines()
	viewportHeight := m.viewport.Height()

	targetYOffset := m.viewport.YOffset()
	if m.autoFollow && totalLines > viewportHeight {
		targetYOffset = max(0, totalLines-viewportHeight)
	}

	m.windowBuffer.SetViewportPosition(targetYOffset, viewportHeight)

	newContent := m.windowBuffer.GetAll(cursorIndex)
	if newContent == m.lastContent {
		return
	}
	m.lastContent = newContent

	m.viewport.SetContent(newContent)

	if m.autoFollow {
		m.viewport.GotoBottom()
	}
}

// ScrollDown scrolls down by lines
func (m *DisplayModel) ScrollDown(lines int) {
	m.viewport.ScrollDown(lines)
}

// AtBottom returns whether viewport is at bottom
func (m *DisplayModel) AtBottom() bool {
	return m.viewport.AtBottom()
}

// ScrollUp scrolls up by lines
func (m *DisplayModel) ScrollUp(lines int) {
	m.viewport.ScrollUp(lines)
}

// GotoBottom goes to bottom
func (m *DisplayModel) GotoBottom() {
	m.viewport.GotoBottom()
}

// GotoTop goes to top
func (m *DisplayModel) GotoTop() {
	m.viewport.GotoTop()
}

// UpdateHeight sets the viewport height based on total window height
func (m *DisplayModel) UpdateHeight(totalHeight int) {
	m.viewport.SetHeight(max(0, totalHeight-LayoutGap))
	m.updateContent()
}

// shouldFollow returns true when viewport should auto-follow new content
func (m *DisplayModel) shouldFollow() bool {
	return m.autoFollow
}

// GetWindowCursor returns the current window cursor index
func (m *DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// GetCursorWindowContent returns the content of the currently selected window.
// Returns empty string if no window is selected.
func (m *DisplayModel) GetCursorWindowContent() string {
	if m.windowCursor < 0 {
		return ""
	}
	return m.windowBuffer.GetWindowContent(m.windowCursor)
}

// SetWindowCursor sets the window cursor to a specific visible window near the given index.
// If the index points to an invisible window, the nearest visible window is chosen instead.
// This disables auto-follow; only G re-enables it.
func (m *DisplayModel) SetWindowCursor(index int) {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
		m.autoFollow = false
		return
	}
	if index < 0 {
		m.windowCursor = -1
		m.autoFollow = false
		return
	}
	if index >= windowCount {
		index = windowCount - 1
	}
	// If target is invisible, find nearest visible window
	if !m.windowBuffer.Windows[index].Visible {
		// Search forward first, then backward
		for i := index; i < windowCount; i++ {
			if m.windowBuffer.Windows[i].Visible {
				index = i
				goto done
			}
		}
		for i := index; i >= 0; i-- {
			if m.windowBuffer.Windows[i].Visible {
				index = i
				goto done
			}
		}
		// No visible windows at all
		m.windowCursor = -1
		m.autoFollow = false
		return
	}
done:
	if m.windowCursor == index {
		return // cursor unchanged, preserve autoFollow
	}
	m.windowCursor = index
	m.autoFollow = false
}

// MoveWindowCursorDown moves the window cursor down, skipping invisible windows.
// When auto-follow is active this is a no-op: new windows may have been
// appended to the buffer between ticks, making the cursor appear to be
// "not at the last window" even though the user never moved.  Allowing j
// to move in that case would silently jump to a window the user can't see
// and disable auto-follow.  Only the tick handler (SetCursorToLastWindow)
// should advance the cursor while auto-following.
func (m *DisplayModel) MoveWindowCursorDown() bool {
	if m.autoFollow {
		return false
	}
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// If cursor is unset, start at the first visible window
	if m.windowCursor < 0 {
		for i := 0; i < windowCount; i++ {
			if m.windowBuffer.Windows[i].Visible {
				m.windowCursor = i
				m.autoFollow = false
				return true
			}
		}
		return false
	}

	// Step forward to the next visible window
	for i := m.windowCursor + 1; i < windowCount; i++ {
		if m.windowBuffer.Windows[i].Visible {
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

// MoveWindowCursorUp moves the window cursor up, skipping invisible windows.
func (m *DisplayModel) MoveWindowCursorUp() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// If cursor is unset or below the last window, start at the last visible window
	if m.windowCursor < 0 || m.windowCursor >= windowCount {
		for i := windowCount - 1; i >= 0; i-- {
			if m.windowBuffer.Windows[i].Visible {
				if m.windowCursor == i {
					return false // already at this window
				}
				m.windowCursor = i
				m.autoFollow = false
				return true
			}
		}
		return false
	}

	// Step backward to the previous visible window
	for i := m.windowCursor - 1; i >= 0; i-- {
		if m.windowBuffer.Windows[i].Visible {
			if i == m.windowCursor {
				return false // already at this window (defensive check)
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

// MarkUserScrolled disables auto-follow. Called by scroll keys (J/K/Ctrl-D/Ctrl-U).
func (m *DisplayModel) MarkUserScrolled() {
	m.autoFollow = false
}

// EnsureCursorVisible scrolls the viewport only if the cursor window is
// completely off-screen. If any part of the window is already visible, the
// viewport position is left unchanged. The cursor highlight tells the user
// where they are; explicit scroll keys (Ctrl-D, J, etc.) can reveal more.
//
// This avoids viewport jumping on repeated navigation and prevents
// oscillation when a window is taller than the viewport.
func (m *DisplayModel) EnsureCursorVisible() {
	if m.windowCursor < 0 {
		return
	}

	startLine := m.windowBuffer.GetWindowStartLine(m.windowCursor)
	endLine := m.windowBuffer.GetWindowEndLine(m.windowCursor)
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()

	if endLine <= viewportTop {
		// Entirely above — show the bottom edge
		m.viewport.SetYOffset(max(0, endLine-m.viewport.Height()))
	} else if startLine >= viewportBottom {
		// Entirely below — show the top edge
		m.viewport.SetYOffset(startLine)
	}
}

// ScrollCursorToTop scrolls the viewport so the cursor window's start line
// is at the top of the viewport.  This is used when jumping to a specific
// prompt (e.g. via b/f) so the user can see both the prompt and the
// response that follows it.
func (m *DisplayModel) ScrollCursorToTop() {
	if m.windowCursor < 0 {
		return
	}
	startLine := m.windowBuffer.GetWindowStartLine(m.windowCursor)
	m.viewport.SetYOffset(startLine)
}

// ValidateCursor ensures the window cursor is valid and visible.
// Uses partial visibility check to avoid jarring scroll jumps on resize.
func (m *DisplayModel) ValidateCursor() {
	m.ClampCursor()
	if m.windowCursor >= 0 && m.windowBuffer.GetWindowCount() > 0 {
		m.EnsureCursorVisible()
	}
}

// ClampCursor clamps the window cursor to a visible window without scrolling.
// Unlike ValidateCursor, this does not call EnsureCursorVisible, preserving
// the user's scroll position. Use this on resize events where only bounds
// correction is needed.
func (m *DisplayModel) ClampCursor() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
		return
	}
	// Cursor is intentionally unset or negative — normalize to -1
	if m.windowCursor < 0 {
		m.windowCursor = -1
		return
	}
	// Cursor is beyond the last window — clamp to last visible
	if m.windowCursor >= windowCount {
		for i := windowCount - 1; i >= 0; i-- {
			if m.windowBuffer.Windows[i].Visible {
				m.windowCursor = i
				return
			}
		}
		m.windowCursor = -1
		return
	}
	// Cursor points to an invisible window — move to nearest visible one
	if !m.windowBuffer.Windows[m.windowCursor].Visible {
		// Search forward first, then backward
		for i := m.windowCursor + 1; i < windowCount; i++ {
			if m.windowBuffer.Windows[i].Visible {
				m.windowCursor = i
				return
			}
		}
		for i := m.windowCursor - 1; i >= 0; i-- {
			if m.windowBuffer.Windows[i].Visible {
				m.windowCursor = i
				return
			}
		}
		m.windowCursor = -1
	}
}

// SetCursorToLastWindow sets the cursor to the last visible window
func (m *DisplayModel) SetCursorToLastWindow() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
	} else {
		// Find the last visible window
		m.windowCursor = -1
		for i := windowCount - 1; i >= 0; i-- {
			if m.windowBuffer.Windows[i].Visible {
				m.windowCursor = i
				break
			}
		}
		m.autoFollow = true
	}
}

// ToggleWindowFold toggles the fold state of the selected window
func (m *DisplayModel) ToggleWindowFold() bool {
	if m.windowCursor < 0 {
		return false
	}
	return m.windowBuffer.ToggleFold(m.windowCursor)
}

// MoveWindowCursorToTop moves cursor to top visible window
func (m *DisplayModel) MoveWindowCursorToTop() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportTop := m.viewport.YOffset()
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		if (startLine <= viewportTop && endLine > viewportTop) || startLine >= viewportTop {
			if i == m.windowCursor {
				return false // cursor unchanged, preserve autoFollow
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

// MoveWindowCursorToBottom moves cursor to bottom visible window.
// No-op when auto-follow is active (same race as MoveWindowCursorDown).
func (m *DisplayModel) MoveWindowCursorToBottom() bool {
	if m.autoFollow {
		return false
	}
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportBottom := m.viewport.YOffset() + m.viewport.Height()
	for i := windowCount - 1; i >= 0; i-- {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		if (startLine < viewportBottom && endLine >= viewportBottom) || endLine <= viewportBottom {
			if i == m.windowCursor {
				return false
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

// MoveWindowCursorToCenter moves cursor to the window at the visual center of the screen.
// It finds the window that contains the center line of the visible viewport.
// If no window contains the center line, it finds the window closest to the center.
func (m *DisplayModel) MoveWindowCursorToCenter() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// Calculate the center line of the visible viewport
	viewportHeight := m.viewport.Height()
	viewportTop := m.viewport.YOffset()
	viewportCenter := viewportTop + viewportHeight/2

	// First, try to find the window that contains the viewport center line
	// endLine is exclusive, so we use < for the upper bound
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)

		// Check if viewport center line falls within this window
		if viewportCenter >= startLine && viewportCenter < endLine {
			if i == m.windowCursor {
				return false // cursor unchanged, preserve autoFollow
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}

	// If center line falls in a gap (or all windows are above/below center),
	// find the visible window whose center is closest to the viewport center
	var bestWindow int
	bestDistance := -1

	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)

		// Only consider visible windows
		if startLine >= viewportTop+viewportHeight || endLine <= viewportTop {
			continue
		}

		// Calculate window center
		windowCenter := (startLine + endLine) / 2

		// Calculate absolute distance from window center to viewport center
		distance := windowCenter - viewportCenter
		if distance < 0 {
			distance = -distance
		}

		if bestDistance < 0 || distance < bestDistance {
			bestWindow = i
			bestDistance = distance
		}
	}

	if bestDistance >= 0 {
		if bestWindow == m.windowCursor {
			return false // cursor unchanged, preserve autoFollow
		}
		m.windowCursor = bestWindow
		m.autoFollow = false
		return true
	}

	return false
}

// MoveWindowCursorToNextUserPrompt moves the window cursor forward (down) to
// the next window whose tag is TagTextUser ("TU"). Returns false if no such
// window exists below the current cursor.
func (m *DisplayModel) MoveWindowCursorToNextUserPrompt() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// Start searching from the window after the current cursor.
	// If cursor is -1 (none), start from 0.
	start := m.windowCursor + 1
	if start < 0 {
		start = 0
	}

	for i := start; i < windowCount; i++ {
		w := m.windowBuffer.GetWindow(i)
		if w != nil && w.Tag == stream.TagTextUser {
			if i == m.windowCursor {
				return false // cursor unchanged, preserve autoFollow
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

// MoveWindowCursorToPrevUserPrompt moves the window cursor backward (up) to
// the previous window whose tag is TagTextUser ("TU"). Returns false if no
// such window exists above the current cursor.
func (m *DisplayModel) MoveWindowCursorToPrevUserPrompt() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// Start searching from the window before the current cursor.
	// Clamp cursor so we don't go out of bounds.
	start := m.windowCursor - 1
	if start >= windowCount {
		start = windowCount - 1
	}

	for i := start; i >= 0; i-- {
		w := m.windowBuffer.GetWindow(i)
		if w != nil && w.Tag == stream.TagTextUser {
			if i == m.windowCursor {
				return false // cursor unchanged, preserve autoFollow
			}
			m.windowCursor = i
			m.autoFollow = false
			return true
		}
	}
	return false
}

var _ tea.Model = (*DisplayModel)(nil)
