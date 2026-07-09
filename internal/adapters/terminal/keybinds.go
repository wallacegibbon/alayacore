package terminal

// Key handling for the terminal UI.
// This file provides key bindings and the key handler.
// Key strings are as reported by bubbletea's tea.KeyMsg.String().

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/platform"
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
	// During async session loading, ignore all keyboard input.
	if m.loading {
		return m, nil
	}

	// Ctrl+Z works from any context, including overlays
	if msg.String() == keyCtrlZ {
		return m, tea.Suspend
	}

	// 1. Confirm dialog takes priority over all other overlays.
	//    It must be on a higher layer because confirmations (e.g. tool
	//    execution prompts) can appear while another overlay is active.
	if m.overlays.ConfirmOverlay().IsOpen() {
		return m.handleOverlayConfirm(msg)
	}

	// 1b. MCP init overlay — shows MCP init progress (and behind it,
	//     the auth confirm dialog may appear temporarily).
	//     Ctrl+G cancels the entire MCP initialization.
	if m.overlays.MCPInitOverlay().IsOpen() {
		if msg.String() == keyCtrlG {
			m.emitCommand(":mcp_cancel")
			return m, scheduleTick()
		}
		return m, nil
	}

	// 2. Theme selector takes precedence when open
	if m.overlays.ThemeSelector().IsOpen() {
		return m.handleThemeSelectorKeys(msg)
	}

	// 3. Model selector takes precedence when open
	if m.overlays.ModelSelector().IsOpen() {
		return m.handleOverlayModelSelector(msg)
	}

	// 5. Help window takes precedence when open
	if m.overlays.HelpWindow().IsOpen() {
		t := trackOverlay(m.overlays.HelpWindow())
		cmd := m.overlays.HelpWindow().HandleKeyMsg(msg)
		if t.JustClosed(m.overlays.HelpWindow()) {
			// If a command was selected via Enter, copy it to input
			if pending := m.overlays.HelpWindow().ConsumePendingCommand(); pending != "" {
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
	if m.overlays.RestoreFocus() == focusDisplay {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	// 8. Global shortcuts (work from any context)
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// 9. Fallback: pass to input field (no-op when display focused)
	return m.handleFallback(msg)
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m *Terminal) handleThemeSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if it's a reload request
	if msg.String() == keyR && m.themeManager != nil {
		m.themeManager.ReloadThemes()
		m.overlays.ThemeSelector().Open(m.themeManager.GetThemes(), m.activeTheme)
		return m, nil
	}

	// Track if selector was open before handling key
	wasOpen := m.overlays.ThemeSelector().IsOpen()

	previewTheme, handled := m.overlays.ThemeSelector().HandleKeyMsg(msg, m.themeManager)
	if !handled {
		return m, nil
	}

	// Check if theme was selected (Enter key)
	if m.overlays.ThemeSelector().ConsumeThemeSelected() {
		selectedTheme := m.overlays.ThemeSelector().GetSelectedTheme()
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
	if wasOpen && !m.overlays.ThemeSelector().IsOpen() {
		originalThemeName := m.overlays.ThemeSelector().GetOriginalThemeName()
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
	r := m.overlays.ConfirmOverlay().ConsumeResult()
	if r == nil {
		return m, nil
	}

	fromCmd := m.confirmFromCommand
	m.confirmFromCommand = false

	switch r.Kind {
	case ConfirmQuit:
		return m.handleConfirmQuit(r, fromCmd)
	case ConfirmCancel:
		return m.handleConfirmCancel(r, fromCmd)
	case ConfirmTool:
		return m.handleConfirmTool(r, fromCmd)
	case ConfirmMCPAuth:
		return m.handleConfirmMCPAuth(r, fromCmd)
	}
	return m, nil
}

func (m *Terminal) handleConfirmQuit(r *ConfirmResult, fromCmd bool) (tea.Model, tea.Cmd) {
	if r.Confirmed {
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return m, tea.Quit
	}
	if fromCmd {
		m.input.SetValue("")
	}
	m.restoreFocusAfterConfirm()
	return m, nil
}

func (m *Terminal) handleConfirmCancel(r *ConfirmResult, fromCmd bool) (tea.Model, tea.Cmd) {
	if fromCmd {
		m.input.SetValue("")
	}
	m.restoreFocusAfterConfirm()
	if r.Confirmed {
		return m, m.submitCommand("cancel", fromCmd)
	}
	return m, nil
}

func (m *Terminal) handleConfirmTool(r *ConfirmResult, fromCmd bool) (tea.Model, tea.Cmd) {
	action := "no"
	if r.Confirmed {
		action = "yes"
	}
	if fromCmd {
		m.input.SetValue("")
	}
	m.emitCommand(":confirm " + r.ToolID + " " + action)
	m.restoreFocusAfterConfirm()
	if nextID, nextName, nextInput, ok := m.out.GetPendingToolConfirm(); ok {
		m.openConfirmTool(nextID, nextName, nextInput)
	}
	return m, scheduleTick()
}

func (m *Terminal) handleConfirmMCPAuth(r *ConfirmResult, fromCmd bool) (tea.Model, tea.Cmd) {
	switch {
	case r.Confirmed:
		// Start a callback server on the adapter's machine so the browser
		// (also on this machine) can redirect to localhost.
		resultCh, redirectURI, cleanup := platform.StartCallbackServer("127.0.0.1:0", r.State)

		// Replace the placeholder in the auth URL with the actual redirect_uri.
		encodedRedirect := url.QueryEscape(redirectURI)
		filledURL := strings.ReplaceAll(r.ToolInput, "REDIRECT_URI_HERE", encodedRedirect)

		if err := platform.OpenURL(filledURL); err != nil {
			m.out.WriteError("Failed to open browser: %v", err)
		}

		// Wait for the auth code in a background goroutine.
		go func() {
			res := <-resultCh
			cleanup()
			if res.Err != nil {
				m.out.WriteError("MCP auth callback error: %v", res.Err)
				m.emitCommand(":mcp_cancel")
				return
			}
			m.emitCommand(fmt.Sprintf(":mcp_auth %s %s %s",
				r.ToolID, res.Code, redirectURI))
		}()
	case r.CtrlGCanceled:
		m.out.ClearMCPAuths()
		m.emitCommand(":mcp_cancel")
	default:
		if fromCmd {
			m.input.SetValue("")
		}
		m.emitCommand(":mcp_auth " + r.ToolID)
	}
	m.restoreFocusAfterConfirm()
	return m, scheduleTick()
}

// restoreFocusAfterConfirm restores input/display focus only if no overlay
// is still open. If another overlay (e.g. model selector) was active before
// the confirm appeared, it remains active — the overlay naturally catches
// keys in handleKeyMsg.
func (m *Terminal) restoreFocusAfterConfirm() {
	if m.overlays.ModelSelector().IsOpen() || m.overlays.ThemeSelector().IsOpen() ||
		m.overlays.HelpWindow().IsOpen() || m.overlays.IsMCPInitOpen() {
		m.display.updateContent()
		return
	}
	m.restoreFocus()
}

// handleOverlayModelSelector handles keyboard input when the model selector is open.
func (m *Terminal) handleOverlayModelSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wasOpen := m.overlays.ModelSelector().IsOpen()
	cmd := m.overlays.ModelSelector().HandleKeyMsg(msg)

	if m.overlays.ModelSelector().ConsumeModelSelected() {
		m.switchToSelectedModel()
	}
	if m.overlays.ModelSelector().ConsumeReloadModels() {
		m.emitCommand(":model_load")
	}
	if wasOpen && !m.overlays.ModelSelector().IsOpen() {
		m.restoreFocus()
	}
	return m, cmd
}

// handleOverlayConfirm handles keyboard input when the confirm dialog is open.
func (m *Terminal) handleOverlayConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 'e' opens the full tool input in an external editor for inspection
	// (view-only — dialog stays open after editor closes)
	if msg.String() == keyE && m.overlays.ConfirmOverlay().Kind() == ConfirmTool && m.overlays.ConfirmOverlay().ToolInput() != "" {
		content := m.overlays.ConfirmOverlay().ToolInput()
		if toolName := m.overlays.ConfirmOverlay().ToolName(); toolName != "" && strings.HasPrefix(content, toolName+": ") {
			content = content[len(toolName)+2:]
		}
		return m, m.editor.OpenForDisplay(content)
	}
	if handled := m.overlays.ConfirmOverlay().HandleKeyMsg(msg); handled {
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

// ---------- display key handler implementations ----------

func handleDisplayKeyH(m *Terminal) tea.Cmd {
	if m.display.MoveWindowCursorToTop() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyL(m *Terminal) tea.Cmd {
	if m.display.MoveWindowCursorToBottom() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyM(m *Terminal) tea.Cmd {
	if m.display.MoveWindowCursorToCenter() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyColon(m *Terminal) tea.Cmd {
	m.focusInput()
	m.input.SetValue(keyColon)
	m.input.CursorEnd()
	m.display.updateContent()
	return nil
}

func handleDisplayKeySpace(m *Terminal) tea.Cmd {
	if m.display.ToggleWindowFold() {
		m.display.EnsureCursorVisible()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyF(m *Terminal) tea.Cmd {
	if m.display.MoveWindowCursorToNextUserPrompt() {
		m.display.ScrollCursorToTop()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyB(m *Terminal) tea.Cmd {
	if m.display.MoveWindowCursorToPrevUserPrompt() {
		m.display.ScrollCursorToTop()
		m.display.updateContent()
	}
	return nil
}

func handleDisplayKeyE(m *Terminal) tea.Cmd {
	content := m.display.GetCursorWindowContent()
	if content != "" {
		m.display.MarkUserScrolled()
		return m.editor.OpenForDisplay(content)
	}
	return nil
}

func handleDisplayKeyCtrlF(m *Terminal) tea.Cmd {
	if historyID := m.display.GetCursorWindowHistoryID(); historyID > 0 {
		m.focusInput()
		m.input.SetValue(fmt.Sprintf(":fork %d ", historyID))
		m.input.CursorEnd()
		m.display.updateContent()
	}
	return nil
}

// ---------- display key handler map ----------

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
	keyH:         handleDisplayKeyH,
	keyL:         handleDisplayKeyL,
	keyM:         handleDisplayKeyM,
	keyG:         func(m *Terminal) tea.Cmd { gotoBottom(m); return nil },
	keyEnd:       func(m *Terminal) tea.Cmd { gotoBottom(m); return nil },
	keyGSmall:    func(m *Terminal) tea.Cmd { gotoTop(m); return nil },
	keyHome:      func(m *Terminal) tea.Cmd { gotoTop(m); return nil },
	keyColon:     handleDisplayKeyColon,
	keySpace:     handleDisplayKeySpace,
	keyF:         handleDisplayKeyF,
	keyB:         handleDisplayKeyB,
	keyE:         handleDisplayKeyE,
	keyCtrlF:     handleDisplayKeyCtrlF,
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
		return m.handleSaveKey(), true

	case keyCtrlL:
		m.openModelSelector()
		return nil, true

	case keyCtrlP:
		m.openThemeSelector()
		return nil, true

	case keyCtrlQ:
		return nil, true

	case keyCtrlR:
		return m.handleRedraw(), true

	case keyCtrlH, keyF1:
		m.openHelpWindow()
		return nil, true
	}

	return nil, false
}

// handleSaveKey handles the Ctrl+S save shortcut.
// If no session file is bound, it focuses the input and inserts ":save "
// so the user can type a filename (same pattern as Ctrl+F for :fork).
// If a session file is bound, it submits the save command directly.
func (m *Terminal) handleSaveKey() tea.Cmd {
	if m.appConfig.Cfg.Session == "" {
		m.focusInput()
		m.input.SetValue(":save ")
		m.input.CursorEnd()
		m.display.updateContent()
		return nil
	}
	return m.submitCommand("save", false)
}

// handleRedraw handles the Ctrl+R force-redraw shortcut.
//
// Layer 1 (synchronous, always works): toggle forceRedraw so View()
// appends/removes an invisible SGR reset, making the view content differ
// from the last rendered frame.  This guarantees the next flush won't
// early-return.
//
// Layer 2 & 3 (best-effort, arm full repaint): tea.ClearScreen sets
// s.clear=true on the renderer; the synthetic WindowSizeMsg does the same
// via resize() and also resets Touched=nil.  If either command arrives the
// flush becomes a full clear+repaint instead of a diff.  If both are
// dropped (rare), the view change from layer 1 still ensures a diff-based
// redraw that covers every content cell.
func (m *Terminal) handleRedraw() tea.Cmd {
	m.forceRedraw++
	m.display.ForceContentDirty()
	m.display.updateContent()

	m.pendingForceRedraw = true
	return tea.Batch(
		tea.ClearScreen,
		func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.windowWidth, Height: m.windowHeight}
		},
	)
}

// handleFallback handles any key not consumed by display or global handlers.
func (m *Terminal) handleFallback(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Don't touch the input field when user is navigating the display
	if m.overlays.RestoreFocus() != focusInput {
		return m, nil
	}

	switch msg.String() {
	case keyEnter:
		return m, m.handleSubmit()
	case keyCtrlO:
		return m, m.OpenEditor()
	case keyCtrlC:
		m.input.SetValue("")
		return m, nil
	}

	m.input.updateFromMsg(msg)
	return m, nil
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit processes the input when Shift+Enter is pressed.
func (m *Terminal) handleSubmit() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())

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
	m.emitUE()
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
	selectedModel := m.overlays.ModelSelector().GetActiveModel()
	if selectedModel == nil {
		return
	}

	// Send model_set command to session
	if selectedModel.ID != 0 {
		m.emitCommand(fmt.Sprintf(":model_set %d", selectedModel.ID))
	}
}
