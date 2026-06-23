package terminal

// Key handling for the terminal UI.
// This file provides key bindings and the key handler.
// Key strings are as reported by bubbletea's tea.KeyMsg.String().

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Key Bindings
// ============================================================================

// ============================================================================
// Key Handler
// ============================================================================

// handleKeyMsg routes keyboard input to the appropriate handler.
func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+Z works from any context, including overlays
	if msg.String() == keyCtrlZ {
		return m, tea.Suspend
	}

	// 1. Confirm dialog takes priority over all other overlays.
	//    It must be on a higher layer because confirmations (e.g. tool
	//    execution prompts) can appear while another overlay is active.
	if m.confirmOverlay.IsOpen() {
		return m.handleOverlayConfirm(msg)
	}

	// 2. Theme selector takes precedence when open
	if m.themeSelector.IsOpen() {
		return m.handleThemeSelectorKeys(msg)
	}

	// 3. Model selector takes precedence when open
	if m.modelSelector.IsOpen() {
		return m.handleOverlayModelSelector(msg)
	}

	// 5. Help window takes precedence when open
	if m.helpWindow.IsOpen() {
		t := trackOverlay(m.helpWindow)
		cmd := m.helpWindow.HandleKeyMsg(msg)
		if t.JustClosed(m.helpWindow) {
			// If a command was selected via Enter, copy it to input
			if pending := m.helpWindow.ConsumePendingCommand(); pending != "" {
				// Insert the command into the input box and focus it.
				// Input and display are never focused simultaneously.
				m.focusInput()
				m.input.SetValue(pending + " ")
				m.input.CursorEnd()
				m.display.updateContent()
				return m, nil
			}
			m.restoreFocus()
		}
		return m, cmd
	}

	// 6. Tab toggles focus between display and input
	if msg.String() == keyTab {
		m.toggleFocus()
		return m, nil
	}

	// 7. Display-specific keys when display is focused
	if m.focusedWindow == focusDisplay {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	// 8. Global shortcuts (work from any context)
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// 9. Default: pass to input
	return m.handleInputKeys(msg)
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m *Terminal) handleThemeSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if it's a reload request
	if msg.String() == keyR && m.themeManager != nil {
		m.themeManager.ReloadThemes()
		m.themeSelector.Open(m.themeManager.GetThemes(), m.activeTheme)
		return m, nil
	}

	// Track if selector was open before handling key
	wasOpen := m.themeSelector.IsOpen()

	previewTheme, handled := m.themeSelector.HandleKeyMsg(msg, m.themeManager)
	if !handled {
		return m, nil
	}

	// Check if theme was selected (Enter key)
	if m.themeSelector.ConsumeThemeSelected() {
		selectedTheme := m.themeSelector.GetSelectedTheme()
		if selectedTheme != nil {
			// Send theme_set command to session via TLV
			// The session will persist the theme and the terminal will apply
			// it visually when it receives the updated theme message.
			m.emitCommand(":theme_set " + selectedTheme.Name)
		}
		m.restoreFocus()
		return m, nil
	}

	// If selector closed without selection (ESC/q), restore original theme
	if wasOpen && !m.themeSelector.IsOpen() {
		originalThemeName := m.themeSelector.GetOriginalThemeName()
		originalTheme := m.themeManager.LoadTheme(originalThemeName)
		m.applyTheme(originalTheme)
		m.restoreFocus()
		return m, nil
	}

	// Apply preview theme if changed
	if previewTheme != nil {
		// For navigation keys, delay theme application to keep cursor responsive
		key := msg.String()
		if key == keyJ || key == keyK || key == keyUp || key == keyDown {
			// Increment the ID to invalidate any pending preview
			m.themePreviewID++
			// Capture the current ID to check if this preview is still valid
			id := m.themePreviewID
			theme := previewTheme

			// Return a command that will apply the theme after a delay
			// This allows the cursor to update immediately
			return m, tea.Tick(ThemePreviewDebounce, func(_ time.Time) tea.Msg {
				return themePreviewMsg{theme: theme, id: id}
			})
		}
		// Apply immediately for other keys
		m.applyTheme(previewTheme)
	}

	return m, nil
}

// themePreviewMsg is sent when a theme preview should be applied
type themePreviewMsg struct {
	theme *theme.Theme
	id    int // ID to check if this preview is still current
}

