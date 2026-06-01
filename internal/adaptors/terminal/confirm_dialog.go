package terminal

// ConfirmDialog renders a centered floating overlay for confirmation dialogs.
// Used for quit, cancel, cancel_all, and tool_confirm prompts.
//
// The dialog uses the same rendering pattern as ModelSelector and QueueManager:
//   - SetSize stores the terminal dimensions
//   - View renders with RenderBorderedBox
//   - RenderOverlay delegates to the shared overlay renderer
//
// Key handling: y/Y = confirm, n/N/esc/Ctrl+C = cancel.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ConfirmKind represents the type of active confirmation dialog.
type ConfirmKind int

const (
	ConfirmNone      ConfirmKind = iota // No dialog active
	ConfirmQuit                         // Confirm exit
	ConfirmCancel                       // Confirm cancel current request
	ConfirmCancelAll                    // Confirm cancel all queued requests
	ConfirmTool                         // Confirm tool execution
)

// ConfirmDialog manages a floating confirmation overlay.
//
// Renders as a centered dialog box:
//
//	┌──────────────────────────────┐
//	│                              │
//	│     Exit AlayaCore?          │
//	│                              │
//	│     y / n                    │
//	│                              │
//	└──────────────────────────────┘
//
// For tool confirmations, the tool input is shown below the message:
//
//	┌──────────────────────────────┐
//	│                              │
//	│  Allow "read_file" to run?   │
//	│  read_file: /path/to/file    │
//	│                              │
//	│     y / n                    │
//	│                              │
//	└──────────────────────────────┘
//
// Key handling: y/Y = confirm, n/N/esc/Ctrl+C = cancel.
type ConfirmDialog struct {
	// Core state — follows the FilteredListCore/ScrollableListCore pattern.
	State    FilteredListState
	Kind     ConfirmKind
	HasFocus bool
	Styles   *Styles

	// Width is set by SetSize with the full terminal width, matching
	// the overlay pattern used by ModelSelector, QueueManager, etc.
	Width  int
	Height int

	// Tool confirm fields (only used for ConfirmTool kind)
	ToolID    string
	ToolName  string
	ToolInput string

	// Result flags — consumed by the Terminal after key handling.
	// Only one of these is set per interaction.
	Confirmed bool
	Canceled  bool
}

// NewConfirmDialog creates a new confirm dialog.
func NewConfirmDialog(styles *Styles) *ConfirmDialog {
	return &ConfirmDialog{
		Styles: styles,
	}
}

// SetSize updates the terminal dimensions for responsive sizing.
// Called on initialization and terminal resize.
func (cd *ConfirmDialog) SetSize(width, height int) {
	if width > 0 {
		cd.Width = width
	}
	cd.Height = height
}

// IsOpen returns true if the dialog is currently shown.
func (cd *ConfirmDialog) IsOpen() bool {
	return cd.State != FilteredListClosed
}

// ---- Open / Close ----

