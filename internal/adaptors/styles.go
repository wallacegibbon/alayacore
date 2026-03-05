package adaptors

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

	// Display styles
	Input  lipgloss.Style
	Status lipgloss.Style

	// Todo styles
	TodoHeader  lipgloss.Style
	Pending     lipgloss.Style
	InProgress  lipgloss.Style
	Completed   lipgloss.Style
	Confirm     lipgloss.Style
	TodoBorder  lipgloss.Style
	InputBorder lipgloss.Style
}

// DefaultStyles returns the default styling configuration
func DefaultStyles() *Styles {
	baseStyle := lipgloss.NewStyle()
	return &Styles{
		// Output text styles
		Text:        baseStyle.Foreground(lipgloss.Color("#cdd6f4")).Bold(true),
		UserInput:   baseStyle.Foreground(lipgloss.Color("#89d4fa")).Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color("#f9e2af")),
		ToolContent: baseStyle.Foreground(lipgloss.Color("#89d4fa")),
		Reasoning:   baseStyle.Foreground(lipgloss.Color("#6c7086")).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color("#f38ba8")),
		System:      baseStyle.Foreground(lipgloss.Color("#6c7086")),
		Prompt:      baseStyle.Foreground(lipgloss.Color("#a6e3a1")).Bold(true),

		// Display styles
		Input:  baseStyle,
		Status: baseStyle.Foreground(lipgloss.Color("#45475a")),

		// Todo styles
		TodoHeader:  baseStyle.Foreground(lipgloss.Color("#f9e2af")),
		Pending:     baseStyle.Foreground(lipgloss.Color("#6c7086")),
		InProgress:  baseStyle.Foreground(lipgloss.Color("#a6e3a1")).Bold(true),
		Completed:   baseStyle.Foreground(lipgloss.Color("#6a8a6a")),
		Confirm:     baseStyle.Foreground(lipgloss.Color("#f38ba8")).Bold(true),
		TodoBorder:  baseStyle.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#6c7086")),
		InputBorder: baseStyle.Border(lipgloss.RoundedBorder()),
	}
}
