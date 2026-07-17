package terminal

// PromptInput handles text input with external editor support.
// It wraps an InputField which supports multi-line content.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PromptInput handles text input.
type PromptInput struct {
	input       InputField
	attachments []string // pending attachment file paths to display
	focused     bool
	styles      *Styles
	width       int
}

// NewPromptInput creates a new prompt input.
func NewPromptInput(styles *Styles) *PromptInput {
	input := NewInputField()
	input.Placeholder = "Enter your prompt..."
	input = input.Focus()
	input.Prompt = ""
	input = input.SetWidth(max(0, DefaultWidth-BorderInnerPadding))

	return &PromptInput{
		input:   input,
		focused: true,
		styles:  styles,
		width:   DefaultWidth,
	}
}

// Init initializes the prompt input.
func (m PromptInput) Init() tea.Cmd {
	return nil
}

// Update handles messages for the prompt input.
func (m *PromptInput) Update(msg tea.Msg) (*PromptInput, tea.Cmd) {
	var cmd tea.Cmd
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.input = m.input.SetWidth(max(0, msg.Width-BorderInnerPadding))
	}
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the input field, with attachments above if present.
func (m PromptInput) View() tea.View {
	input := m.updateInputStyles()
	content := input.View()
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width-BorderInnerPadding)
		styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
		separator := m.styles.System.Width(innerWidth).Render(Separator)
		var sb strings.Builder
		sb.WriteString(styledMedia)
		sb.WriteString("\n")
		sb.WriteString(separator)
		sb.WriteString("\n")
		sb.WriteString(content)
		return tea.NewView(sb.String())
	}
	return tea.NewView(content)
}

// updateInputStyles updates the text input styles based on current theme.
func (m PromptInput) updateInputStyles() InputField {
	// Use warning color when there are multiple lines as a brighter visual cue.
	promptColor := m.styles.ColorAccent
	if m.input.LineCount() > 1 {
		promptColor = m.styles.ColorWarning
	}
	return m.input.SetStyles(
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(promptColor).Bold(true),
			Text:        lipgloss.NewStyle().Bold(true),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorMuted),
		},
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true),
			Text:        lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorDim),
		},
		m.styles.CursorColor,
	)
}

// Focus sets focus on the input.
func (m *PromptInput) Focus() {
	m.focused = true
	m.input = m.input.Focus()
}

// Blur removes focus from the input.
func (m *PromptInput) Blur() {
	m.focused = false
	m.input = m.input.Blur()
}

func (m PromptInput) IsFocused() bool {
	return m.focused
}

func (m PromptInput) Value() string {
	return m.input.Value()
}

func (m *PromptInput) SetValue(value string) {
	m.input = m.input.SetValue(value)
}

// SetAttachments sets the pending attachment paths for display.
func (m *PromptInput) SetAttachments(paths []string) {
	m.attachments = paths
}

// Clear clears the input and attachments.
func (m *PromptInput) Clear() {
	m.input = m.input.SetValue("")
	m.attachments = nil
}

// Attachments returns the current attachment paths.
func (m PromptInput) Attachments() []string {
	return m.attachments
}

// Height returns the total height (in terminal lines) of the rendered input box,
// including border and attachments if present.
func (m PromptInput) Height() int {
	// Base: border (2) + input field (1) = 3
	lines := 3
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width-BorderInnerPadding)
		styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
		lines += lipgloss.Height(styledMedia) + 1 // attachment lines + separator
	}
	return lines
}

// OpenEditor opens the external editor for multi-line input.
func (m *Terminal) OpenEditor() tea.Cmd {
	return m.editor.Open(m.input.Value())
}

// RenderWithBorder renders the input with a border.
// When blockInput is true, renders an empty bordered box (visually indicating
// that input is blocked by an overlay) instead of the active input field.
// Shows attachment list above the text input when present.
// When the input has multiple lines, the border uses warning color as a
// brighter visual cue.
func (m PromptInput) RenderWithBorder(blockInput bool) string {
	borderColor := m.styles.BorderFocused
	if !m.focused {
		borderColor = m.styles.BorderBlurred
	} else if m.input.LineCount() > 1 {
		borderColor = m.styles.ColorWarning
	}

	// Set input styles based on focus state
	input := m.updateInputStyles()

	if blockInput {
		return m.styles.RenderBorderedBox("", m.width, borderColor)
	}

	var content string
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width-BorderInnerPadding)
		styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
		separator := m.styles.System.Width(innerWidth).Render(Separator)
		var sb strings.Builder
		sb.WriteString(styledMedia)
		sb.WriteString("\n")
		sb.WriteString(separator)
		sb.WriteString("\n")
		sb.WriteString(input.View())
		content = sb.String()
	} else {
		content = input.View()
	}

	return m.styles.RenderBorderedBox(content, m.width, borderColor)
}

func (m *PromptInput) SetWidth(width int) {
	m.width = width
	m.input = m.input.SetWidth(max(0, width-BorderInnerPadding))
}

func (m *PromptInput) SetStyles(styles *Styles) {
	m.styles = styles
	m.input = m.updateInputStyles()
}

// CursorEnd moves cursor to end.
func (m *PromptInput) CursorEnd() {
	m.input = m.input.CursorEnd()
}

// CursorPos returns the cursor position (in runes) within the input field.
func (m PromptInput) CursorPos() int {
	return m.input.CursorPos()
}
