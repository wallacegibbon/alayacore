package terminal

// Overlay rendering for selectors.
// Shared logic for centering overlay content above the input area.

import (
	"charm.land/lipgloss/v2"
)

// renderOverlay positions a content box centered horizontally and above the
// input area at the bottom of the screen.
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int) string {
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x := max(0, (screenWidth-boxWidth)/2)

	// Position above the input box (input box is ~3 lines, status bar is 1 line)
	inputAreaHeight := LayoutGap // input box (3 lines) + status bar (1 line)
	y := max(0, screenHeight-boxHeight-inputAreaHeight)

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return c.Render()
}
