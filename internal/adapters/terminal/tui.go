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
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

// emitCommand sends a user-level command to the session via TLV.
// Errors are ignored — commands are best-effort.
func (m *Terminal) emitCommand(cmd string) {
	_ = stream.WriteTLV(m.streamInput, stream.TagUserT, cmd) //nolint:errcheck
}

// emitMB sends a message boundary tag, flushing any staged content
// as a complete user message.
func (m *Terminal) emitMB() {
	_ = stream.WriteTLV(m.streamInput, stream.TagMessageBoundary, "") //nolint:errcheck
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
	display        DisplayModel
	input          InputModel
	modelSelector  *ModelSelector
	themeSelector  *ThemeSelector
	themeManager   *ThemeManager
	helpWindow     *HelpWindow
	confirmOverlay *ConfirmDialog

	// Status bar state (simplified - no separate struct)
	statusText string
	inProgress bool

	// State
	quitting           bool
	confirmFromCommand bool   // tracks if cancel came from :cancel command (vs Ctrl+G)
	focusedWindow      string // "input" or "display"
	windowWidth        int
	windowHeight       int
	styles             *Styles
	hasFocus           bool   // tracks whether the terminal has application focus
	activeTheme        string // cached from system info updates
	appliedTheme       string // last theme name that was visually applied (for detecting session-driven changes)

	// Theme preview debouncing
	themePreviewID int // ID of the current pending theme preview
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

	m := &Terminal{
		out:            out,
		streamInput:    inputWriter,
		appConfig:      appCfg,
		editor:         editor,
		display:        NewDisplayModel(out.WindowBuffer(), styles),
		input:          NewInputModel(styles),
		modelSelector:  NewModelSelector(styles),
		themeSelector:  NewThemeSelector(styles),
		themeManager:   themeManager,
		helpWindow:     NewHelpWindow(styles),
		confirmOverlay: NewConfirmDialog(styles),
		windowWidth:    initialWidth,
		windowHeight:   initialHeight,
		styles:         styles,
		focusedWindow:  "input",
		hasFocus:       true,
		activeTheme:    themeName,
		appliedTheme:   themeName,
	}

	// Initialize component widths
	m.display.SetWidth(initialWidth)
	m.input.SetWidth(initialWidth)
	m.modelSelector.SetSize(initialWidth, initialHeight)
	m.themeSelector.SetSize(initialWidth, initialHeight)
	m.helpWindow.SetSize(initialWidth, initialHeight)
	m.confirmOverlay.SetSize(initialWidth, initialHeight)
	m.updateDisplayHeight()

	return m
}

// Init starts the periodic tick loop for processing session updates.
func (m *Terminal) Init() tea.Cmd {
	// Display any buffered warnings from initialization
	if m.themeManager != nil {
		if warnings := m.themeManager.GetWarnings(); len(warnings) > 0 {
			for _, w := range warnings {
				m.out.WriteError("%s", w.Message)
			}
		}
	}

	return tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
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
		m.input.updateFromMsg(msg)
		return m, nil
	}

	// Default: pass to input component
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
	m.modelSelector.SetSize(msg.Width, msg.Height)
	m.themeSelector.SetSize(msg.Width, msg.Height)
	m.helpWindow.SetSize(msg.Width, msg.Height)
	m.confirmOverlay.SetSize(msg.Width, msg.Height)
	m.updateDisplayHeight()

	// Clamp cursor to valid bounds (windows may have been removed) but
	// don't scroll to make it visible — the user's scroll position is
	// preserved across resizes and suspend/resume cycles.
	m.display.ClampCursor()

	// Re-render display content with new width (windowBuffer was marked dirty by SetWindowWidth)
	m.display.updateContent()

	return m, nil
}