func (m *Terminal) handleThemePreview(msg themePreviewMsg) (tea.Model, tea.Cmd) {
	// Only apply if this preview is still the current one (debouncing)
	if msg.id == m.themePreviewID {
		m.applyTheme(msg.theme)
	}
	return m, nil
}

func (m *Terminal) handleConfirmResult() (tea.Model, tea.Cmd) {
	confirmed, canceled := m.confirmOverlay.ConsumeResult()
	if !confirmed && !canceled {
		return m, nil
	}

	kind := m.confirmOverlay.Kind()
	toolID := m.confirmOverlay.ToolID()
	m.confirmOverlay.Close()

	fromCmd := m.confirmFromCommand
	m.confirmFromCommand = false

	if canceled {
		return m.handleConfirmCanceled(kind, toolID, fromCmd)
	}
	return m.handleConfirmConfirmed(kind, toolID, fromCmd)
}

// restoreFocusAfterConfirm restores input/display focus only if no overlay
// is still open. If another overlay (e.g. model selector) was active before
// the confirm appeared, it remains active — the overlay naturally catches
// keys in handleKeyMsg.
func (m *Terminal) restoreFocusAfterConfirm() {
	if m.modelSelector.IsOpen() || m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen() {
		m.display.updateContent()
		return
	}
	m.restoreFocus()
}

// handleConfirmCanceled handles the cancel path of a confirm dialog result.
func (m *Terminal) handleConfirmCanceled(kind ConfirmKind, toolID string, fromCmd bool) (tea.Model, tea.Cmd) {
	if fromCmd {
		m.input.SetValue("")
	}

	if kind == ConfirmTool {
		m.emitCommand(":confirm " + toolID + " no")
		m.restoreFocusAfterConfirm()
		if id, toolName, toolInput, ok := m.out.GetPendingToolConfirm(); ok {
			m.openConfirmTool(id, toolName, toolInput)
		}
		return m, scheduleTick()
	}

	m.restoreFocusAfterConfirm()
	return m, nil
}

// handleConfirmConfirmed handles the confirm (yes) path of a confirm dialog result.
func (m *Terminal) handleConfirmConfirmed(kind ConfirmKind, toolID string, fromCmd bool) (tea.Model, tea.Cmd) {
	switch kind {
	case ConfirmQuit:
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return m, tea.Quit

	case ConfirmCancel:
		if fromCmd {
			m.input.SetValue("")
		}
		m.restoreFocusAfterConfirm()
		return m, m.submitCommand(agentpkg.CommandNameCancel, fromCmd)

	case ConfirmTool:
		m.emitCommand(":confirm " + toolID + " yes")
		m.restoreFocusAfterConfirm()
		if nextID, nextName, nextInput, ok := m.out.GetPendingToolConfirm(); ok {
			m.openConfirmTool(nextID, nextName, nextInput)
		}
		return m, scheduleTick()
	}
	return m, nil
}

// handleOverlayModelSelector handles keyboard input when the model selector is open.
func (m *Terminal) handleOverlayModelSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wasOpen := m.modelSelector.IsOpen()
	cmd := m.modelSelector.HandleKeyMsg(msg)

	if m.modelSelector.ConsumeModelSelected() {
		m.switchToSelectedModel()
	}
	if m.modelSelector.ConsumeOpenModelFile() {
		return m, tea.Batch(cmd, m.openModelConfigFile())
	}
	if m.modelSelector.ConsumeReloadModels() {
		m.emitCommand(":model_load")
	}
	if wasOpen && !m.modelSelector.IsOpen() {
		m.restoreFocus()
	}
	return m, cmd
}

// handleOverlayConfirm handles keyboard input when the confirm dialog is open.
func (m *Terminal) handleOverlayConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 'e' opens the full tool input in an external editor for inspection
	// (view-only — dialog stays open after editor closes)
	if msg.String() == keyE && m.confirmOverlay.Kind() == ConfirmTool && m.confirmOverlay.ToolInput() != "" {
		content := m.confirmOverlay.ToolInput()
		if toolName := m.confirmOverlay.ToolName(); toolName != "" && strings.HasPrefix(content, toolName+": ") {
			content = content[len(toolName)+2:]
		}
		return m, m.editor.OpenForDisplay(content)
	}
	if handled := m.confirmOverlay.HandleKeyMsg(msg); handled {
		return m.handleConfirmResult()
	}
	return m, nil
}

