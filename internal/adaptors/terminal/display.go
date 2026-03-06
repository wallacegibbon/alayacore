package terminal

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/coreclaw/internal/adaptors/common"
)

// DisplayMsg represents messages specific to the display component
type DisplayMsg struct {
	Type    string
	Content string
}

// DisplayModel handles the main content viewport
type DisplayModel struct {
	viewport         viewport.Model
	userScrolledAway bool
	showingWelcome   bool
	welcomeText      string
	windowBuffer     *WindowBuffer
	styles           *Styles
	width            int
	height           int
	windowCursor     int // index of the currently selected window (-1 means no selection)
}

// NewDisplayModel creates a new display model
func NewDisplayModel(windowBuffer *WindowBuffer, styles *Styles) DisplayModel {
	coloredWelcome := colorizeWelcomeText(common.WelcomeText)
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SetContent(coloredWelcome)

	return DisplayModel{
		viewport:       vp,
		showingWelcome: true,
		welcomeText:    coloredWelcome,
		windowBuffer:   windowBuffer,
		styles:         styles,
		width:          80,
		height:         20,
		windowCursor:   -1,
	}
}

// Init initializes the display
func (m DisplayModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the display
func (m DisplayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.viewport.SetWidth(max(0, msg.Width))
		m.centerWelcomeText()
	case DisplayMsg:
		switch msg.Type {
		case "content_update":
			m.updateContent()
		case "scroll_down":
			m.scrollDown(1)
		case "scroll_up":
			m.viewport.ScrollUp(1)
			m.userScrolledAway = true
		case "goto_bottom":
			m.viewport.GotoBottom()
			m.userScrolledAway = false
		case "goto_top":
			m.viewport.GotoTop()
			m.userScrolledAway = true
		case "scroll_half_down":
			m.scrollDown(m.viewport.Height() / 2)
		case "scroll_half_up":
			m.viewport.ScrollUp(m.viewport.Height() / 2)
			m.userScrolledAway = true
		}
	}
	return m, nil
}

// View renders the display
func (m DisplayModel) View() tea.View {
	return tea.NewView(m.viewport.View())
}

// SetHeight sets the viewport height
func (m *DisplayModel) SetHeight(height int) {
	m.height = height
	m.viewport.SetHeight(max(0, height))
}

// GetHeight returns the current viewport height
func (m DisplayModel) GetHeight() int {
	return m.viewport.Height()
}

// SetWidth sets the viewport width
func (m *DisplayModel) SetWidth(width int) {
	m.width = width
	m.viewport.SetWidth(max(0, width))
}

// YOffset returns the current scroll position
func (m DisplayModel) YOffset() int {
	return m.viewport.YOffset()
}

// updateContent updates the viewport content from the window buffer
func (m *DisplayModel) updateContent() {
	newContent := m.windowBuffer.GetAll(m.windowCursor)

	if m.showingWelcome {
		if newContent != "" && newContent != m.welcomeText {
			m.showingWelcome = false
		} else {
			return
		}
	}

	m.viewport.SetContent(newContent)
	if !m.userScrolledAway {
		m.viewport.GotoBottom()
	}
}

// scrollDown scrolls the display and updates userScrolledAway
func (m *DisplayModel) scrollDown(lines int) {
	m.viewport.ScrollDown(lines)
	m.userScrolledAway = !m.viewport.AtBottom()
}

// centerWelcomeText centers the welcome text in the viewport
func (m *DisplayModel) centerWelcomeText() {
	width := m.viewport.Width()
	height := m.viewport.Height()
	if width == 0 || height == 0 {
		return
	}

	if !m.showingWelcome {
		return
	}

	lines := strings.Split(m.welcomeText, "\n")
	maxWidth := 0
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}

	lineCount := len(lines)
	topPadding := max(0, (height-lineCount)/2)

	centeredLines := make([]string, 0, len(lines)+topPadding)
	if maxWidth < width {
		padding := (width - maxWidth) / 2
		for _, line := range lines {
			centeredLines = append(centeredLines, strings.Repeat(" ", padding)+line)
		}
	} else {
		centeredLines = append(centeredLines, lines...)
	}

	for range topPadding {
		centeredLines = append([]string{""}, centeredLines...)
	}

	m.viewport.SetContent(strings.Join(centeredLines, "\n"))
}

