package terminal

// Overlay rendering for selectors.
// Shared logic for positioning overlay content centered horizontally with
// a consistent bottom alignment.

import (
	"charm.land/lipgloss/v2"
)

// renderOverlay positions a content box centered horizontally, with its bottom
// edge aligned at a consistent vertical position.
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int) string {
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x := max(0, (screenWidth-boxWidth)/2)

	// Align the bottom of all overlays at 60% down the terminal
	bottomY := screenHeight * 3 / 5
	y := max(0, bottomY-boxHeight)

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(box).X(x).Y(y).Z(0),
	)
	return c.Render()
}
