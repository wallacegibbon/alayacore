package terminal

// Focus management: input/display focus switching, blur/focus handling.
//
// Extracted from tui.go. All remain methods on *Terminal.

import tea "charm.land/bubbletea/v2"

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.overlays.SetFocusedWindow(focusInput)
	m.display.SetDisplayFocused(false)
	m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.overlays.SetFocusedWindow(focusDisplay)
	m.display.SetDisplayFocused(true)
	m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display.SetCursorToLastWindow()
	}
}

// openModelSelector opens the model selector UI.
func (m *Terminal) openModelSelector() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenModelSelector()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m *Terminal) restoreFocus() {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display.updateContent()
}

// openThemeSelector opens the theme selector UI.
func (m *Terminal) openThemeSelector() {
	if m.themeManager == nil {
		return
	}
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenThemeSelector(m.themeManager.GetThemes(), m.activeTheme)
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openHelpWindow opens the help window UI.
func (m *Terminal) openHelpWindow() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenHelpWindow()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmQuit opens the quit confirmation dialog.
func (m *Terminal) openConfirmQuit() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmQuit()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m *Terminal) openConfirmCancel() {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmCancel()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m *Terminal) openConfirmTool(id, toolName, toolInput string) {
	m.overlays.SetFocusedWindow(m.overlays.RestoreFocus())
	m.overlays.OpenConfirmTool(id, toolName, toolInput)
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.overlays.SetFocused(false)
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true
	m.overlays.SetFocused(true)

	if m.overlays.ModelSelector().IsOpen() ||
		m.overlays.ThemeSelector().IsOpen() ||
		m.overlays.HelpWindow().IsOpen() ||
		m.overlays.ConfirmOverlay().IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display.updateContent()
	return m, nil
}
