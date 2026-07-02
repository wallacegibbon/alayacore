package terminal

// This package implements the terminal UI adapter for AlayaCore.
// It uses Bubble Tea for the TUI framework and handles:
//   - Display of assistant output with virtual scrolling
//   - User input with external editor support
//   - Model selection and theme switching
//   - TLV protocol communication with the session

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Async Session Loading Messages
// ============================================================================

// sessionLoadedMsg is sent when the async session loading completes.
type sessionLoadedMsg struct{}

// sessionLoadingErrorMsg is sent when the async session loading fails.
type sessionLoadingErrorMsg struct {
	err error
}

// emitCommand sends a user-level command to the session via TLV.
// Errors are silently ignored — commands are best-effort and the
// session may close the input stream at any time.
func (m *Terminal) emitCommand(cmd string) {
	_ = stream.WriteTLV(m.streamInput, stream.TagUserT, cmd)
}

// emitUE sends a TagUserEnd frame, flushing any staged content
// as a complete user message.
func (m *Terminal) emitUE() {
	_ = stream.WriteTLV(m.streamInput, stream.TagUserEnd, "")
}

// ============================================================================
// Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Row allocation: input box, status bar, newlines
	InputRows  = 3
	StatusRows = 1
	LayoutGap  = 4 // input + status + newlines between sections

	// Component sizing
	InputPaddingH     = 8  // horizontal padding for input fields (border + padding both sides)
	SelectorMaxHeight = 30 // maximum height for model selector and similar overlays
	SelectorListRows  = 8  // content rows inside selector borders
)

// Timing constants
const (
	TickInterval    = 250 * time.Millisecond // polling during streaming
	SubmitTickDelay = 50 * time.Millisecond  // delay before first tick after submit
)

// Focus constants
const (
	focusInput   = "input"
	focusDisplay = "display"
)

// ============================================================================
// Terminal Model
// ============================================================================

// Terminal is the main Bubble Tea model that composes display, input, and status components.
// It serves as the central coordinator for the terminal UI, managing:
//   - User input and keyboard shortcuts (delegated to keybinds.go)
//   - Display updates from the agent session
//   - Model selection and switching
//   - Theme selection and switching
//   - Window focus management
type Terminal struct {
	// Core components
	out         OutputWriter
	streamInput io.WriteCloser
	appConfig   *app.Config
	editor      *Editor

	// UI components
	display      DisplayModel
	input        PromptInput
	themeManager *ThemeManager
	overlays     *OverlayManager

	// Status bar state (simplified - no separate struct)
	statusText string
	inProgress bool

	// State
	quitting           bool
	confirmFromCommand bool // tracks if cancel came from :cancel command (vs Ctrl+G)
	windowWidth        int
	windowHeight       int
	styles             *Styles
	hasFocus           bool   // tracks whether the terminal has application focus
	activeTheme        string // cached from system info updates
	appliedTheme       string // last theme name that was visually applied (for detecting session-driven changes)

	// Theme preview debouncing
	themePreviewID int // ID of the current pending theme preview

	// Force-redraw counter: incremented on Ctrl-R; View() toggles an
	// invisible SGR reset when odd so the renderer detects a changed
	// view and performs a full repaint rather than early-returning.
	forceRedraw uint64

	// pendingForceRedraw is set by handleRedraw before sending a synthetic
	// WindowSizeMsg.  handleWindowSize consumes it; the view toggle already
	// happened in handleRedraw, so resize()'s s.clear=true can take effect
	// on the same flush.
	pendingForceRedraw bool

	// Async session loading state.
	// When true, Init() kicks off the loading in a goroutine and View()
	// renders a loading screen instead of the normal TUI.
	loading      bool
	loadingError error
}

