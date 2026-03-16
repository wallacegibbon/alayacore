package terminal

import "charm.land/lipgloss/v2"

// Styles holds all lipgloss styles for the terminal UI
type Styles struct {
	// Output text styles
	Text        lipgloss.Style
	UserInput   lipgloss.Style
	Tool        lipgloss.Style
	ToolContent lipgloss.Style
	Reasoning   lipgloss.Style
	Error       lipgloss.Style
	System      lipgloss.Style
	Prompt      lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffAdd     lipgloss.Style
	DiffSame    lipgloss.Style // dimmed for unchanged lines
	DiffSep     lipgloss.Style // dimmed separator |

	// Display styles
	Input       lipgloss.Style
	Status      lipgloss.Style
	Confirm     lipgloss.Style
	InputBorder lipgloss.Style
}

// RenderBorderedBox renders content with consistent border, padding, and width.
// This ensures all bordered boxes (input, model selector, queue manager) have the same width.
// The width calculation is: borderStyle.Padding(0, 1).Render(innerStyle.Width(width-4).Render(content))
func (s *Styles) RenderBorderedBox(content string, width int, borderColor string, height ...int) string {
	borderStyle := s.InputBorder.
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1)

	innerStyle := s.Input.Width(max(0, width-4))
	if len(height) > 0 {
		innerStyle = innerStyle.Height(height[0])
	}

	return borderStyle.Render(innerStyle.Render(content))
}

// DefaultStyles returns the default styling configuration
func DefaultStyles() *Styles {
	baseStyle := lipgloss.NewStyle()
	return &Styles{
		// Output text styles
		Text:        baseStyle.Foreground(lipgloss.Color("#cdd6f4")).Bold(true),
		UserInput:   baseStyle.Foreground(lipgloss.Color("#89d4fa")).Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color("#f9e2af")),
		ToolContent: baseStyle.Foreground(lipgloss.Color("#6c7086")),
		Reasoning:   baseStyle.Foreground(lipgloss.Color("#6c7086")).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color("#f38ba8")),
		System:      baseStyle.Foreground(lipgloss.Color("#6c7086")),
		Prompt:      baseStyle.Foreground(lipgloss.Color("#89d4fa")).Bold(true),
		DiffRemove:  baseStyle.Foreground(lipgloss.Color("#f38ba8")),
		DiffAdd:     baseStyle.Foreground(lipgloss.Color("#a6e3a1")),
		DiffSame:    baseStyle.Foreground(lipgloss.Color("#6c7086")),
		DiffSep:     baseStyle.Foreground(lipgloss.Color("#6c7086")),

		// Display styles
		Input:       baseStyle,
		Status:      baseStyle.Foreground(lipgloss.Color("#45475a")),
		Confirm:     baseStyle.Foreground(lipgloss.Color("#f38ba8")).Bold(true),
		InputBorder: baseStyle.Border(lipgloss.RoundedBorder()),
	}
}