// handleTick processes periodic updates for display and model switching.
func (m *Terminal) handleTick() (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Check for pending tool confirmation from the session
	if !m.confirmOverlay.IsOpen() {
		if id, toolName, toolInput, ok := m.out.GetPendingToolConfirm(); ok {
			m.openConfirmTool(id, toolName, toolInput)
		}
	}

	// Check if display needs refresh (dirty flag)
	if m.out.DrainDirty() {
		if m.out.WindowBuffer().WindowCount() > 0 {
			m.updateStatus()
			m.updateDisplayHeight()
			if m.display.shouldFollow() {
				m.display.SetCursorToLastWindow()
			}
			m.display.updateContent()
		}

		// Update model selector if models changed
		modelSnap := m.out.SnapshotModels()
		cmd = m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)
	} else {
		m.updateStatus()
	}
	return m, tea.Batch(
		tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
			return tickMsg{}
		}),
		cmd,
	)
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
			// Strip trailing newlines that text editors add by default
			content := strings.TrimRight(msg.Content, "\n")
			m.input.editorContent = content
			m.input.SetValue(FormatEditorContent(content))
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
func (m *Terminal) updateStatus() {
	snap := m.out.SnapshotStatus()

	valStyle := m.styles.Status.Foreground(m.styles.ColorMuted)

	// Build status segments - each rendered separately with appropriate colors
	var segments []string

	// Switch indicators segment (compact: "R1✦ F↓" in one segment)
	var switches []string
	if snap.ReasoningLevel > config.ReasoningLevelOff {
		reasonStyle := m.styles.Status.Foreground(m.styles.ColorAccent).Bold(true)
		switches = append(switches, reasonStyle.Render(fmt.Sprintf("R%d✦", snap.ReasoningLevel)))
	}
	if m.display.shouldFollow() {
		switches = append(switches, valStyle.Render("F↓"))
	}
	if len(switches) > 0 {
		segments = append(segments, strings.Join(switches, " "))
	}

	// Context segment
	if snap.ContextTokens > 0 {
		var ctxVal string
		if snap.ContextLimit > 0 {
			pct := float64(snap.ContextTokens) * 100.0 / float64(snap.ContextLimit)
			ctxVal = fmt.Sprintf("%s/%s %.1f%%", formatTokenCount(snap.ContextTokens), formatTokenCount(snap.ContextLimit), pct)
		} else {
			ctxVal = formatTokenCount(snap.ContextTokens)
		}
		segments = append(segments, valStyle.Render(ctxVal))
	}

	// Steps segment (rightmost — show only when there's step activity)
	if stepVal := statusStepsSegment(snap.LastMaxSteps, snap.TaskError, snap.LastCurrentStep,
		snap.InProgress, snap.CurrentStep, snap.MaxSteps); stepVal != "" {
		segments = append(segments, valStyle.Render(stepVal))
	}

	// Video config segment (last)
	if fps := snap.VideoFPS; fps > 0 {
		segments = append(segments, valStyle.Render(fmt.Sprintf("V:%d,%d", fps, snap.VideoRes)))
	}

	// Join segments with dimmed separator
	var status string
	if len(segments) > 0 {
		separator := m.styles.Status.Render("|")
		status = segments[0]
		for i := 1; i < len(segments); i++ {
			status += " " + separator + " " + segments[i]
		}
	}

	m.statusText = status
	m.inProgress = snap.InProgress

	m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)
	m.activeTheme = snap.ActiveTheme
}

// statusStepsSegment returns the steps status string, or "" if no activity.
func statusStepsSegment(lastMaxSteps int, taskError bool, lastCurrentStep int, inProgress bool, currentStep int, maxSteps int) string {
	if lastMaxSteps > 0 && taskError {
		return fmt.Sprintf("%d/%d", lastCurrentStep, lastMaxSteps)
	}
	if inProgress && currentStep > 0 {
		if maxSteps > 0 {
			return fmt.Sprintf("%d/%d", currentStep, maxSteps)
		}
		return fmt.Sprintf("%d/INF", currentStep)
	}
	return ""
}

