package terminal

// Tool output parsing and rendering.
// Consolidates all tool-related logic: types, parsing, and rendering.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ============================================================================
// Tool Status
// ============================================================================

// ToolStatus represents the execution status of a tool window.
type ToolStatus int

const (
	ToolStatusNone    ToolStatus = iota // No status indicator (dimmed hollow dot)
	ToolStatusSuccess                   // Tool completed successfully (green dot)
	ToolStatusError                     // Tool failed (red dot)
	ToolStatusPending                   // Tool is running (dimmed dot)
)

// Indicator returns the styled status indicator string.
func (s ToolStatus) Indicator(styles *Styles) string {
	switch s {
	case ToolStatusSuccess:
		return lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("• ")
	case ToolStatusError:
		return lipgloss.NewStyle().Foreground(styles.ColorError).Render("• ")
	case ToolStatusPending:
		return lipgloss.NewStyle().Foreground(styles.ColorDim).Render("• ")
	default:
		return lipgloss.NewStyle().Foreground(styles.ColorDim).Render("· ")
	}
}

// ParseToolStatus converts a status string to ToolStatus.
func ParseToolStatus(status string) ToolStatus {
	switch status {
	case "success":
		return ToolStatusSuccess
	case "error":
		return ToolStatusError
	case "pending":
		return ToolStatusPending
	default:
		return ToolStatusNone
	}
}

// ============================================================================
// Stream ID Parsing (for text deltas and status updates)
// ============================================================================

// ParseStreamID extracts stream ID prefix from value.
// Format: "[:id:]content". Returns id, content, true if prefix found.
func ParseStreamID(value string) (id string, content string, ok bool) {
	const prefixStart = "[:"
	const prefixEnd = ":]"
	if !strings.HasPrefix(value, prefixStart) {
		return "", value, false
	}
	endIdx := strings.Index(value, prefixEnd)
	if endIdx == -1 {
		return "", value, false
	}
	id = value[len(prefixStart):endIdx]
	content = value[endIdx+len(prefixEnd):]
	return id, content, true
}

// ============================================================================
// Rendering
// ============================================================================

// ColorizeTool applies tool-specific styling to tool output.
func ColorizeTool(value string, styles *Styles) string {
	// Expand tabs BEFORE styling to ensure correct column counting
	value = expandTabs(value)

	lines := strings.Split(value, "\n")
	if len(lines) == 1 {
		return colorizeSingleLineTool(value, styles)
	}
	return colorizeMultiLineTool(lines, styles)
}

func colorizeSingleLineTool(value string, styles *Styles) string {
	colonIdx := strings.Index(value, ":")
	if colonIdx > 0 {
		toolName := value[:colonIdx]
		rest := value[colonIdx:]
		return styles.Tool.Render(toolName) + styles.ToolContent.Render(rest)
	}
	return styles.Tool.Render(value)
}

func colorizeMultiLineTool(lines []string, styles *Styles) string {
	var result strings.Builder
	firstLine := lines[0]
	colonIdx := strings.Index(firstLine, ":")

	if colonIdx > 0 {
		toolName := firstLine[:colonIdx]
		restFirst := firstLine[colonIdx:]
		result.WriteString(styles.Tool.Render(toolName))
		result.WriteString(styles.ToolContent.Render(restFirst))
	} else {
		result.WriteString(styles.Tool.Render(firstLine))
	}

	for _, line := range lines[1:] {
		result.WriteString("\n")
		// Fallback for other lines
		switch {
		case strings.HasPrefix(line, "- "):
			result.WriteString(styles.DiffRemove.Render(line))
		case strings.HasPrefix(line, "+ "):
			result.WriteString(styles.DiffAdd.Render(line))
		default:
			result.WriteString(styles.ToolContent.Render(line))
		}
	}
	return result.String()
}

// RenderToolContent renders a tool window with header + text content.
// Used for write_file, read_file, posix_shell, etc. where output should be styled as text.
func RenderToolContent(content string, status ToolStatus, styles *Styles) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// Header line: "tool_name: args"
			// Colorize the tool name and the rest
			result = append(result, status.Indicator(styles)+ColorizeTool(line, styles))
			continue
		}
		// Content lines - apply text style
		result = append(result, styles.Text.Render(expandTabs(line)))
	}

	return strings.Join(result, "\n")
}

// RenderDiffContent renders a diff window from its raw Content.
// The Content already has `- `, `+ `, `  ` prefixes - we just apply colors.
func RenderDiffContent(content string, status ToolStatus, styles *Styles) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// Header line: "edit_file: /path"
			// Need to re-render with status indicator
			path := strings.TrimPrefix(line, "edit_file: ")
			result = append(result, status.Indicator(styles)+styles.Tool.Render("edit_file: ")+styles.ToolContent.Render(path))
			continue
		}
		if line == "" {
			continue
		}

		// Apply color based on prefix
		switch {
		case strings.HasPrefix(line, "- "):
			result = append(result, styles.DiffRemove.Render(line))
		case strings.HasPrefix(line, "+ "):
			result = append(result, styles.DiffAdd.Render(line))
		default:
			// Unchanged line (starts with "  ") or anything else
			result = append(result, styles.Text.Render(line))
		}
	}

	return strings.Join(result, "\n")
}

// expandTabs converts tabs to spaces, treating tabs as 8-space width.
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		} else if r == '\n' {
			result.WriteRune(r)
			col = 0
		} else {
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
}
