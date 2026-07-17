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
func (m Terminal) handleKeyMsg(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	// During async session loading, ignore all keyboard input.
	if m.loading {
		return m, nil
	}

	// Ctrl+Z works from any context, including overlays
	if msg.String() == keyCtrlZ {
		return m, tea.Suspend
	}

	// Priority overlays (confirm, MCP init)
	if tm, cmd, handled := m.handlePriorityOverlayKeys(msg); handled {
		return tm, cmd
	}

	// Selector overlays (theme, model, attachment, help)
	if tm, cmd, handled := m.handleSelectorOverlayKeys(msg); handled {
		return tm, cmd
	}

	// Tab toggles focus between display and input
	if msg.String() == keyTab {
		m = m.toggleFocus()
		return m, nil
	}

	// 7. Display-specific keys when display is focused
	if m.overlays.RestoreFocus() == focusDisplay {
		if tm, cmd, handled := m.handleDisplayKeys(msg); handled {
			return tm, cmd
		}
	}

	// 8. Global shortcuts (work from any context)
	if tm, cmd, handled := m.handleGlobalKeys(msg); handled {
		return tm, cmd
	}

	// 9. Fallback: pass to input field (no-op when display focused)
	return m.handleFallback(msg)
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m Terminal) handleThemeSelectorKeys(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	// Check if it's a reload request
	if msg.String() == keyR && m.themeManager != nil {
		m.themeManager.ReloadThemes()
		m.overlays.ThemeSelector().Open(m.themeManager.GetThemes(), m.activeTheme)
		return m, nil
	}

	// Track if selector was open before handling key
	wasOpen := m.overlays.ThemeSelector().IsOpen()

	ts, result := m.overlays.ThemeSelector().HandleKeyMsg(msg, m.themeManager)
	m.overlays = m.overlays.WithThemeSelector(ts)

	// Check if theme was selected (Enter key)
	if result.ThemeSelected {
		selectedTheme := ts.GetSelectedTheme()
		if selectedTheme != nil {
			m.emitCommand(":theme_set " + selectedTheme.Name)
		}
		m = m.restoreFocus()
		return m, nil
	}

	// If selector closed without selection (ESC/q), restore original theme
	if wasOpen && !ts.IsOpen() {
		originalThemeName := ts.GetOriginalThemeName()
		originalTheme := m.themeManager.LoadTheme(originalThemeName)
		m = m.applyTheme(originalTheme)
		m = m.restoreFocus()
		return m, nil
	}

	// Apply preview theme if changed
	if result.PreviewTheme != nil {
		// For navigation keys, delay theme application to keep cursor responsive
		key := msg.String()
		if key == keyJ || key == keyK || key == keyUp || key == keyDown {
			// Increment the ID to invalidate any pending preview
			m.themePreviewID++
			// Capture the current ID to check if this preview is still valid
			id := m.themePreviewID
			theme := result.PreviewTheme

			// Return a command that will apply the theme after a delay
			// This allows the cursor to update immediately
			return m, tea.Tick(ThemePreviewDebounce, func(_ time.Time) tea.Msg {
				return themePreviewMsg{theme: theme, id: id}
			})
		}
		// Apply immediately for other keys
		m = m.applyTheme(result.PreviewTheme)
	}

	return m, nil
}

// themePreviewMsg is sent when a theme preview should be applied
type themePreviewMsg struct {
	theme *theme.Theme
	id    int // ID to check if this preview is still current
}

func (m Terminal) handleThemePreview(msg themePreviewMsg) Terminal {
	// Only apply if this preview is still the current one (debouncing)
	if msg.id == m.themePreviewID {
		m = m.applyTheme(msg.theme)
	}
	return m
}

