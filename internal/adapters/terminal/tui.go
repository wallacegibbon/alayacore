package terminal

// This package implements the terminal UI adapter for AlayaCore.
// It uses Bubble Tea for the TUI framework and handles:
//   - Display of assistant output with virtual scrolling
//   - User input with external editor support
//   - Model selection and theme switching
//   - TLV protocol communication with the session

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
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
	_ = tlv.WriteTLV(m.streamInput, tlv.TagUserT, cmd)
}

// emitUE sends a TagUserEnd frame, flushing any staged content
// as a complete user message.
func (m *Terminal) emitUE() {
	_ = tlv.WriteTLV(m.streamInput, tlv.TagUserEnd, "")
}

// emitAttachments reads pending attachment files and sends them as TLV frames.
// Failed files are skipped with an error message shown to the user.
// URLs are sent as-is without reading.
func (m *Terminal) emitAttachments() {
	for _, a := range m.pendingAttachments {
		var value string
		if a.isURL {
			value = a.path
		} else {
			data, err := os.ReadFile(a.path)
			if err != nil {
				m.out.WriteError("Failed to read attachment: %s", err)
				continue
			}
			mime := tlv.MimeTypeForPath(a.path)
			b64 := base64.StdEncoding.EncodeToString(data)
			value = fmt.Sprintf("data:%s;base64,%s", mime, b64)
		}
		_ = tlv.WriteTLV(m.streamInput, a.tag, value)
	}
}

// addAttachment adds a local file path to pending attachments.
func (m *Terminal) addAttachment(path string) {
	tag := tlv.TagForPath(path)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: path, tag: tag})
	m.input.SetAttachments(m.pendingAttachmentLabels())
	m.updateDisplayHeight()
}

// addURLAttachment adds a URL to pending attachments.
func (m *Terminal) addURLAttachment(url string) {
	tag := tlv.TagForPath(url)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: url, tag: tag, isURL: true})
	m.input.SetAttachments(m.pendingAttachmentLabels())
	m.updateDisplayHeight()
}

// clearAttachments clears all pending attachments.
func (m *Terminal) clearAttachments() {
	m.pendingAttachments = nil
	m.input.SetAttachments(nil)
	m.updateDisplayHeight()
}

// pendingAttachmentLabels returns display labels for all pending attachments.
func (m *Terminal) pendingAttachmentLabels() []string {
	labels := make([]string, len(m.pendingAttachments))
	for i, a := range m.pendingAttachments {
		labels[i] = tlv.MediaLabel(a.tag)
	}
	return labels
}

// ============================================================================
// Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Component sizing
	InputPaddingH     = 8  // horizontal padding for input fields (border + padding both sides)
	SelectorMaxHeight = 30 // maximum height for model selector and similar overlays
	SelectorListRows  = 8  // content rows inside selector borders
	LayoutGap         = 4  // non-content rows subtracted for selector/list sizing
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
	pipeReader  *io.PipeReader // read end of the input pipe; set before async loading
	appConfig   *app.Config
	editor      *Editor

	// UI components
	display      DisplayModel
	input        PromptInput
	themeManager *ThemeManager
	overlays     *OverlayManager

	// Status bar state (simplified - no separate struct)
	statusText    string
	statusTextDim string // dimmed version of statusText for inactive focus
	inProgress    bool

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

	// Pending attachments for multi-modal input.
	pendingAttachments []attachment

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

	// postLoading is true after the session finishes loading but before the
	// first tick has a chance to check whether MCP init is needed.  During
	// this brief window the input border is blocked (rendered as an empty
	// box) so it doesn't appear focused while the MCP init overlay may open.
	postLoading bool
}

