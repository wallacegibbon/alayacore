package terminal

// Theme and styling for the terminal UI.
// The Theme struct, DefaultTheme(), and LoadTheme() now live in
// internal/theme — this file only keeps lipgloss-specific style derivation.

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Styles - Derived Lipgloss Styles
// ============================================================================

// Styles holds all lipgloss styles for the terminal UI.
//
// IMMUTABILITY: Styles is created by NewStyles and never modified after
// construction. When the theme changes, a new Styles instance is created
// and swapped in atomically via atomic.Pointer in outputWriter. Storing
// a pointer obtained from to.styles.Load() and reading its fields is safe
// because the underlying struct is never mutated in-place — SetStyles
// always replaces the entire instance.
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
	Attachment  lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffAdd     lipgloss.Style

	// Display styles
	Input       lipgloss.Style
	Status      lipgloss.Style
	Separator   lipgloss.Style
	Confirm     lipgloss.Style
	InputBorder lipgloss.Style

	// Component-specific colors (exposed as color.Color for dynamic use)
	// Border colors
	BorderFocused color.Color
	BorderBlurred color.Color
	BorderCursor  color.Color

	// Text colors for dynamic use
	ColorAccent  color.Color
	ColorDim     color.Color
	ColorMuted   color.Color
	ColorError   color.Color
	ColorSuccess color.Color
	CursorColor  color.Color

	// Fold indicator character (repeated to form the fold splitter row)
	FoldIndicator string
}

// RenderBorderedBox renders content with consistent border, padding, and width.
// This ensures all bordered boxes (input, model selector, input field) have the same width.
// The width calculation is: borderStyle.Padding(0, 1).Render(innerStyle.Width(width-4).Render(content))
func (s *Styles) RenderBorderedBox(content string, width int, borderColor color.Color, height ...int) string {
	borderStyle := s.InputBorder.
		BorderForeground(borderColor).
		Padding(0, 1)

	innerStyle := s.Input.Width(max(0, width-BorderInnerPadding))
	if len(height) > 0 {
		innerStyle = innerStyle.Height(height[0])
	}

	return borderStyle.Render(innerStyle.Render(content))
}

// NewStyles creates a Styles instance from a Theme
func NewStyles(t *theme.Theme) *Styles {
	baseStyle := lipgloss.NewStyle()
	return &Styles{
		// Output text styles
		Text:        baseStyle.Foreground(lipgloss.Color(t.Text)).Bold(true),
		UserInput:   baseStyle.Foreground(lipgloss.Color(t.Primary)).Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color(t.Warning)),
		ToolContent: baseStyle.Foreground(lipgloss.Color(t.Muted)),
		Reasoning:   baseStyle.Foreground(lipgloss.Color(t.Muted)).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color(t.Error)),
		System:      baseStyle.Foreground(lipgloss.Color(t.Muted)),
		Prompt:      baseStyle.Foreground(lipgloss.Color(t.Primary)).Bold(true),
		Attachment:  baseStyle.Foreground(lipgloss.Color(t.Warning)).Bold(true),
		DiffRemove:  baseStyle.Foreground(lipgloss.Color(t.Removed)),
		DiffAdd:     baseStyle.Foreground(lipgloss.Color(t.Added)),

		// Display styles
		Input:       baseStyle,
		Status:      baseStyle.Foreground(lipgloss.Color(t.Dim)),
		Separator:   baseStyle.Foreground(lipgloss.Color(t.Dim)),
		Confirm:     baseStyle.Foreground(lipgloss.Color(t.Error)).Bold(true),
		InputBorder: baseStyle.Border(lipgloss.RoundedBorder()),

		// Component-specific colors
		BorderFocused: lipgloss.Color(t.Primary),
		BorderBlurred: lipgloss.Color(t.Dim),
		BorderCursor:  lipgloss.Color(t.Selection),

		ColorAccent:  lipgloss.Color(t.Primary),
		ColorDim:     lipgloss.Color(t.Dim),
		ColorMuted:   lipgloss.Color(t.Muted),
		ColorError:   lipgloss.Color(t.Error),
		ColorSuccess: lipgloss.Color(t.Success),
		CursorColor:  lipgloss.Color(t.Cursor),

		FoldIndicator: t.FoldIndicator,
	}
}

func DefaultStyles() *Styles {
	return NewStyles(theme.DefaultTheme())
}