// NewTerminalWithTheme creates a new Terminal model with a custom theme.
// themeName is the name of the initial theme (used for tracking session-driven theme changes).
func NewTerminalWithTheme(
	out OutputWriter,
	inputWriter io.WriteCloser,
	appCfg *app.Config,
	initialWidth, initialHeight int,
	theme *theme.Theme,
	themeManager *ThemeManager,
	themeName string,
) *Terminal {
	styles := NewStyles(theme)

	editor := NewEditor()

	modelSelector := NewModelSelector(styles)
	themeSelector := NewThemeSelector(styles)
	helpWindow := NewHelpWindow(styles)
	confirmOverlay := NewConfirmDialog(styles)
	mcpInitOverlay := NewConfirmDialog(styles)
	overlays := NewOverlayManager(modelSelector, themeSelector, helpWindow, confirmOverlay, mcpInitOverlay, styles)

	m := &Terminal{
		out:          out,
		streamInput:  inputWriter,
		appConfig:    appCfg,
		editor:       editor,
		display:      NewDisplayModel(out.WindowBuffer(), styles),
		input:        NewPromptInput(styles),
		themeManager: themeManager,
		overlays:     overlays,
		windowWidth:  initialWidth,
		windowHeight: initialHeight,
		styles:       styles,
		hasFocus:     true,
		activeTheme:  themeName,
		appliedTheme: themeName,
	}

	// Initialize component widths
	m.display.SetWidth(initialWidth)
	m.input.SetWidth(initialWidth)
	m.overlays.SetSize(initialWidth, initialHeight)
	m.overlays.SetFocusedWindow(focusInput)
	m.updateDisplayHeight()

	return m
}

// Init starts the periodic tick loop for processing session updates.
// When loading is true, it also kicks off async session loading.
func (m *Terminal) Init() tea.Cmd {
	// Display any buffered warnings from initialization
	if m.themeManager != nil {
		if warnings := m.themeManager.GetWarnings(); len(warnings) > 0 {
			for _, w := range warnings {
				m.out.WriteError("%s", w.Message)
			}
		}
	}

	cmds := []tea.Cmd{
		tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
			return tickMsg{}
		}),
	}

	// If in loading mode, kick off async session loading.
	if m.loading {
		cmds = append(cmds, m.loadSessionCmd())
	}

	return tea.Batch(cmds...)
}

// loadSessionCmd returns a tea.Cmd that runs app.StartSession in a goroutine.
// It is only used when the TUI starts in loading mode (m.loading == true).
// The session is nil, the input buffer already exists as m.streamInput.
func (m *Terminal) loadSessionCmd() tea.Cmd {
	return func() tea.Msg {
		// streamInput is always a *stream.SliceBuffer in production.
		input, ok := m.streamInput.(*stream.SliceBuffer)
		if !ok {
			return sessionLoadingErrorMsg{
				err: fmt.Errorf("internal: streamInput is not a SliceBuffer"),
			}
		}

		_, _, err := app.StartSession(m.appConfig, m.out, input)
		if err != nil {
			return sessionLoadingErrorMsg{err: err}
		}
		return sessionLoadedMsg{}
	}
}

// Update handles all incoming messages and routes them to appropriate handlers.
// Messages are processed in order of priority:
//  1. KeyMsg - keyboard input (highest priority for responsiveness)
//  2. WindowSizeMsg - terminal resize
//  3. tickMsg - periodic updates for display and model switching
//  4. Editor messages - external editor completion
//  5. Focus/Blur - application focus changes
//  6. Paste - clipboard paste
func (m *Terminal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Loading message handling — these take priority during startup.
	switch msg := msg.(type) {
	case sessionLoadedMsg:
		m.loading = false
		// The session's sendSystemInfo("all") was already written to the
		// WindowBuffer during loading. Update the display to reflect it.
		m.out.DrainDirty()
		if m.out.WindowBuffer().WindowCount() > 0 {
			m.updateStatus()
			m.updateDisplayHeight()
			if m.display.shouldFollow() {
				m.display.SetCursorToLastWindow()
			}
			m.display.updateContent()
		}
		// Sync theme from the now-loaded session state.
		snap := m.out.SnapshotStatus()
		m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)
		// Populate model selector from the now-loaded model list.
		// sendSystemInfo("all") already wrote SM "model_list" to the
		// output buffer during loading, so SnapshotModels is populated.
		modelSnap := m.out.SnapshotModels()
		return m, m.overlays.ModelSelector().LoadModels(modelSnap.Models, modelSnap.ActiveID)

	case sessionLoadingErrorMsg:
		m.loading = false
		m.loadingError = msg.err
		m.quitting = true
		return m, tea.Quit
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tickMsg:
		return m.handleTick()

	case themePreviewMsg:
		return m.handleThemePreview(msg)

	case editorStartMsg:
		return m.handleEditorStart(msg)

	case EditorFinishedMsg:
		return m.handleEditorFinished(msg)

	case tea.BlurMsg:
		return m.handleBlur()

	case tea.FocusMsg:
		return m.handleFocus()
	}

	// Default: pass to prompt input
	m.input.updateFromMsg(msg)
	return m, nil
}