// Display key handler helpers (shared between multiple keys).
// These don't return tea.Cmd — the map type doesn't require it.

func moveWindowCursorDown(m *Terminal) {
	if m.display.MoveWindowCursorDown() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
}

func moveWindowCursorUp(m *Terminal) {
	if m.display.MoveWindowCursorUp() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
}

func scrollDownLine(m *Terminal) {
	if !m.display.AtBottom() {
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		m.display.updateContent()
	}
}

func scrollUpLine(m *Terminal) {
	m.display.MarkUserScrolled()
	m.display.ScrollUp(1)
	m.display.updateContent()
}

func scrollDownHalf(m *Terminal) {
	if !m.display.AtBottom() {
		m.display.MarkUserScrolled()
		m.display.ScrollDown(max(1, m.display.GetHeight()/2))
		m.display.updateContent()
	}
}

func scrollUpHalf(m *Terminal) {
	m.display.MarkUserScrolled()
	m.display.ScrollUp(max(1, m.display.GetHeight()/2))
	m.display.updateContent()
}

func gotoBottom(m *Terminal) {
	m.display.SetCursorToLastWindow()
	m.display.GotoBottom()
	m.display.updateContent()
}

func gotoTop(m *Terminal) {
	m.display.SetWindowCursor(0)
	m.display.GotoTop()
	m.display.updateContent()
}

// DisplayKeyHandler handles a display key event and returns an optional tea.Cmd.
type DisplayKeyHandler func(*Terminal) tea.Cmd

// displayKeyHandlers maps display key strings to their handler functions.
// All display keys are listed in a single map; handlers that don't need
// to return a command simply return nil.
var displayKeyHandlers = map[string]DisplayKeyHandler{
	keyJ:         func(m *Terminal) tea.Cmd { moveWindowCursorDown(m); return nil },
	keyDown:      func(m *Terminal) tea.Cmd { moveWindowCursorDown(m); return nil },
	keyK:         func(m *Terminal) tea.Cmd { moveWindowCursorUp(m); return nil },
	keyUp:        func(m *Terminal) tea.Cmd { moveWindowCursorUp(m); return nil },
	keyCtrlD:     func(m *Terminal) tea.Cmd { scrollDownHalf(m); return nil },
	keyPgDown:    func(m *Terminal) tea.Cmd { scrollDownHalf(m); return nil },
	keyCtrlU:     func(m *Terminal) tea.Cmd { scrollUpHalf(m); return nil },
	keyPgUp:      func(m *Terminal) tea.Cmd { scrollUpHalf(m); return nil },
	keyJCapital:  func(m *Terminal) tea.Cmd { scrollDownLine(m); return nil },
	keyShiftDown: func(m *Terminal) tea.Cmd { scrollDownLine(m); return nil },
	keyKCapital:  func(m *Terminal) tea.Cmd { scrollUpLine(m); return nil },
	keyShiftUp:   func(m *Terminal) tea.Cmd { scrollUpLine(m); return nil },
	keyH: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToTop() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	keyL: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToBottom() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	keyM: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToCenter() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	keyG:      func(m *Terminal) tea.Cmd { gotoBottom(m); return nil },
	keyEnd:    func(m *Terminal) tea.Cmd { gotoBottom(m); return nil },
	keyGSmall: func(m *Terminal) tea.Cmd { gotoTop(m); return nil },
	keyHome:   func(m *Terminal) tea.Cmd { gotoTop(m); return nil },
	keyColon: func(m *Terminal) tea.Cmd {
		m.focusInput()
		m.input.SetValue(keyColon)
		m.input.CursorEnd()
		m.display.updateContent()
		return nil
	},
	keySpace: func(m *Terminal) tea.Cmd {
		if m.display.ToggleWindowFold() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	keyF: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToNextUserPrompt() {
			m.display.ScrollCursorToTop()
			m.display.updateContent()
		}
		return nil
	},
	keyB: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToPrevUserPrompt() {
			m.display.ScrollCursorToTop()
			m.display.updateContent()
		}
		return nil
	},
	keyE: func(m *Terminal) tea.Cmd {
		content := m.display.GetCursorWindowContent()
		if content != "" {
			m.display.MarkUserScrolled()
			return m.editor.OpenForDisplay(content)
		}
		return nil
	},
	keyCtrlF: func(m *Terminal) tea.Cmd {
		if historyID := m.display.GetCursorWindowHistoryID(); historyID > 0 {
			m.focusInput()
			m.input.SetValue(fmt.Sprintf(":fork %d ", historyID))
			m.input.CursorEnd()
			m.display.updateContent()
		}
		return nil
	},
}

