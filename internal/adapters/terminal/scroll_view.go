package terminal

// ScrollView is a vertically scrollable viewport that replaces
// Bubbles' viewport component. It provides:
//   - Set content as a string (lines separated by \n)
//   - Vertical scrolling (scroll up/down, goto top/bottom)
//   - YOffset management
//   - Height/width control
//
// It does NOT support soft wrapping, gutters, highlights, or
// horizontal scrolling — features AlayaCore doesn't use.

import (
	"strings"
)

// ScrollView is a simple scrollable viewport.
type ScrollView struct {
	width   int
	height  int
	yOffset int
	content string
	lines   []string // cached split of content
}

// NewScrollView creates a new ScrollView with the given dimensions.
func NewScrollView(width, height int) *ScrollView {
	return &ScrollView{
		width:   max(0, width),
		height:  max(0, height),
		yOffset: 0,
	}
}

// SetWidth sets the viewport width (unused in rendering, kept for API compat).
func (m *ScrollView) SetWidth(w int) {
	m.width = max(0, w)
}

// SetHeight sets the viewport height.
func (m *ScrollView) SetHeight(h int) {
	m.height = max(0, h)
	m.clampYOffset()
}

// Height returns the viewport height.
func (m *ScrollView) Height() int {
	return m.height
}

// SetContent sets the content to display. Content is split by \n into lines.
func (m *ScrollView) SetContent(s string) {
	m.content = s
	m.lines = strings.Split(s, "\n")
	m.clampYOffset()
}

// YOffset returns the current vertical scroll position.
func (m *ScrollView) YOffset() int {
	return m.yOffset
}

// SetYOffset sets the vertical scroll position.
func (m *ScrollView) SetYOffset(y int) {
	m.yOffset = max(0, y)
	m.clampYOffset()
}

// ScrollDown scrolls down by n lines.
func (m *ScrollView) ScrollDown(n int) {
	m.yOffset += n
	m.clampYOffset()
}

// ScrollUp scrolls up by n lines.
func (m *ScrollView) ScrollUp(n int) {
	m.yOffset -= n
	m.clampYOffset()
}

// GotoBottom scrolls to the bottom of the content.
func (m *ScrollView) GotoBottom() {
	m.yOffset = m.maxYOffset()
}

// GotoTop scrolls to the top of the content.
func (m *ScrollView) GotoTop() {
	m.yOffset = 0
}

// AtBottom returns whether the viewport is at the bottom.
func (m *ScrollView) AtBottom() bool {
	return m.yOffset >= m.maxYOffset()
}

// AtTop returns whether the viewport is at the top.
func (m *ScrollView) AtTop() bool {
	return m.yOffset <= 0
}

// PastBottom returns whether the viewport is scrolled past the last line.
func (m *ScrollView) PastBottom() bool {
	return m.yOffset > m.maxYOffset()
}

// View returns the rendered content (visible portion as a string),
// padded with empty lines to fill the viewport height.
func (m *ScrollView) View() string {
	if m.height <= 0 {
		return ""
	}

	start := m.yOffset
	end := min(start+m.height, len(m.lines))

	// Build visible lines
	var visible []string
	if start < len(m.lines) {
		visible = m.lines[start:end]
	}

	// Pad with empty lines to fill viewport height, so content below
	// (input box, status bar) stays at the bottom of the screen.
	for len(visible) < m.height {
		visible = append(visible, "")
	}

	return strings.Join(visible, "\n")
}

// maxYOffset returns the maximum valid Y offset.
func (m *ScrollView) maxYOffset() int {
	return max(0, len(m.lines)-m.height)
}

// clampYOffset ensures Y offset is within valid bounds.
func (m *ScrollView) clampYOffset() {
	m.yOffset = clampInt(m.yOffset, 0, max(0, len(m.lines)-m.height))
}

// clampInt clamps v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if hi < lo {
		lo, hi = hi, lo
	}
	return min(hi, max(lo, v))
}
