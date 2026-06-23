package terminal

// Line wrapping and truncation utilities for window content rendering.
// These functions handle wrapping styled content at display width
// boundaries while preserving ANSI styles across line breaks, and
// display-width-aware truncation with progressive suffix degradation.
//
// Used by Window.renderer.BuildInner, tool.go
// (RenderDiffContent), model_selector.go, help_window.go,
// theme_selector.go, input_component.go, and tests.

import (
	"bytes"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// wrapContent wraps styled content at the given display width, preserving
// ANSI styles across line breaks. Updates the wrapping strategy here to
// change how all window content is wrapped.
func wrapContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Step 1: hard-wrap at character boundaries (like a terminal)
	s = ansi.Hardwrap(s, width, false)
	// Step 2: re-apply ANSI styles after each inserted newline
	var buf bytes.Buffer
	w := lipgloss.NewWrapWriter(&buf)
	defer w.Close()
	_, _ = io.WriteString(w, s) //nolint:errcheck // bytes.Buffer.Write never fails
	return buf.String()
}

// wrapLines wraps content into lines at the given width.
func wrapLines(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := wrapContent(content, width)
	return strings.Split(wrapped, "\n")
}

// appendDeltaToLines incrementally wraps a delta onto existing lines.
func appendDeltaToLines(lines []string, delta string, width int) []string {
	if len(lines) == 0 {
		return wrapLines(delta, width)
	}
	if width <= 0 {
		lines[len(lines)-1] += delta
		return lines
	}

	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlines(lines, delta, width)
	}

	// Append to last line and rewrap
	lastLine := lines[len(lines)-1]
	combined := lastLine + delta
	newLines := wrapLines(combined, width)
	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	parts := strings.Split(delta, "\n")
	for i, part := range parts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastIdx := len(lines) - 1
				combined := lines[lastIdx] + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:lastIdx], newLines...)
			}
		} else {
			lines = append(lines, wrapLines(part, width)...)
		}
	}
	return lines
}

// styleMultiline applies a style to each line of text.
func styleMultiline(content string, style lipgloss.Style) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// truncateWithSuffix truncates content to fit within maxWidth, using a
// progressively shorter suffix as space shrinks: "...", "..", ".", or just "."
// for a single character — indicating content exists but is too narrow.
func truncateWithSuffix(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if maxWidth == 1 {
		return "."
	}
	truncated := ansi.Hardwrap(content, maxWidth, false)
	if truncated == content {
		return content
	}

	var suffix string
	switch {
	case maxWidth >= 4:
		suffix = "..."
	case maxWidth == 3:
		suffix = ".."
	case maxWidth == 2:
		suffix = "."
	}

	inner := ansi.Hardwrap(content, max(1, maxWidth-lipgloss.Width(suffix)), false)
	return strings.SplitN(inner, "\n", 2)[0] + suffix
}