// ClearWelcome clears the welcome screen
func (m *DisplayModel) ClearWelcome() {
	m.showingWelcome = false
}

// IsShowingWelcome returns whether welcome is being shown
func (m DisplayModel) IsShowingWelcome() bool {
	return m.showingWelcome
}

// AtBottom returns whether viewport is at bottom
func (m DisplayModel) AtBottom() bool {
	return m.viewport.AtBottom()
}

// ScrollUp scrolls up by lines
func (m *DisplayModel) ScrollUp(lines int) {
	m.viewport.ScrollUp(lines)
	m.userScrolledAway = true
}

// GotoBottom goes to bottom
func (m *DisplayModel) GotoBottom() {
	m.viewport.GotoBottom()
	m.userScrolledAway = false
}

// GotoTop goes to top
func (m *DisplayModel) GotoTop() {
	m.viewport.GotoTop()
	m.userScrolledAway = true
}

// UpdateHeightForTodos adjusts height based on todo visibility
func (m *DisplayModel) UpdateHeightForTodos(totalHeight int, todoCount int) {
	height := totalHeight - 4 // Subtract input (3) + status (1)
	if todoCount > 0 {
		height -= (1 + todoCount + 2) // header + items + borders
	}

	newHeight := max(0, height)
	oldHeight := m.viewport.Height()

	if oldHeight != newHeight {
		rawContent := m.windowBuffer.GetAll(m.windowCursor)
		totalLines := max(1, strings.Count(rawContent, "\n")+1)

		topLine := m.viewport.YOffset()
		var newTopLine int

		if m.userScrolledAway {
			newTopLine = topLine
		} else {
			bottomLine := topLine + oldHeight - 1
			newTopLine = bottomLine - newHeight + 1
		}

		maxTopLine := max(0, totalLines-newHeight)
		if newTopLine > maxTopLine {
			newTopLine = maxTopLine
		}
		if newTopLine < 0 {
			newTopLine = 0
		}

		m.viewport.SetHeight(newHeight)
		m.viewport.SetYOffset(newTopLine)
	} else {
		m.viewport.SetHeight(newHeight)
	}

	m.updateContent()
}

var _ tea.Model = (*DisplayModel)(nil)

// GetWindowCursor returns the current window cursor index (-1 if none).
func (m *DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// SetWindowCursor sets the window cursor to a specific index.
// Pass -1 to deselect all windows.
func (m *DisplayModel) SetWindowCursor(index int) {
	windowCount := m.windowBuffer.GetWindowCount()
	if index < -1 {
		index = -1
	} else if index >= windowCount {
		index = windowCount - 1
	}
	m.windowCursor = index
}

// MoveWindowCursorDown moves the window cursor down by one window.
func (m *DisplayModel) MoveWindowCursorDown() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return
	}
	if m.windowCursor < 0 || m.windowCursor >= windowCount-1 {
		m.windowCursor = windowCount - 1
	} else {
		m.windowCursor++
	}
}

// MoveWindowCursorUp moves the window cursor up by one window.
func (m *DisplayModel) MoveWindowCursorUp() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return
	}
	if m.windowCursor <= 0 {
		m.windowCursor = 0
	} else {
		m.windowCursor--
	}
}

// EnsureCursorVisible scrolls the viewport to make the cursor window visible.
func (m *DisplayModel) EnsureCursorVisible() {
	if m.windowCursor < 0 {
		return
	}

	startLine := m.windowBuffer.GetWindowStartLine(m.windowCursor)
	endLine := m.windowBuffer.GetWindowEndLine(m.windowCursor)

	viewportTop := m.viewport.YOffset()
	viewportHeight := m.viewport.Height()
	viewportBottom := viewportTop + viewportHeight

	// If window is above viewport, scroll up
	if startLine < viewportTop {
		m.viewport.SetYOffset(startLine)
		return
	}

	// If window is below viewport, scroll down
	if endLine > viewportBottom {
		newTop := endLine - viewportHeight
		m.viewport.SetYOffset(newTop)
	}
}

// SetCursorToLastWindow sets the cursor to the last window.
func (m *DisplayModel) SetCursorToLastWindow() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
	} else {
		m.windowCursor = windowCount - 1
	}
}
