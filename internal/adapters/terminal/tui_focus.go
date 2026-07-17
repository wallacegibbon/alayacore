package terminal

// Focus management: input/display focus switching, blur/focus handling.
//
// Extracted from tui.go. All remain methods on *Terminal.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display = m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.overlays.SetFocusedWindow(focusInput)
	m.display = m.display.SetDisplayFocused(false)
	m.input = m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.overlays.SetFocusedWindow(focusDisplay)
	m.display = m.display.SetDisplayFocused(true)
	m.input = m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display = m.display.SetCursorToLastWindow()
	}
}

// openModelSelector opens the model selector UI.
func (m *Terminal) openModelSelector() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenModelSelector()
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m *Terminal) restoreFocus() {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display = m.display.updateContent()
}

// openThemeSelector opens the theme selector UI.
func (m *Terminal) openThemeSelector() {
	if m.themeManager == nil {
		return
	}
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenThemeSelector(m.themeManager.GetThemes(), m.activeTheme)
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// openHelpWindow opens the help window UI.
func (m *Terminal) openHelpWindow() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenHelpWindow()
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// openAttachmentWindow opens the attachment picker overlay.
func (m *Terminal) openAttachmentWindow() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenAttachmentWindow(func(item string) {
		if strings.HasPrefix(item, "http://") || strings.HasPrefix(item, "https://") {
			m.addURLAttachment(item)
		} else {
			m.addAttachment(item)
		}
	})
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// openConfirmQuit opens the quit confirmation dialog.
func (m *Terminal) openConfirmQuit() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmQuit()
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m *Terminal) openConfirmCancel() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmCancel()
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m *Terminal) openConfirmTool(id, toolName, toolInput string) {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmTool(id, toolName, toolInput)
	m.input = m.input.Blur()
	m.display = m.display.SetDisplayFocused(false)
	m.display = m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (Terminal, tea.Cmd) {
	m.hasFocus = false
	m.display = m.display.SetDisplayFocused(false)
	m.input = m.input.Blur()
	m.overlays.SetFocused(false)
	m.display = m.display.updateContent()
	return *m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (Terminal, tea.Cmd) {
	m.hasFocus = true
	m.overlays.SetFocused(true)

	if m.overlays.ModelSelector().IsOpen() ||
		m.overlays.ThemeSelector().IsOpen() ||
		m.overlays.HelpWindow().IsOpen() ||
		m.overlays.AttachmentWindow().IsOpen() ||
		m.overlays.ConfirmOverlay().IsOpen() ||
		m.overlays.IsMCPInitOpen() {
		m.display = m.display.updateContent()
		return *m, nil
	}

	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display = m.display.updateContent()
	return *m, nil
}
