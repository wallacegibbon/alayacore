package terminal

// InputField is a single-line text input component.
// Supports: text entry, basic deletion, cursor navigation,
// placeholder, prompt prefix, horizontal scrolling, focus/blur styling.
// Multi-line editing is handled by the external editor (Ctrl+O).

import (
	"image/color"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"
)

// InputField is the Bubble Tea model for a single-line text input.
type InputField struct {
	value       []rune
	pos         int // cursor position in value
	offset      int // horizontal scroll offset (cells)
	width       int // visible width (cells)
	Prompt      string
	Placeholder string
	focused     bool

	styleFocused inputFieldStyle
	styleBlurred inputFieldStyle
	cursorColor  color.Color
	cursorRender func(string) string
	promptRender func() string
}

type inputFieldStyle struct {
	Prompt      lipgloss.Style
	Text        lipgloss.Style
	Placeholder lipgloss.Style
}

// NewInputField creates a new InputField with default settings.
func NewInputField() *InputField {
	return &InputField{
		width:   20,
		Prompt:  "> ",
		focused: true,
	}
}

// Init implements tea.Model.
func (m *InputField) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *InputField) Update(msg tea.Msg) (*InputField, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	if msg, ok := msg.(tea.KeyMsg); ok {
		return m.handleKeyMsg(msg)
	}
	return m, nil
}

func (m *InputField) handleKeyMsg(msg tea.KeyMsg) (*InputField, tea.Cmd) {
	key := msg.String()

	if m.handleMovement(key) {
		return m, nil
	}
	if m.handleDeletion(key) {
		return m, nil
	}
	if m.handleInsertion(key) {
		return m, nil
	}

	return m, nil
}

// handleMovement returns true if the key was a cursor movement.
func (m *InputField) handleMovement(key string) bool {
	switch {
	case key == "left" || key == "ctrl+b":
		m.pos = max(0, m.pos-1)
	case key == "right" || key == "ctrl+f":
		m.pos = min(len(m.value), m.pos+1)
	case key == "home" || key == "ctrl+a":
		m.pos = 0
		m.offset = 0
		return true
	case key == "end" || key == "ctrl+e":
		m.pos = len(m.value)
	default:
		return false
	}
	m.ensureCursorVisible()
	return true
}

// handleDeletion returns true if the key was a deletion action.
func (m *InputField) handleDeletion(key string) bool {
	switch {
	case key == "backspace" || key == "ctrl+h":
		if m.pos > 0 {
			m.value = slices.Delete(m.value, m.pos-1, m.pos)
			m.pos--
		} else {
			return true
		}
	case key == "delete" || key == "ctrl+d":
		if m.pos < len(m.value) {
			m.value = slices.Delete(m.value, m.pos, m.pos+1)
		}
	default:
		return false
	}
	m.ensureCursorVisible()
	return true
}

// handleInsertion returns true if the key was a character or space insertion.
func (m *InputField) handleInsertion(key string) bool {
	if key == "space" {
		m.value = slices.Insert(m.value, m.pos, ' ')
		m.pos++
		m.ensureCursorVisible()
		return true
	}
	if r, ok := printableRune(key); ok {
		m.value = slices.Insert(m.value, m.pos, r)
		m.pos++
		m.ensureCursorVisible()
		return true
	}
	return false
}

func (m *InputField) ensureCursorVisible() {
	if m.width <= 0 {
		return
	}
	cursorCell := runesWidth(m.value[:m.pos])
	visibleEnd := m.offset + m.width
	switch {
	case cursorCell < m.offset:
		m.offset = cursorCell
	case cursorCell >= visibleEnd:
		m.offset = cursorCell - m.width + 2
		if m.offset < 0 {
			m.offset = 0
		}
	}
}

