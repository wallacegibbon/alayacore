package terminal

// Overlay rendering for selectors.
// Shared logic for positioning overlay content centered horizontally with
// a consistent bottom alignment.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderOverlay positions a content box centered horizontally, with its bottom
// edge aligned at a consistent vertical position. Visual separator bands are
// added above and below the box to distinguish it from the base content.
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int) string {
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x := max(0, (screenWidth-boxWidth)/2)

	// Align the bottom of all overlays at 60% down the terminal
	bottomY := screenHeight * 3 / 5
	y := max(0, bottomY-boxHeight)

	// Build visual separator lines above and below the overlay.
	// Light shade characters create a subtle dimming effect that
	// visually separates the overlay from the content behind.
	sep := strings.Repeat("░", boxWidth)
	paddedBox := "\n" + sep + "\n" + box + "\n" + sep + "\n"

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(paddedBox).X(x).Y(y-1).Z(0),
	)
	return c.Render()
}