// tickMsg is sent periodically to update the display.
type tickMsg struct{}

// handleWindowSize handles terminal resize events.
func (m *Terminal) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.windowWidth = msg.Width
	m.windowHeight = msg.Height

	// Update all components
	m.out.SetWindowWidth(max(0, msg.Width))
	m.display.SetWidth(max(0, msg.Width))
	m.input.SetWidth(max(0, msg.Width))
	m.overlays.SetSize(msg.Width, msg.Height)
	m.updateDisplayHeight()

	// Clamp cursor to valid bounds (windows may have been removed) but
	// don't scroll to make it visible — the user's scroll position is
	// preserved across resizes and suspend/resume cycles.
	m.display.ClampCursor()

	// If this is a synthetic resize triggered by Ctrl-R, consume the flag.
	// The view toggle already happened in handleRedraw, and resize() just
	// armed the renderer's clear flag — the next flush will do a full
	// clear+repaint.
	if m.pendingForceRedraw {
		m.pendingForceRedraw = false
	}

	// Re-render display content with new width (windowBuffer was marked dirty by SetWindowWidth)
	m.display.updateContent()

	return m, nil
}

// handleTick processes periodic updates for display and model switching.
func (m *Terminal) handleTick() (tea.Model, tea.Cmd) {
	// During async loading, the only periodic task is to re-render the
	// loading screen (spinner animation). Skip all session-driven updates.
	if m.loading {
		return m, tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
			return tickMsg{}
		})
	}

	m.handleMCPOverlays()
	cmd := m.handleDisplayRefresh()
	return m, tea.Batch(
		tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
			return tickMsg{}
		}),
		cmd,
	)
}

// handleMCPOverlays manages all MCP overlay state in one place.
// Called on every tick.
//
// The init overlay (mcpInitOverlay) persists throughout MCP init.
// The confirm overlay (confirmOverlay) handles auth confirm and tool
// confirm as temporary dialogs on top of the init overlay.
func (m *Terminal) handleMCPOverlays() {
	action := m.overlays.HandleMCPProgress(m.out)
	if action.CloseInitOverlay {
		m.restoreFocusAfterConfirm()
	}
	if action.InitOverlayActive || action.OpenedConfirm {
		m.display.updateContent()
	}
}

// handleDisplayRefresh checks if the display needs updating and returns
// a tea.Cmd for model selector updates if models changed.
func (m *Terminal) handleDisplayRefresh() tea.Cmd {
	if !m.out.DrainDirty() {
		m.updateStatus()
		return nil
	}

	if m.out.WindowBuffer().WindowCount() > 0 {
		m.updateStatus()
		m.updateDisplayHeight()
		if m.display.shouldFollow() {
			m.display.SetCursorToLastWindow()
		}
		m.display.updateContent()
	}

	modelSnap := m.out.SnapshotModels()
	return m.overlays.ModelSelector().LoadModels(modelSnap.Models, modelSnap.ActiveID)
}