// View implements tea.Model.
func (m *InputField) View() string {
	if len(m.value) == 0 && m.Placeholder != "" {
		return m.placeholderView()
	}

	styles := m.activeStyle()
	styleText := styles.Text.Inline(true).Render
	visible := m.buildVisibleText()
	cursorCell := max(0, runesWidth(m.value[:m.pos])-m.offset)

	var v string
	if cursorCell < len(visible) {
		v += styleText(visible[:cursorCell])
		v += m.cursorRender(string(visible[cursorCell]))
		v += styleText(visible[cursorCell+1:])
	} else {
		v += styleText(visible)
		v += m.cursorRender(" ")
	}

	valWidth := ansi.StringWidth(visible)
	if m.width > 0 && valWidth < m.width {
		padding := m.width - valWidth
		if cursorCell >= len(visible) {
			padding++
		}
		v += styleText(strings.Repeat(" ", padding))
	}

	return m.promptRender() + v
}

func (m *InputField) buildVisibleText() string {
	if len(m.value) == 0 {
		return ""
	}
	startIdx := 0
	for cells, i := 0, 0; i < len(m.value); i++ {
		w := rw.RuneWidth(m.value[i])
		if cells >= m.offset {
			startIdx = i
			break
		}
		if cells+w > m.offset {
			startIdx = i
			break
		}
		cells += w
	}
	var visible strings.Builder
	for cells := 0; startIdx < len(m.value); startIdx++ {
		w := rw.RuneWidth(m.value[startIdx])
		if cells+w > m.width {
			break
		}
		visible.WriteRune(m.value[startIdx])
		cells += w
	}
	return visible.String()
}

func (m *InputField) placeholderView() string {
	v := m.cursorRender(" ")
	placeholder := m.Placeholder
	if m.width > 0 && ansi.StringWidth(placeholder) > m.width-1 {
		placeholder = truncatePlaceholder(placeholder, m.width-1)
	}
	v += m.activeStyle().Placeholder.Inline(true).Render(placeholder)
	valWidth := ansi.StringWidth(v)
	if m.width > 0 && valWidth < m.width {
		v += strings.Repeat(" ", m.width-valWidth)
	}
	return m.promptRender() + v
}

func (m *InputField) activeStyle() inputFieldStyle {
	if m.focused {
		return m.styleFocused
	}
	return m.styleBlurred
}

func (m *InputField) Focus() {
	m.focused = true
	m.rebuildRenderFuncs()
}

func (m *InputField) Blur() {
	m.focused = false
	m.rebuildRenderFuncs()
}

func (m *InputField) IsFocused() bool { return m.focused }

func (m *InputField) Value() string { return string(m.value) }

func (m *InputField) CursorEnd() {
	m.pos = len(m.value)
	m.ensureCursorVisible()
}

func (m *InputField) SetValue(s string) {
	m.value = []rune(s)
	m.pos = len(m.value)
	m.ensureCursorVisible()
}

func (m *InputField) SetWidth(w int) {
	m.width = max(0, w)
	m.ensureCursorVisible()
}

func (m *InputField) SetStyles(focused, blurred inputFieldStyle, cursorColor color.Color) {
	m.styleFocused = focused
	m.styleBlurred = blurred
	m.cursorColor = cursorColor
	m.rebuildRenderFuncs()
}

func (m *InputField) rebuildRenderFuncs() {
	styles := m.activeStyle()
	cursorBG := lipgloss.NewStyle().Background(m.cursorColor).Foreground(lipgloss.Color("0"))
	m.cursorRender = func(s string) string { return cursorBG.Render(s) }
	m.promptRender = func() string { return styles.Prompt.Inline(true).Render(m.Prompt) }
}

// ============================================================================
// Helpers
// ============================================================================

func runesWidth(runes []rune) int {
	w := 0
	for _, r := range runes {
		w += rw.RuneWidth(r)
	}
	return w
}

func printableRune(key string) (rune, bool) {
	if len(key) != 1 {
		return 0, false
	}
	r := []rune(key)[0]
	if unicode.IsControl(r) || r == 0x7f {
		return 0, false
	}
	return r, true
}

func truncatePlaceholder(s string, maxWidth int) string {
	var result strings.Builder
	cells := 0
	for _, r := range s {
		w := rw.RuneWidth(r)
		if cells+w > maxWidth {
			break
		}
		result.WriteRune(r)
		cells += w
	}
	return result.String()
}