func (m Terminal) handleConfirmResult(cd ConfirmDialog, update ConfirmDialogUpdate) (Terminal, tea.Cmd) {
	r := update.Result
	m.overlays = m.overlays.WithConfirmOverlay(cd)
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

func (m Terminal) handleConfirmQuit(r *ConfirmResult, fromCmd bool) (Terminal, tea.Cmd) {
	if r.Confirmed {
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return m, tea.Quit
	}
	if fromCmd {
		m.input = m.input.WithValue("")
	}
	m = m.restoreFocusAfterConfirm()
	return m, nil
}

func (m Terminal) handleConfirmCancel(r *ConfirmResult, fromCmd bool) (Terminal, tea.Cmd) {
	if fromCmd {
		m.input = m.input.WithValue("")
	}
	m = m.restoreFocusAfterConfirm()
	if r.Confirmed {
		return m.submitCommand("cancel", fromCmd)
	}
	return m, nil
}

func (m Terminal) handleConfirmTool(r *ConfirmResult, fromCmd bool) (Terminal, tea.Cmd) {
	action := "no"
	if r.Confirmed {
		action = "yes"
	}
	if fromCmd {
		m.input = m.input.WithValue("")
	}
	m.emitCommand(":confirm " + r.ToolID + " " + action)
	m = m.restoreFocusAfterConfirm()
	if nextID, nextName, nextInput, ok := m.out.GetPendingToolConfirm(); ok {
		m = m.openConfirmTool(nextID, nextName, nextInput)
	}
	return m, scheduleTick()
}

func (m Terminal) handleConfirmMCPAuth(r *ConfirmResult, fromCmd bool) (Terminal, tea.Cmd) {
	switch {
	case r.Confirmed:
		m.startMCPAuthFlow(r.ToolID, r.ToolInput)
	case r.CtrlGCanceled:
		m.out.ClearMCPAuths()
		m.emitCommand(":mcp_cancel")
	default:
		if fromCmd {
			m.input = m.input.WithValue("")
		}
		m.emitCommand(":mcp_auth " + r.ToolID)
	}
	m = m.restoreFocusAfterConfirm()
	return m, scheduleTick()
}

// startMCPAuthFlow starts the OAuth callback server, opens the browser,
// and waits for the authorization code in a background goroutine.
func (m Terminal) startMCPAuthFlow(serverName, authURL string) {
	state := platform.RandomState()

	resultCh, redirectURI, cleanup := platform.StartCallbackServer("127.0.0.1:0", state, serverName)

	encodedRedirect := url.QueryEscape(redirectURI)
	filledURL := authURL
	filledURL = strings.ReplaceAll(filledURL, "{{redirect_uri}}", encodedRedirect)
	filledURL = strings.ReplaceAll(filledURL, "{{state}}", state)

	m.out.WriteNotify(fmt.Sprintf("Authorizing %s. If your browser doesn't open, open this URL:\n%s",
		serverName, filledURL))

	if err := platform.OpenURL(filledURL); err != nil {
		m.out.WriteError("Failed to open browser: %v", err)
	}

	go func() {
		res := <-resultCh
		cleanup()
		if res.Err != nil {
			m.out.WriteError("MCP auth callback error: %v", res.Err)
			m.emitCommand(":mcp_cancel")
			return
		}
		m.emitCommand(fmt.Sprintf(":mcp_auth %s %s %s",
			serverName, res.Code, redirectURI))
	}()
}

// restoreFocusAfterConfirm restores input/display focus only if no overlay
// is still open. If another overlay (e.g. model selector) was active before
// the confirm appeared, it remains active — the overlay naturally catches
// keys in handleKeyMsg.
func (m Terminal) restoreFocusAfterConfirm() Terminal {
	if m.overlays.ModelSelector().IsOpen() || m.overlays.ThemeSelector().IsOpen() ||
		m.overlays.HelpWindow().IsOpen() || m.overlays.IsMCPInitOpen() {
		m.display = m.display.updateContent()
		return m
	}
	m = m.restoreFocus()
	return m
}

// handleOverlayModelSelector handles keyboard input when the model selector is open.
func (m Terminal) handleOverlayModelSelector(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	wasOpen := m.overlays.ModelSelector().IsOpen()
	ms, result := m.overlays.ModelSelector().HandleKeyMsg(msg)
	m.overlays = m.overlays.WithModelSelector(ms)

	if result.ModelSelected {
		m = m.switchToSelectedModel()
	}
	if result.ReloadModels {
		m.emitCommand(":model_load")
	}
	if wasOpen && !ms.IsOpen() {
		m = m.restoreFocus()
	}
	return m, result.Cmd
}

// handleMCPInitKeys handles keyboard input when the MCP init overlay is open.
func (m Terminal) handleMCPInitKeys(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	if msg.String() == keyCtrlG {
		m.emitCommand(":mcp_cancel")
		return m, scheduleTick()
	}
	return m, nil
}

// handlePriorityOverlayKeys handles the highest-priority overlays that
// block all other interaction (confirm dialog, MCP init overlay).
func (m Terminal) handlePriorityOverlayKeys(msg tea.KeyMsg) (Terminal, tea.Cmd, bool) {
	if m.overlays.ConfirmOverlay().IsOpen() {
		tm, cmd := m.handleOverlayConfirm(msg)
		return tm, cmd, true
	}
	if m.overlays.MCPInitOverlay().IsOpen() {
		tm, cmd := m.handleMCPInitKeys(msg)
		return tm, cmd, true
	}
	return m, nil, false
}

// handleSelectorOverlayKeys handles selector-style overlays (theme, model,
// attachment, help) that are mutually exclusive.
func (m Terminal) handleSelectorOverlayKeys(msg tea.KeyMsg) (Terminal, tea.Cmd, bool) {
	if m.overlays.ThemeSelector().IsOpen() {
		tm, cmd := m.handleThemeSelectorKeys(msg)
		return tm, cmd, true
	}
	if m.overlays.ModelSelector().IsOpen() {
		tm, cmd := m.handleOverlayModelSelector(msg)
		return tm, cmd, true
	}
	if m.overlays.AttachmentWindow().IsOpen() {
		aw := m.overlays.AttachmentWindow()
		t := trackOverlay(aw)
		aw, cmd := aw.HandleKeyMsg(msg)
		m.overlays = m.overlays.WithAttachmentWindow(aw)
		if t.JustClosed(aw) {
			if path := aw.SelectedPath(); path != "" {
				if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
					m = m.addURLAttachment(path)
				} else {
					m = m.addAttachment(path)
				}
			}
			m = m.restoreFocus()
		}
		return m, cmd, true
	}
	if m.overlays.HelpWindow().IsOpen() {
		hw := m.overlays.HelpWindow()
		t := trackOverlay(hw)
		hw, result := hw.HandleKeyMsg(msg)
		m.overlays = m.overlays.WithHelpWindow(hw)
		if t.JustClosed(hw) {
			if result.PendingCommand != "" {
				m = m.focusInput()
				m.input = m.input.WithValue(result.PendingCommand + " ")
				m.input = m.input.CursorEnd()
				m.display = m.display.updateContent()
				return m, nil, true
			}
			m = m.restoreFocus()
		}
		return m, result.Cmd, true
	}
	return m, nil, false
}

