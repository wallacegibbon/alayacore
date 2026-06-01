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
	ansi "github.com/charmbracelet/x/ansi"
)

// ConfirmContentRows defines the fixed number of content lines inside
// the confirm dialog border. Matches SelectorListRows (8) used by
// QueueManager, ModelSelector, ThemeSelector, and HelpWindow.
// If content exceeds this, it gets truncated with a "..." indicator —
// same pattern used by QueueManager for items.
const ConfirmContentRows = 8

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
// The dialog has a fixed content height (ConfirmContentRows) matching
// the pattern used by QueueManager, ModelSelector, and other overlays.
func (cd *ConfirmDialog) View() tea.View {
	if !cd.IsOpen() {
		return tea.NewView("")
	}

	// Build the message lines (pre-styled with Confirm/System styles)
	msgLines := cd.buildContentLines()

	// Pad to the fixed content height, same as QueueManager
	for len(msgLines) < ConfirmContentRows {
		msgLines = append(msgLines, "")
	}

	// Join with newlines
	content := strings.Join(msgLines, "\n")

	// Render with bordered box — border uses error color for visual warning.
	// Pass fixed height so the window is always the same size, same as
	// QueueManager and ModelSelector overlays.
	box := cd.Styles.RenderBorderedBox(content, cd.Width, cd.Styles.ColorError, ConfirmContentRows)

	// Blank line underneath for consistent vertical positioning with other overlays
	return tea.NewView(box + "\n")
}

// buildContentLines returns the display lines for the dialog content,
// with styles already applied (Confirm for the question, System for hints).
// (Borders are applied by View via RenderBorderedBox.)
//
// The dialog has a fixed layout: the body (question + tool input) occupies
// the upper rows, then "y / n" is always on the penultimate row and an
// empty row is always on the last row. If the body overflows, the tool
// input is truncated with "..." — same pattern used by QueueManager.
func (cd *ConfirmDialog) buildContentLines() []string {
	innerWidth := max(0, cd.Width-BorderInnerPadding)
	// Reserve last 2 rows for "y / n" prompt and trailing empty line
	maxBodyLines := max(0, ConfirmContentRows-2)

	var body []string

	switch cd.Kind {
	case ConfirmQuit:
		body = []string{""}
		body = append(body, cd.wrapAndCenter("Exit AlayaCore?", cd.Styles.Confirm, innerWidth)...)
		body = append(body, "")
	case ConfirmCancel:
		body = []string{""}
		body = append(body, cd.wrapAndCenter("Cancel current task?", cd.Styles.Confirm, innerWidth)...)
		body = append(body, "")
	case ConfirmCancelAll:
		body = []string{""}
		body = append(body, cd.wrapAndCenter("Cancel all queued tasks?", cd.Styles.Confirm, innerWidth)...)
		body = append(body, "")
	case ConfirmTool:
		msg := "Allow "
		if cd.ToolName != "" {
			msg += fmt.Sprintf("%q", cd.ToolName)
		} else {
			msg += "this tool"
		}
		msg += " to run?"
		body = []string{""}
		body = append(body, cd.wrapAndCenter(msg, cd.Styles.Confirm, innerWidth)...)
		if cd.ToolInput != "" {
			// Show first line of tool input in muted style, truncated
			// like queue items: wrap-check at innerWidth, then truncate
			// with "..." if it doesn't fit on a single line.
			inputLine := cd.ToolInput
			if nl := strings.IndexByte(inputLine, '\n'); nl >= 0 {
				inputLine = inputLine[:nl]
			}
			if inputLine != "" {
				// Truncate width-wise like queue items do
				truncated := ansi.Hardwrap(inputLine, innerWidth, false)
				if truncated != inputLine {
					truncated = ansi.Hardwrap(inputLine, innerWidth-3, false)
					inputLine = strings.SplitN(truncated, "\n", 2)[0] + "..."
				}
				body = append(body, "")
				body = append(body, cd.wrapAndCenter(inputLine, cd.Styles.System, innerWidth)...)
			}
		}
		body = append(body, "")
	default:
		return nil
	}

	// If the body exceeds the available rows, truncate the body's content
	// (the tool input section) and replace the last line with "...".
	if len(body) > maxBodyLines {
		body = body[:maxBodyLines-1]
		body = append(body, cd.wrapAndCenter("...", cd.Styles.System, innerWidth)[0])
	}

	// Pad body to fill remaining rows before the fixed footer
	for len(body) < maxBodyLines {
		body = append(body, "")
	}

	// Append fixed footer: "y / n" on the penultimate row,
	// empty row on the last row.
	lines := body
	lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)[0])
	lines = append(lines, "")

	return lines
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