// handleEditorFinished handles completion of the external editor.
// Dispatches based on the EditorAction to handle different editor scenarios:
//
//   - EditorActionNone:          view-only (display), no side effects
//   - EditorActionSubmit:        submit content as user input
//   - EditorActionReloadConfig:  reload configuration after file edit
func (m *Terminal) handleEditorFinished(msg EditorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.out.WriteError("Editor error: %v", msg.Err)
		return m, nil
	}

	switch msg.Action {
	case EditorActionNone:
		// View-only (display window viewing) — nothing to do
		return m, nil

	case EditorActionSubmit:
		if msg.Content != "" {
			// Strip trailing newlines that text editors add by default.
			content := strings.TrimRight(msg.Content, "\n")
			m.input.SetValue(content)
			m.input.CursorEnd()
			m.focusInput()
		}
		return m, nil

	case EditorActionReloadConfig:
		if msg.FileType == "model_config" {
			m.emitCommand(":model_load")
		}
		return m, nil

	default:
		return m, nil
	}
}

// updateDisplayHeight updates the display viewport height based on window size.
func (m *Terminal) updateDisplayHeight() {
	m.display.UpdateHeight(m.windowHeight)
}

// updateStatus updates the status bar state from the output writer.
func (m *Terminal) syncThemeFromSession(sessionTheme string, themeData *theme.Theme) {
	if m.appliedTheme == sessionTheme || sessionTheme == "" {
		return
	}
	if themeData != nil {
		m.applyTheme(themeData)
	} else if m.themeManager != nil {
		t := m.themeManager.LoadTheme(sessionTheme)
		m.applyTheme(t)
	}
	m.appliedTheme = sessionTheme
}

// View renders the complete terminal UI.
func (m *Terminal) View() tea.View {
	// Loading screen: shown while the session is being loaded asynchronously.
	if m.loading {
		return m.renderLoadingView()
	}
	if m.loadingError != nil {
		// Should not normally be reached since we quit on error,
		// but provide a fallback view just in case.
		v := tea.NewView(fmt.Sprintf("Session loading failed: %v\n", m.loadingError))
		v.AltScreen = true
		return v
	}

	var sb strings.Builder

	// Display area
	sb.WriteString(m.display.View().Content)
	sb.WriteString("\n")

	// Input area (empty border when confirm overlay blocks input)
	sb.WriteString(m.input.RenderWithBorder(m.overlays.IsConfirmOpen()))

	// Status bar (simplified - just render directly)
	sb.WriteString("\n")
	sb.WriteString(m.renderStatusBar())

	baseContent := sb.String()

	// Render all overlay layers through the overlay manager.
	overlayContent, hasConfirm := m.overlays.Render(baseContent, m.windowWidth, m.windowHeight, m.forceRedraw&1 == 1)

	if hasConfirm {
		v := tea.NewView(overlayContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	v := tea.NewView(overlayContent)
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

// renderLoadingView renders the loading screen shown while the session is
// being loaded asynchronously. It displays a centered message with a simple
// spinner animation (updated by tickMsg) so the user gets instant feedback
// even on slow machines.
func (m *Terminal) renderLoadingView() tea.View {
	// Simple spinner frames.
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := int(time.Now().UnixMilli()/150) % len(spinner)
	spinnerChar := spinner[frame]

	msg := fmt.Sprintf(" %s Loading session...", spinnerChar)
	// Center the message vertically and horizontally (ASCII width ~= len).
	contentWidth := len(msg)
	padX := max(0, (m.windowWidth-contentWidth)/2)
	padY := max(0, m.windowHeight/2-1)

	var sb strings.Builder
	sb.WriteString(strings.Repeat("\n", padY))
	sb.WriteString(strings.Repeat(" ", padX))
	sb.WriteString(msg)
	sb.WriteString("\n")

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}

// formatTokenCount returns a compact human-readable representation of a
// token count (e.g. 1500 → "1.5K", 1000000 → "1M").
var _ tea.Model = (*Terminal)(nil)

// ============================================================================
// Focus Management
// ============================================================================

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

// applyTheme applies a new theme to all UI components.
func (m *Terminal) applyTheme(theme *theme.Theme) {
	m.styles = NewStyles(theme)
	m.out.SetStyles(m.styles)
	m.display.SetStyles(m.styles)
	m.input.SetStyles(m.styles)
	m.overlays.SetStyles(m.styles)
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