// syncThemeFromSession checks if the session has reported a different active
// theme and applies it visually if so.
// This is the convergence point for both :theme_set and theme selector confirm.
// Theme data is resolved by sessionState.updateTheme from the cached list;
// the disk fallback handles older sessions that don't send theme_list.
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
	var sb strings.Builder

	// Display area
	sb.WriteString(m.display.View().Content)
	sb.WriteString("\n")

	// Input area (empty border when confirm overlay blocks input)
	sb.WriteString(m.input.RenderWithBorder(m.confirmOverlay.IsOpen()))

	// Status bar (simplified - just render directly)
	sb.WriteString("\n")
	sb.WriteString(m.renderStatusBar())

	baseContent := sb.String()

	// Layer 1: Regular overlay windows (model selector, theme selector, help).
	// These are mutually exclusive — only one can be open at a time.
	overlayContent := baseContent

	switch {
	case m.modelSelector.IsOpen():
		overlayContent = m.modelSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
	case m.themeSelector.IsOpen():
		overlayContent = m.themeSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
	case m.helpWindow.IsOpen():
		overlayContent = m.helpWindow.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
	}

	// Layer 2: Confirm dialog — rendered ON TOP of any regular overlay.
	// Confirm is a separate layer because it must be visible even when
	// another overlay (e.g. model selector) is active, for example when
	// a tool confirmation arrives while the model selector is open.
	if m.confirmOverlay.IsOpen() {
		fullContent := m.confirmOverlay.RenderOverlay(overlayContent, m.windowWidth, m.windowHeight)
		v := tea.NewView(fullContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	v := tea.NewView(overlayContent)
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

// formatTokenCount returns a compact human-readable representation of a
// token count (e.g. 1500 → "1.5K", 1000000 → "1M").
func formatTokenCount(n int64) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		v := float64(n) / 1_000
		if v == math.Floor(v) {
			return fmt.Sprintf("%.0fK", v)
		}
		return fmt.Sprintf("%.1fK", v)
	}
	v := float64(n) / 1_000_000
	if v == math.Floor(v) {
		return fmt.Sprintf("%.0fM", v)
	}
	return fmt.Sprintf("%.1fM", v)
}

// renderStatusBar renders the status bar line.
func (m *Terminal) renderStatusBar() string {
	var indicator string
	if m.inProgress {
		indicator = m.styles.Status.Foreground(m.styles.ColorSuccess).Render("•")
	} else {
		indicator = m.styles.Status.Foreground(m.styles.ColorDim).Render("·")
	}

	if m.statusText != "" {
		padding := m.styles.Status.Padding(0, 2)
		return padding.Render(indicator + " " + m.statusText)
	}
	return m.styles.Status.Padding(0, 2).Render(indicator)
}

// Ensure Terminal implements tea.Model
var _ tea.Model = (*Terminal)(nil)

// ============================================================================
// Focus Management
// ============================================================================

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	if m.focusedWindow == focusDisplay {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.focusedWindow = focusInput
	m.display.SetDisplayFocused(false)
	m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.focusedWindow = focusDisplay
	m.display.SetDisplayFocused(true)
	m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display.SetCursorToLastWindow()
	}
}

// openModelSelector opens the model selector UI.
func (m *Terminal) openModelSelector() {
	m.modelSelector.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m *Terminal) restoreFocus() {
	if m.focusedWindow == focusDisplay {
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

	m.themeSelector.Open(m.themeManager.GetThemes(), m.activeTheme)
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openHelpWindow opens the help window UI.
func (m *Terminal) openHelpWindow() {
	m.helpWindow.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmQuit opens the quit confirmation dialog.
func (m *Terminal) openConfirmQuit() {
	m.confirmOverlay.OpenQuit()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m *Terminal) openConfirmCancel() {
	m.confirmOverlay.OpenCancel()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m *Terminal) openConfirmTool(id, toolName, toolInput string) {
	m.confirmOverlay.OpenTool(id, toolName, toolInput)
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
	m.modelSelector.SetStyles(m.styles)
	m.themeSelector.SetStyles(m.styles)
	m.helpWindow.SetStyles(m.styles)
	m.confirmOverlay.Styles = m.styles
	m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.modelSelector.SetHasFocus(false)
	m.themeSelector.SetHasFocus(false)
	m.helpWindow.SetHasFocus(false)
	m.confirmOverlay.HasFocus = false
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true

	m.modelSelector.SetHasFocus(true)
	m.themeSelector.SetHasFocus(true)
	m.helpWindow.SetHasFocus(true)
	m.confirmOverlay.HasFocus = true

	if m.modelSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.themeSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.helpWindow.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.confirmOverlay.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.focusedWindow == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display.updateContent()

	return m, nil
}
