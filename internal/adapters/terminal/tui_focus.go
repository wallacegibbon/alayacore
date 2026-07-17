package terminal

// Focus management: input/display focus switching, blur/focus handling, paste.
//
// Extracted from tui.go.

import tea "charm.land/bubbletea/v2"

// toggleFocus switches between display and input windows.
func (m Terminal) toggleFocus() Terminal {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m = m.focusInput()
	} else {
		m = m.focusDisplay()
	}
	m.display = m.display.updateContent()
	return m
}

// focusInput switches focus to the input window.
func (m Terminal) focusInput() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(focusInput)
	m.display = m.display.WithDisplayFocused(false)
	m.input = m.input.Focus()
	return m
}

// focusDisplay switches focus to the display window.
func (m Terminal) focusDisplay() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(focusDisplay)
	m.display = m.display.WithDisplayFocused(true)
	m.input = m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display = m.display.WithCursorToLastWindow()
	}
	return m
}

// openModelSelector opens the model selector UI.
func (m Terminal) openModelSelector() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenModelSelector()
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m Terminal) restoreFocus() Terminal {
	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// openThemeSelector opens the theme selector UI.
func (m Terminal) openThemeSelector() Terminal {
	if m.themeManager == nil {
		return m
	}
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenThemeSelector(m.themeManager.GetThemes(), m.activeTheme, m.themeManager)
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openHelpWindow opens the help window UI.
func (m Terminal) openHelpWindow() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenHelpWindow()
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openAttachmentWindow opens the attachment picker overlay.
func (m Terminal) openAttachmentWindow() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenAttachmentWindow()
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmQuit opens the quit confirmation dialog.
func (m Terminal) openConfirmQuit() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenConfirmQuit()
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m Terminal) openConfirmCancel() Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenConfirmCancel()
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m Terminal) openConfirmTool(id, toolName, toolInput string) Terminal {
	m.overlays = m.overlays.WithFocusedWindow(m.overlays.RestoreFocus())
	m.overlays = m.overlays.OpenConfirmTool(id, toolName, toolInput)
	m.input = m.input.Blur()
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// handleBlur handles loss of application focus.
func (m Terminal) handleBlur() Terminal {
	m.hasFocus = false
	m.display = m.display.WithDisplayFocused(false)
	m.input = m.input.Blur()
	m.overlays = m.overlays.WithFocus(false)
	m.display = m.display.updateContent()
	return m
}

// handleFocus handles gain of application focus.
func (m Terminal) handleFocus() Terminal {
	m.hasFocus = true
	m.overlays = m.overlays.WithFocus(true)

	if m.overlays.ModelSelector().IsOpen() ||
		m.overlays.ThemeSelector().IsOpen() ||
		m.overlays.HelpWindow().IsOpen() ||
		m.overlays.AttachmentWindow().IsOpen() ||
		m.overlays.ConfirmOverlay().IsOpen() ||
		m.overlays.IsMCPInitOpen() {
		m.display = m.display.updateContent()
		return m
	}

	fw := m.overlays.RestoreFocus()
	if fw == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// handlePaste handles clipboard paste events.
func (m Terminal) handlePaste(msg tea.PasteMsg) (Terminal, tea.Cmd) {
	if m.overlays.AttachmentWindow().IsOpen() {
		aw := m.overlays.AttachmentWindow()
		aw, _ = aw.Update(msg)
		m.overlays = m.overlays.WithAttachmentWindow(aw)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