// OpenQuit opens the dialog for confirming application exit.
func (cd *ConfirmDialog) OpenQuit() {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmQuit
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// OpenCancel opens the dialog for confirming task cancellation.
func (cd *ConfirmDialog) OpenCancel() {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmCancel
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// OpenCancelAll opens the dialog for confirming cancellation of all tasks.
func (cd *ConfirmDialog) OpenCancelAll() {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmCancelAll
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// OpenTool opens the dialog for confirming a tool call.
func (cd *ConfirmDialog) OpenTool(toolID, toolName, toolInput string) {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmTool
	cd.ToolID = toolID
	cd.ToolName = toolName
	cd.ToolInput = toolInput
	cd.Confirmed = false
	cd.Canceled = false
}

// Close closes the dialog without committing any action.
// This is equivalent to the user pressing esc.
func (cd *ConfirmDialog) Close() {
	cd.State = FilteredListClosed
	cd.Kind = ConfirmNone
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// ---- Key Handling ----

// HandleKeyMsg processes a key press and updates state.
// Returns true if the key was consumed by the dialog.
func (cd *ConfirmDialog) HandleKeyMsg(msg tea.KeyMsg) bool {
	if !cd.IsOpen() {
		return false
	}

	key := msg.String()

	switch key {
	case keyY, keyYCapital:
		cd.Confirmed = true
		cd.State = FilteredListClosed
		return true

	case keyN, keyNCapital, keyEsc, keyCtrlC:
		cd.Canceled = true
		cd.State = FilteredListClosed
		return true
	}

	// All other keys are consumed while the dialog is open
	return true
}

// ConsumeResult returns the result flags and resets them.
// Returns (confirmed, canceled).
func (cd *ConfirmDialog) ConsumeResult() (bool, bool) {
	confirmed := cd.Confirmed
	canceled := cd.Canceled
	cd.Confirmed = false
	cd.Canceled = false
	return confirmed, canceled
}

// ---- Rendering ----

// View returns the rendered dialog content as a tea.View.
// Uses RenderBorderedBox for consistent styling with other overlays.
func (cd *ConfirmDialog) View() tea.View {
	if !cd.IsOpen() {
		return tea.NewView("")
	}

	// Build the message lines (pre-styled with Confirm/System styles)
	msgLines := cd.buildContentLines()

	// Join with newlines
	content := strings.Join(msgLines, "\n")

	// Render with bordered box — border uses error color for visual warning
	box := cd.Styles.RenderBorderedBox(content, cd.Width, cd.Styles.ColorError)

	return tea.NewView(box)
}

// buildContentLines returns the display lines for the dialog content,
// with styles already applied (Confirm for the question, System for hints).
// (Borders are applied by View via RenderBorderedBox.)
func (cd *ConfirmDialog) buildContentLines() []string {
	innerWidth := max(0, cd.Width-BorderInnerPadding)

	switch cd.Kind {
	case ConfirmQuit:
		lines := []string{""}
		lines = append(lines, cd.wrapAndCenter("Exit AlayaCore?", cd.Styles.Confirm, innerWidth)...)
		lines = append(lines, "")
		lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)...)
		lines = append(lines, "")
		return lines
	case ConfirmCancel:
		lines := []string{""}
		lines = append(lines, cd.wrapAndCenter("Cancel current task?", cd.Styles.Confirm, innerWidth)...)
		lines = append(lines, "")
		lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)...)
		lines = append(lines, "")
		return lines
	case ConfirmCancelAll:
		lines := []string{""}
		lines = append(lines, cd.wrapAndCenter("Cancel all queued tasks?", cd.Styles.Confirm, innerWidth)...)
		lines = append(lines, "")
		lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)...)
		lines = append(lines, "")
		return lines
	case ConfirmTool:
		msg := "Allow "
		if cd.ToolName != "" {
			msg += fmt.Sprintf("%q", cd.ToolName)
		} else {
			msg += "this tool"
		}
		msg += " to run?"
		lines := []string{""}
		lines = append(lines, cd.wrapAndCenter(msg, cd.Styles.Confirm, innerWidth)...)
		if cd.ToolInput != "" {
			// Show first line of tool input in muted style
			inputLine := cd.ToolInput
			if nl := strings.IndexByte(inputLine, '\n'); nl >= 0 {
				inputLine = inputLine[:nl]
			}
			if inputLine != "" {
				lines = append(lines, "")
				lines = append(lines, cd.wrapAndCenter(inputLine, cd.Styles.System, innerWidth)...)
			}
		}
		lines = append(lines, "")
		lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)...)
		lines = append(lines, "")
		return lines
	default:
		return nil
	}
}

// wrapAndCenter styles, wraps, and centers text for display in the dialog.
// When text wraps to multiple lines, the entire block is centered as a unit
// so all lines share the same left margin — no jagged alignment.
func (cd *ConfirmDialog) wrapAndCenter(text string, style lipgloss.Style, width int) []string {
	// Style the text
	styled := style.Render(text)

	// Wrap at the given width
	wrapped := wrapContent(styled, width)

	// Split into lines
	rawLines := strings.Split(wrapped, "\n")

	// Find the widest line (in display columns)
	maxLineWidth := 0
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}

	// Pad all lines to the same width, then add identical left padding
	// so the whole block is centered as one unit.
	blockPadding := max(0, (width-maxLineWidth)/2)
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		rightPad := maxLineWidth - w
		// Add right padding so all lines are equal width, then left padding for centering
		lines = append(lines, strings.Repeat(" ", blockPadding)+line+strings.Repeat(" ", rightPad))
	}
	return lines
}

// RenderOverlay renders the dialog as a centered overlay on top of base content.
// Uses the same renderOverlay function as all other overlays.
func (cd *ConfirmDialog) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if !cd.IsOpen() {
		return baseContent
	}
	return renderOverlay(baseContent, cd.View().Content, screenWidth, screenHeight)
}