// handleOverlayConfirm handles keyboard input when the confirm dialog is open.
func (m Terminal) handleOverlayConfirm(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	// 'e' opens the full tool input in an external editor for inspection
	// (view-only — dialog stays open after editor closes)
	if msg.String() == keyE && m.overlays.ConfirmOverlay().Kind() == ConfirmTool && m.overlays.ConfirmOverlay().ToolInput() != "" {
		content := m.overlays.ConfirmOverlay().ToolInput()
		if toolName := m.overlays.ConfirmOverlay().ToolName(); toolName != "" && strings.HasPrefix(content, toolName+": ") {
			content = content[len(toolName)+2:]
		}
		return m, m.editor.OpenForDisplay(content)
	}
	if cd, update := m.overlays.ConfirmOverlay().HandleKeyMsg(msg); update.Handled {
		return m.handleConfirmResult(cd, update)
	}
	return m, nil
}

// Display key handler helpers (shared between multiple keys).
// These don't return tea.Cmd — the map type doesn't require it.

// handleDisplayKeys handles key events when display window is focused.
//
// IMPORTANT: When moving the cursor, always call a scroll method
// (EnsureCursorVisible() or ScrollCursorToTop()) BEFORE updateContent().
// This ensures the viewport position is updated before content is
// regenerated, preventing blank areas in the virtual rendering.
//
//nolint:gocyclo
func (m Terminal) handleDisplayKeys(msg tea.KeyMsg) (Terminal, tea.Cmd, bool) {
	switch msg.String() {
	case keyJ, keyDown:
		m.display, _ = m.display.MoveWindowCursorDown()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyK, keyUp:
		m.display, _ = m.display.MoveWindowCursorUp()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyCtrlD, keyPgDown:
		if !m.display.AtBottom() {
			m.display = m.display.MarkUserScrolled()
			m.display = m.display.ScrollDown(max(1, m.display.GetHeight()/2))
			m.display = m.display.updateContent()
		}
		return m, nil, true

	case keyCtrlU, keyPgUp:
		m.display = m.display.MarkUserScrolled()
		m.display = m.display.ScrollUp(max(1, m.display.GetHeight()/2))
		m.display = m.display.updateContent()
		return m, nil, true

	case keyJCapital, keyShiftDown:
		if !m.display.AtBottom() {
			m.display = m.display.MarkUserScrolled()
			m.display = m.display.ScrollDown(1)
			m.display = m.display.updateContent()
		}
		return m, nil, true

	case keyKCapital, keyShiftUp:
		m.display = m.display.MarkUserScrolled()
		m.display = m.display.ScrollUp(1)
		m.display = m.display.updateContent()
		return m, nil, true

	case keyG, keyEnd:
		m.display = m.display.WithCursorToLastWindow()
		m.display = m.display.GotoBottom()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyGSmall, keyHome:
		m.display = m.display.WithWindowCursor(0)
		m.display = m.display.GotoTop()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyH:
		m.display, _ = m.display.MoveWindowCursorToTop()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyL:
		m.display, _ = m.display.MoveWindowCursorToBottom()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyM:
		m.display, _ = m.display.MoveWindowCursorToCenter()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyColon:
		m = m.focusInput()
		m.input = m.input.WithValue(keyColon)
		m.input = m.input.CursorEnd()
		m.display = m.display.updateContent()
		return m, nil, true

	case keySpace:
		m.display, _ = m.display.ToggleWindowFold()
		m.display = m.display.EnsureCursorVisible()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyF:
		m.display, _ = m.display.MoveWindowCursorToNextUserPrompt()
		m.display = m.display.ScrollCursorToTop()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyB:
		m.display, _ = m.display.MoveWindowCursorToPrevUserPrompt()
		m.display = m.display.ScrollCursorToTop()
		m.display = m.display.updateContent()
		return m, nil, true

	case keyE:
		content := m.display.GetCursorWindowContent()
		if content != "" {
			m.display = m.display.MarkUserScrolled()
			return m, m.editor.OpenForDisplay(content), true
		}
		return m, nil, true

	case keyCtrlF:
		if historyID := m.display.GetCursorWindowHistoryID(); historyID > 0 {
			m = m.focusInput()
			m.input = m.input.WithValue(fmt.Sprintf(":fork %d ", historyID))
			m.input = m.input.CursorEnd()
			m.display = m.display.updateContent()
		}
		return m, nil, true
	}

	return m, nil, false
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m Terminal) handleGlobalKeys(msg tea.KeyMsg) (Terminal, tea.Cmd, bool) {
	switch msg.String() {
	case keyCtrlG:
		m = m.openConfirmCancel()
		m.confirmFromCommand = false
		return m, nil, true

	case keyCtrlS:
		tm, cmd := m.handleSaveKey()
		return tm, cmd, true

	case keyCtrlL:
		m = m.openModelSelector()
		return m, nil, true

	case keyCtrlP:
		m = m.openThemeSelector()
		return m, nil, true

	case keyCtrlQ:
		return m, nil, true

	case keyCtrlR:
		tm, cmd := m.handleRedraw()
		return tm, cmd, true

	case keyCtrlH, keyF1:
		m = m.openHelpWindow()
		return m, nil, true
	}

	return m, nil, false
}