// attachment represents a pending file attachment for multi-modal input.
type attachment struct {
	path  string // file path or URL
	tag   string // TLV tag: TagUserI, TagUserV, TagUserA, or TagUserD
	isURL bool   // true if it's a URL (sent as-is, no file reading)
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
	attachmentWindow := NewAttachmentWindow(styles)
	overlays := NewOverlayManager(modelSelector, themeSelector, helpWindow, confirmOverlay, mcpInitOverlay, attachmentWindow, styles)

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
	// Display any buffered init errors from initialization
	if m.themeManager != nil {
		if errs := m.themeManager.GetInitErrors(); len(errs) > 0 {
			for _, e := range errs {
				m.out.WriteError("%s", e.Message)
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
func (m *Terminal) loadSessionCmd() tea.Cmd {
	return func() tea.Msg {
		_, _, err := app.StartSession(m.appConfig, m.out, m.pipeReader)
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
		return m.handleSessionLoadedMsg()

	case sessionLoadingErrorMsg:
		return m.handleSessionLoadingError(msg.err)
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

	case tea.PasteMsg:
		if m.overlays.AttachmentWindow().IsOpen() {
			m.overlays.AttachmentWindow().FilterInput.Update(msg)
		} else {
			m.input.updateFromMsg(msg)
		}
		return m, nil
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

	// First tick after loading: initialization is done — restore input
	// focus if no overlay is blocking it.
	if m.postLoading {
		m.postLoading = false
		if !m.overlays.IsBlocked() {
			m.restoreFocus()
		}
	}

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
	wasOpen := m.overlays.IsMCPInitOpen()
	action := m.overlays.HandleMCPProgress(m.out)
	if action.CloseInitOverlay {
		m.restoreFocusAfterConfirm()
	}
	if action.InitOverlayActive || action.OpenedConfirm {
		// MCP init overlay just opened — blur input so its border renders
		// as blurred (empty box) rather than focused but unreachable.
		if action.InitOverlayActive && !wasOpen {
			m.input.Blur()
		}
		m.display.updateContent()
	}
}

// handleSessionLoadedMsg is called when the async session loading completes.
// It transitions the UI from the loading spinner to the normal TUI view,
// applying the loaded theme, populating the model selector, and preparing
// for MCP initialization if needed.
func (m *Terminal) handleSessionLoadedMsg() (tea.Model, tea.Cmd) {
	m.loading = false
	m.postLoading = true

	// Drain any buffered output written during loading.
	m.out.DrainDirty()

	// Reset appliedTheme so the session's theme data (from sendThemeListMsg)
	// is always applied on initial load, even if the theme name matches the
	// loading screen theme. This ensures that if the user modified a theme
	// file (e.g. theme-dark.conf), the changes take effect immediately.
	m.appliedTheme = ""

	if m.out.WindowBuffer().WindowCount() > 0 {
		m.updateStatus()
		m.updateDisplayHeight()
		if m.display.shouldFollow() {
			m.display.SetCursorToLastWindow()
		}
		m.display.updateContent()
	}

	// Sync theme from the now-loaded session state.
	// (Fallback when there are no windows yet — updateStatus above already
	// handles the normal case via syncThemeFromSession.)
	snap := m.out.SnapshotStatus()
	m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)

	// Populate model selector from the now-loaded model list.
	modelSnap := m.out.SnapshotModels()

	// Blur the input and try to open MCP init overlay immediately.
	// The input stays blurred (rendered as empty box) until the first
	// tick determines whether MCP init is needed.
	m.input.Blur()
	m.handleMCPOverlays()

	return m, m.overlays.ModelSelector().LoadModels(modelSnap.Models, modelSnap.ActiveID)
}

// handleSessionLoadingError is called when the async session loading fails.
// It transitions the UI to a quitting state with the error recorded.
func (m *Terminal) handleSessionLoadingError(err error) (tea.Model, tea.Cmd) {
	m.loading = false
	m.loadingError = err
	m.quitting = true
	return m, tea.Quit
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

// updateDisplayHeight updates the display viewport height based on window size
// and current input box height (which varies when attachments are present).
func (m *Terminal) updateDisplayHeight() {
	// Layout from bottom up:
	//   line H:          status bar (fixed, 1 line)
	//   separator:       1 newline between input and status
	//   lines above:     input box (dynamic, based on attachments)
	//   separator:       1 newline between display and input
	//   remaining lines: display (elastic)
	//
	// Total = display + inputBox + statusBar = H
	inputBoxHeight := m.input.Height()
	m.display.SetHeight(max(0, m.windowHeight-inputBoxHeight-1))
	m.display.updateContent()
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

	// Input area — empty bordered box (blurred) while MCP init or
	// post-loading is in progress, same as confirm overlay behavior.
	sb.WriteString(m.input.RenderWithBorder(
		m.overlays.IsConfirmOpen() || m.overlays.IsMCPInitOpen() || m.postLoading))
	sb.WriteString("\n")

	// Status bar (simplified - just render directly)
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

var _ tea.Model = (*Terminal)(nil)

func (m *Terminal) applyTheme(theme *theme.Theme) {
	m.styles = NewStyles(theme)
	m.out.SetStyles(m.styles)
	m.display.SetStyles(m.styles)
	m.input.SetStyles(m.styles)
	m.overlays.SetStyles(m.styles)
	m.display.updateContent()
}
