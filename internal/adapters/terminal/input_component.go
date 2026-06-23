package terminal

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// InputModel handles text input.
type InputModel struct {
	input         textinput.Model
	focused       bool
	editorContent string
	styles        *Styles
	width         int
}

// NewInputModel creates a new input model
func NewInputModel(styles *Styles) InputModel {
	input := textinput.New()
	input.Placeholder = "Enter your prompt..."
	input.Focus()
	input.Prompt = "> "
	input.SetWidth(max(0, DefaultWidth-InputPaddingH))

	return InputModel{
		input:   input,
		focused: true,
		styles:  styles,
		width:   DefaultWidth,
	}
}

// Init initializes the input model
func (m InputModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the input model
func (m InputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.input.SetWidth(max(0, msg.Width-8))
	}

	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	// Clear editor content if user manually edits the input field
	if m.editorContent != "" && oldValue != newValue {
		m.editorContent = ""
	}

	return m, nil
}

// View renders the input field
func (m InputModel) View() tea.View {
	m.updateInputStyles()
	return tea.NewView(m.input.View())
}

// updateInputStyles updates the text input styles based on current theme
func (m InputModel) updateInputStyles() {
	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorAccent).Bold(true)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(m.styles.ColorDim)
	styles.Cursor.Color = m.styles.CursorColor
	m.input.SetStyles(styles)
}

// Focus sets focus on the input
func (m *InputModel) Focus() {
	m.focused = true
	m.input.Focus()
}

// Blur removes focus from the input
func (m *InputModel) Blur() {
	m.focused = false
	m.input.Blur()
}

func (m InputModel) IsFocused() bool {
	return m.focused
}

func (m InputModel) Value() string {
	return m.input.Value()
}

func (m *InputModel) SetValue(value string) {
	m.input.SetValue(value)
}

// Clear clears the input and editor content
func (m *InputModel) Clear() {
	m.input.SetValue("")
	m.editorContent = ""
}

// GetPrompt returns the actual prompt text (editor content or input value)
func (m InputModel) GetPrompt() string {
	if m.editorContent != "" {
		return m.editorContent
	}
	return m.input.Value()
}

func (m InputModel) GetEditorContent() string {
	return m.editorContent
}

func (m *InputModel) ClearEditorContent() {
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
func (m InputModel) RenderWithBorder(blockInput bool) string {
	borderColor := m.styles.BorderFocused
	if !m.focused {
		borderColor = m.styles.BorderBlurred
	}

	// Set input styles based on focus state
	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorAccent).Bold(true)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(m.styles.ColorDim)
	styles.Cursor.Color = m.styles.CursorColor
	m.input.SetStyles(styles)

	if blockInput {
		return m.styles.RenderBorderedBox("", m.width, borderColor)
	}

	return m.styles.RenderBorderedBox(m.input.View(), m.width, borderColor)
}

func (m *InputModel) SetWidth(width int) {
	m.width = width
	m.input.SetWidth(max(0, width-InputPaddingH))
}

func (m *InputModel) SetStyles(styles *Styles) {
	m.styles = styles
	m.updateInputStyles()
}

// CursorEnd moves cursor to end
func (m *InputModel) CursorEnd() {
	m.input.CursorEnd()
}

// updateFromMsg handles a message and updates internal state (non-tea.Model interface)
func (m *InputModel) updateFromMsg(msg tea.Msg) {
	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	// Clear editor content if user manually edits the input field.
	// This ensures manual input takes precedence over editor-sourced content.
	if m.editorContent != "" && oldValue != newValue {
		m.editorContent = ""
	}
}

var _ tea.Model = (*InputModel)(nil)
