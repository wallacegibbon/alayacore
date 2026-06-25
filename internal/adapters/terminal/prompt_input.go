package terminal

// PromptInput handles text input with external editor support.
// It wraps an InputField for single-line editing and stores multi-line
// content (from external editor or paste) in editorContent.

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PromptInput handles text input.
type PromptInput struct {
	input         *InputField
	focused       bool
	editorContent string
	styles        *Styles
	width         int
}

// NewPromptInput creates a new prompt input.
func NewPromptInput(styles *Styles) PromptInput {
	input := NewInputField()
	input.Placeholder = "Enter your prompt..."
	input.Focus()
	input.Prompt = "> "
	input.SetWidth(max(0, DefaultWidth-InputPaddingH))

	return PromptInput{
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
func (m PromptInput) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.input.SetWidth(max(0, msg.Width-InputPaddingH))
	}

	m.updateFromMsg(msg)

	return m, nil
}

// View renders the input field.
func (m PromptInput) View() tea.View {
	m.updateInputStyles()
	return tea.NewView(m.input.View())
}

// updateInputStyles updates the text input styles based on current theme.
func (m PromptInput) updateInputStyles() {
	m.input.SetStyles(
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(m.styles.ColorAccent).Bold(true),
			Text:        lipgloss.NewStyle(),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorMuted),
		},
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true),
			Text:        lipgloss.NewStyle().Foreground(m.styles.ColorDim),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorDim),
		},
		m.styles.CursorColor,
	)
}

// Focus sets focus on the input.
func (m *PromptInput) Focus() {
	m.focused = true
	m.input.Focus()
}

// Blur removes focus from the input.
func (m *PromptInput) Blur() {
	m.focused = false
	m.input.Blur()
}

func (m PromptInput) IsFocused() bool {
	return m.focused
}

func (m PromptInput) Value() string {
	return m.input.Value()
}

func (m *PromptInput) SetValue(value string) {
	m.input.SetValue(value)
}

// Clear clears the input and editor content.
func (m *PromptInput) Clear() {
	m.input.SetValue("")
	m.editorContent = ""
}

// GetPrompt returns the actual prompt text (editor content or input value).
func (m PromptInput) GetPrompt() string {
	if m.editorContent != "" {
		return m.editorContent
	}
	return m.input.Value()
}

func (m PromptInput) GetEditorContent() string {
	return m.editorContent
}

func (m *PromptInput) ClearEditorContent() {
	m.editorContent = ""
}

// OpenEditor opens the external editor for multi-line input.
func (m *Terminal) OpenEditor() tea.Cmd {
	content := m.input.editorContent
	if content == "" {
		content = m.input.Value()
	}
	return m.editor.Open(content)
}

// RenderWithBorder renders the input with a border.
// When blockInput is true, renders an empty bordered box (visually indicating
// that input is blocked by an overlay) instead of the active input field.
func (m PromptInput) RenderWithBorder(blockInput bool) string {
	borderColor := m.styles.BorderFocused
	if !m.focused {
		borderColor = m.styles.BorderBlurred
	}

	// Set input styles based on focus state
	m.updateInputStyles()

	if blockInput {
		return m.styles.RenderBorderedBox("", m.width, borderColor)
	}

	return m.styles.RenderBorderedBox(m.input.View(), m.width, borderColor)
}

func (m *PromptInput) SetWidth(width int) {
	m.width = width
	m.input.SetWidth(max(0, width-InputPaddingH))
}

func (m *PromptInput) SetStyles(styles *Styles) {
	m.styles = styles
	m.updateInputStyles()
}

// CursorEnd moves cursor to end.
func (m *PromptInput) CursorEnd() {
	m.input.CursorEnd()
}

// updateFromMsg handles a message and updates internal state (non-tea.Model interface).
func (m *PromptInput) updateFromMsg(msg tea.Msg) {
	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	// Clear editor content if user manually edits the input field.
	// This ensures manual input takes precedence over editor-sourced content.
	if m.editorContent != "" && oldValue != newValue {
		m.editorContent = ""
	}
}

var _ tea.Model = (*PromptInput)(nil)