// handleDisplayKeys handles key events when display window is focused.
//
// IMPORTANT: When moving the cursor, always call a scroll method
// (EnsureCursorVisible() or ScrollCursorToTop()) BEFORE updateContent().
// This ensures the viewport position is updated before content is
// regenerated, preventing blank areas in the virtual rendering.
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	if fn, ok := displayKeyHandlers[msg.String()]; ok {
		return fn(m), true
	}
	return nil, false
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case keyCtrlG:
		m.openConfirmCancel()
		m.confirmFromCommand = false
		return nil, true

	case keyCtrlS:
		return m.submitCommand(agentpkg.CommandNameSave, false), true

	case keyCtrlL:
		m.openModelSelector()
		return nil, true

	case keyCtrlP:
		m.openThemeSelector()
		return nil, true

	case keyCtrlQ:
		return nil, true

	case keyCtrlH, keyF1:
		m.openHelpWindow()
		return nil, true

	case keyEnter:
		return m.handleSubmit(), true
	}

	return nil, false
}

// handleInputKeys handles keys when input is focused (default behavior).
func (m *Terminal) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Input-only global shortcuts
	if m.focusedWindow == focusInput {
		switch msg.String() {
		case keyCtrlO:
			return m, m.OpenEditor()
		case keyCtrlC:
			m.input.SetValue("")
			m.input.editorContent = ""
			return m, nil
		}
	}

	// Block keys that would modify input content unexpectedly
	switch msg.String() {
	case keyCtrlU, keyCtrlD:
		// Swallow to prevent textinput's default clear-line / delete-char
		return m, nil
	}

	m.input.updateFromMsg(msg)

	return m, nil
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit processes the input when Enter is pressed.
func (m *Terminal) handleSubmit() tea.Cmd {
	prompt := strings.TrimSpace(m.input.GetPrompt())
	m.input.editorContent = ""

	if prompt == "" {
		return nil
	}

	// Check if it's a command (starts with ":")
	if command, found := strings.CutPrefix(prompt, ":"); found {
		return m.handleCommand(strings.TrimSpace(command))
	}

	// If a task is running, reject without clearing input.
	if m.inProgress {
		m.out.WriteError("A task is already running. Wait for it to complete or cancel it.")
		return nil
	}

	// Regular prompt — stage the text, then flush with UE.
	m.emitCommand(prompt)
	m.emitMB()
	m.input.SetValue("")

	return scheduleTick()
}

// handleCommand processes a command string (without the ":" prefix).
func (m *Terminal) handleCommand(command string) tea.Cmd {
	// Quit command
	if command == cmdQuit || command == cmdQShort {
		m.openConfirmQuit()
		m.confirmFromCommand = true
		return nil
	}

	// Cancel command
	if command == cmdCancel {
		m.openConfirmCancel()
		m.confirmFromCommand = true
		return nil
	}

	// Suspend command - suspends the process (like Ctrl+Z)
	if command == cmdSuspend {
		m.input.SetValue("")
		return tea.Suspend
	}

	// Help command - opens help window locally, not sent to session
	if command == cmdHelp {
		m.input.SetValue("")
		m.openHelpWindow()
		return nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	m.emitCommand(":" + command)
	if clearInput {
		m.input.SetValue("")
	}
	return scheduleTick()
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() tea.Cmd {
	return tea.Tick(SubmitTickDelay, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// switchToSelectedModel sends a model_set command to switch to the selected model.
func (m *Terminal) switchToSelectedModel() {
	selectedModel := m.modelSelector.GetActiveModel()
	if selectedModel == nil {
		return
	}

	// Send model_set command to session
	if selectedModel.ID != 0 {
		m.emitCommand(fmt.Sprintf(":model_set %d", selectedModel.ID))
	}
}

// openModelConfigFile opens the model config file with $EDITOR.
func (m *Terminal) openModelConfigFile() tea.Cmd {
	path := m.out.SnapshotModels().ConfigPath
	if path == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{
				Err:      fmt.Errorf("no model config file path configured"),
				Action:   EditorActionReloadConfig,
				FileType: "model_config",
			}
		}
	}

	return m.editor.OpenFile(path, "model_config")
}
