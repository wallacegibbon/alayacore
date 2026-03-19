package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// GetAll returns the concatenated rendered windows as a single string.
// Uses virtual rendering when viewportHeight > 0, otherwise falls back to full render.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Use virtual rendering if enabled
	if wb.viewportHeight > 0 {
		return wb.getVirtualRender(cursorIndex)
	}

	// Fallback: full render (for backwards compatibility)
	if wb.dirtyIndex != -1 {
		if wb.dirtyIndex == fullRebuild {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.dirtyIndex)
		}
		wb.dirtyIndex = -1
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
		styled := wb.renderAndCacheWindow(i, w)
		sb.WriteString(styled)
	}
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}
	wb.cachedRender = sb.String()
}

// rebuildOneWindow re-renders only the window at idx and updates the full cached string.
func (wb *WindowBuffer) rebuildOneWindow(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	// Ensure lineHeights has right length (new window case)
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Re-render the dirty window (don't use totalLines from renderAndCacheWindow)
	styled := wb.renderAndCacheWindow(idx, w)
	wb.lineHeights[idx] = strings.Count(styled, "\n") + 1

	// Rebuild totalLines from all lineHeights
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}

	// Rebuild cachedRender by concatenating: [before] + [new] + [after]
	var sb strings.Builder
	for i := 0; i < len(wb.Windows); i++ {
		if i > 0 {
			sb.WriteString("\n")
		}
		if i == idx {
			sb.WriteString(styled)
		} else {
			sb.WriteString(wb.Windows[i].cachedRender)
		}
	}
	wb.cachedRender = sb.String()
}

// renderAndCacheWindow renders a window, updates its cache and lineHeights[i], returns styled string.
// Stores cachedInnerContent for cursor border swap (avoid re-calling renderWindowContent).
func (wb *WindowBuffer) renderAndCacheWindow(i int, w *Window) string {
	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)
	styled := w.Style.Width(wb.width).Render(innerContent)
	lineCount := strings.Count(styled, "\n") + 1

	if i < len(wb.lineHeights) {
		wb.lineHeights[i] = lineCount
	}
	w.cachedRender = styled
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped
	return styled
}

// renderWithCursor renders all windows with cursor highlighting on the specified window.
// For the cursor window: reuse cachedInnerContent, only swap border style (no lipgloss.Wrap).
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Non-cursor window: use cached render if valid
		if i != cursorIndex {
			// Check cache validity
			// For diff windows: check Wrapped state
			// For content windows: check Content length
			cacheValid := w.cachedRender != "" && w.cachedWidth == wb.width &&
				(w.IsDiffWindow() && w.Wrapped == w.lastWrapped || !w.IsDiffWindow() && len(w.Content) == w.lastContentLen)
			if cacheValid {
				sb.WriteString(w.cachedRender)
				continue
			}
			// Fallback: re-render and cache
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := w.Style.Width(wb.width).Render(innerContent)
			w.cachedRender = styled
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			w.lastWrapped = w.Wrapped
			sb.WriteString(styled)
			continue
		}

		// Cursor window: border swap - reuse cachedInnerContent, avoid renderWindowContent
		// Check cache validity
		cacheValid := w.cachedInnerContent != "" && w.cachedWidth == wb.width &&
			(w.IsDiffWindow() && w.Wrapped == w.lastWrapped || !w.IsDiffWindow() && len(w.Content) == w.lastContentLen)
		if cacheValid {
			sb.WriteString(wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent))
		} else {
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
			w.cachedRender = w.Style.Width(wb.width).Render(innerContent) // cache dimmed for next time
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			w.lastWrapped = w.Wrapped
			sb.WriteString(styled)
		}
	}
	return sb.String()
}

// renderWindowContent renders the content of a window (wrapping, truncation for wrapped mode)
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	// Handle diff windows
	if w.IsDiffWindow() {
		// Render diff content
		fullContent := wb.renderDiffContent(w.Diff, innerWidth, w.Status)

		// Apply folding if window is wrapped
		if w.Wrapped {
			lines := strings.Split(fullContent, "\n")
			if len(lines) > 5 {
				// Show: first line, tricolon separator, last 3 lines (5 lines total)
				firstLine := lines[0]
				lastThreeLines := lines[len(lines)-3:]

				// Create full-width tricolon separator with border color
				wrapIndicator := lipgloss.NewStyle().
					Foreground(wb.styles.ColorBase).
					Render(strings.Repeat("⁝", innerWidth))

				// Show first line, separator, last 3 lines
				return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
			}
		}
		return fullContent
	}

	// Build content with optional status indicator
	content := w.Content
	if w.Tag == stream.TagFunctionNotify {
		// Tool windows always have a status indicator
		var indicator string
		if w.Status == "success" {
			// Green filled dot
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorSuccess).
				Render("• ")
		} else if w.Status == "error" {
			// Red filled dot
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorError).
				Render("• ")
		} else if w.Status == "pending" {
			// Dimmed filled dot for pending
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorDim).
				Render("• ")
		} else {
			// Default: dimmed hollow dot (for loaded sessions without status)
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorDim).
				Render("· ")
		}
		content = indicator + content
	}

	if w.Wrapped {
		// In wrapped mode, show up to 5 lines
		wrappedContent := lipgloss.Wrap(content, innerWidth, " ")

		// Check if content spans more than 5 lines (needs truncation)
		lines := strings.Split(wrappedContent, "\n")
		if len(lines) > 5 {
			// Show: first line, tricolon separator, last 3 lines (5 lines total)
			firstLine := lines[0]
			lastThreeLines := lines[len(lines)-3:]

			// Create full-width tricolon separator with border color
			wrapIndicator := lipgloss.NewStyle().
				Foreground(wb.styles.ColorBase).
				Render(strings.Repeat("⁝", innerWidth))

			// Show first line, separator, last 3 lines
			return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
		}
		// Content fits in 5 lines or less, just show wrapped content
		return wrappedContent
	}
	return lipgloss.Wrap(content, innerWidth, " ")
}