// handleSaveKey handles the Ctrl+S save shortcut.
// If no session file is bound, it focuses the input and inserts ":save "
// so the user can type a filename (same pattern as Ctrl+F for :fork).
// If a session file is bound, it submits the save command directly.
func (m Terminal) handleSaveKey() (Terminal, tea.Cmd) {
	if m.appConfig.Cfg.Session == "" {
		m = m.focusInput()
		m.input = m.input.WithValue(":save ")
		m.input = m.input.CursorEnd()
		m.display = m.display.updateContent()
		return m, nil
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
func (m Terminal) handleRedraw() (Terminal, tea.Cmd) {
	m.forceRedraw++
	m.display = m.display.ForceContentDirty()
	m.display = m.display.updateContent()

	m.pendingForceRedraw = true
	return m, tea.Batch(
		tea.ClearScreen,
		func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.windowWidth, Height: m.windowHeight}
		},
	)
}

// handleFallback handles any key not consumed by display or global handlers.
func (m Terminal) handleFallback(msg tea.KeyMsg) (Terminal, tea.Cmd) {
	// Don't touch the input field when user is navigating the display
	if m.overlays.RestoreFocus() != focusInput {
		return m, nil
	}

	switch msg.String() {
	case keyEnter:
		return m.handleSubmit()
	case keyCtrlO:
		return m, m.OpenEditor()
	case keyCtrlA:
		m = m.openAttachmentWindow()
		return m, nil
	case keyCtrlC:
		m.input = m.input.WithValue("")
		m = m.clearAttachments()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit processes the input when Shift+Enter is pressed.
func (m Terminal) handleSubmit() (Terminal, tea.Cmd) {
	prompt := strings.TrimSpace(m.input.Value())

	// Check if it's a command (starts with ":") — ignore attachments for commands.
	if command, found := strings.CutPrefix(prompt, ":"); found {
		return m.handleCommand(strings.TrimSpace(command))
	}

	// If a task is running, reject without clearing input.
	if m.inProgress {
		m.out.WriteError("A task is already running. Wait for it to complete or cancel it.")
		return m, nil
	}

	// Nothing to send
	if prompt == "" && len(m.pendingAttachments) == 0 {
		return m, nil
	}

	// Capture resources, clear state, return Cmd for I/O
	attachments := m.pendingAttachments
	writer := m.streamInput
	out := m.out
	m.input = m.input.WithValue("")
	m = m.clearAttachments()

	return m, tea.Batch(
		submitCmd(writer, out, attachments, prompt),
		scheduleTick(),
	)
}

// handleCommand processes a command string (without the ":" prefix).
func (m Terminal) handleCommand(command string) (Terminal, tea.Cmd) {
	// Quit command
	if command == cmdQuit || command == cmdQShort {
		m = m.openConfirmQuit()
		m.confirmFromCommand = true
		return m, nil
	}

	// Cancel command
	if command == cmdCancel {
		m = m.openConfirmCancel()
		m.confirmFromCommand = true
		return m, nil
	}

	// Suspend command - suspends the process (like Ctrl+Z)
	if command == cmdSuspend {
		m.input = m.input.WithValue("")
		return m, tea.Suspend
	}

	// Help command - opens help window locally, not sent to session
	if command == cmdHelp {
		m.input = m.input.WithValue("")
		m = m.openHelpWindow()
		return m, nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m Terminal) submitCommand(command string, clearInput bool) (Terminal, tea.Cmd) {
	m.emitCommand(":" + command)
	if clearInput {
		m.input = m.input.WithValue("")
	}
	return m, scheduleTick()
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() tea.Cmd {
	return tea.Tick(SubmitTickDelay, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// switchToSelectedModel sends a model_set command to switch to the selected model.
func (m Terminal) switchToSelectedModel() Terminal {
	selectedModel := m.overlays.ModelSelector().GetActiveModel()
	if selectedModel == nil {
		return m
	}

	// Send model_set command to session
	if selectedModel.ID != 0 {
		m.emitCommand(fmt.Sprintf(":model_set %d", selectedModel.ID))
	}
	return m
}
